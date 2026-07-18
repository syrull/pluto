package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/syrull/pluto/internal/debug"
)

// stdioConn speaks newline-delimited JSON-RPC over a subprocess's stdin/stdout.
// A single background reader routes each response to the waiting caller by id,
// so calls are safe to make concurrently.
type stdioConn struct {
	name string
	w    io.WriteCloser
	stop func() error // tears down the process/pipes

	writeMu sync.Mutex // serializes frame writes
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan rpcIncoming
	closed  bool
	readErr error
}

// newStdioConn wires a connection to an already-open reader/writer pair and a
// teardown func. It is split from process spawning so it can be tested over an
// in-memory pipe without a real subprocess.
func newStdioConn(name string, r io.Reader, w io.WriteCloser, stop func() error) *stdioConn {
	c := &stdioConn{name: name, w: w, stop: stop, pending: make(map[int64]chan rpcIncoming)}
	go c.readLoop(r)
	return c
}

// startStdioProcess spawns the configured command and connects to it over stdio.
func startStdioProcess(ctx context.Context, name string, cfg ServerConfig) (*stdioConn, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = mergeEnv(cfg.Env)
	// The server's own diagnostics go to our stderr so they land in a terminal
	// scrollback; only stdout carries protocol frames.
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: stdin pipe: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: stdout pipe: %w", name, err)
	}
	debug.Info("mcp", "spawning stdio server", "server", name, "command", cfg.Command, "args", len(cfg.Args))
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: %s: start %q: %w", name, cfg.Command, err)
	}
	stop := func() error {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return cmd.Wait()
	}
	return newStdioConn(name, stdout, stdin, stop), nil
}

// mergeEnv overlays the server's declared env on the process environment.
func mergeEnv(extra map[string]string) []string {
	if len(extra) == 0 {
		return os.Environ()
	}
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// readLoop consumes framed messages, dispatching responses to waiters and
// logging (then dropping) server-initiated requests/notifications. A larger
// buffer accommodates big tool results on a single line.
func (c *stdioConn) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcIncoming
		if err := json.Unmarshal(line, &msg); err != nil {
			debug.Warn("mcp", "undecodable frame", "server", c.name, "err", err)
			continue
		}
		if msg.isResponse() {
			c.deliver(msg)
			continue
		}
		debug.Trace("mcp", "server message ignored", "server", c.name, "method", msg.Method)
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	c.failAll(err)
}

// deliver routes a response to its waiting caller, if any is still registered.
func (c *stdioConn) deliver(msg rpcIncoming) {
	c.mu.Lock()
	ch, ok := c.pending[*msg.ID]
	if ok {
		delete(c.pending, *msg.ID)
	}
	c.mu.Unlock()
	if ok {
		ch <- msg
	}
}

// failAll records the terminating read error and unblocks every waiter so no
// call hangs after the server dies.
func (c *stdioConn) failAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readErr = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	debug.Debug("mcp", "stdio read loop ended", "server", c.name, "err", err)
}

func (c *stdioConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	frame, err := marshalRequest(id, method, params)
	if err != nil {
		return nil, err
	}
	ch := make(chan rpcIncoming, 1)
	c.mu.Lock()
	if c.closed || c.readErr != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp: %s: connection closed", c.name)
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.writeFrame(frame); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case msg, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("mcp: %s: connection closed before reply", c.name)
		}
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	}
}

func (c *stdioConn) notify(_ context.Context, method string, params any) error {
	frame, err := marshalNotification(method, params)
	if err != nil {
		return err
	}
	return c.writeFrame(frame)
}

// writeFrame writes one newline-delimited frame under the write lock.
func (c *stdioConn) writeFrame(frame []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.w.Write(append(frame, '\n')); err != nil {
		return fmt.Errorf("mcp: %s: write: %w", c.name, err)
	}
	return nil
}

func (c *stdioConn) close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	if c.stop != nil {
		return c.stop()
	}
	return nil
}
