package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

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

// stopGrace bounds each stage of a graceful subprocess shutdown (EOF → SIGTERM →
// SIGKILL) so a stuck server can't wedge pluto's exit for long.
const stopGrace = 2 * time.Second

// startStdioProcess spawns the configured command and connects to it over stdio.
// ctx is intentionally NOT applied to the process (via exec.CommandContext): the
// server must outlive the bounded dial context and stay alive for the session;
// the handshake that follows is what the caller bounds with ctx.
func startStdioProcess(_ context.Context, name string, cfg ServerConfig) (*stdioConn, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = mergeEnv(cfg.Env)
	// Forward the server's stderr diagnostics to the debug log rather than our own
	// stderr: pluto runs a full-screen (alt-screen) TUI, so raw writes would paint
	// over and corrupt the display. os/exec drains this writer on its own goroutine
	// and Wait finishes it, so no pipe bookkeeping is needed.
	cmd.Stderr = &stderrLogger{server: name}
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
	stop := func() error { return stopProcess(cmd, stdin) }
	return newStdioConn(name, stdout, stdin, stop), nil
}

// stopProcess tears a server down gracefully: closing stdin signals EOF so a
// well-behaved server exits on its own, then it escalates to SIGTERM and finally
// SIGKILL, each after stopGrace, so a stuck process is still reaped.
func stopProcess(cmd *exec.Cmd, stdin io.Closer) error {
	_ = stdin.Close()
	if cmd.Process == nil {
		return cmd.Wait()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(stopGrace):
	}
	_ = cmd.Process.Signal(syscall.SIGTERM) // best-effort; a no-op on platforms without it
	select {
	case err := <-done:
		return err
	case <-time.After(stopGrace):
	}
	_ = cmd.Process.Kill()
	return <-done
}

// inheritedEnvVars is the curated allowlist a spawned stdio server inherits from
// pluto's environment. It deliberately omits secrets (API keys, OAuth tokens, and
// anything else in the parent env) so a third-party server never receives pluto's
// credentials — a server gets a secret only when its config "env" block sets it.
var inheritedEnvVars = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TERM",
	"LANG", "LC_ALL", "LC_CTYPE", "TZ", "TMPDIR",
	// Windows equivalents so stdio servers resolve their runtime there too.
	"SYSTEMROOT", "SYSTEMDRIVE", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
	"PROGRAMFILES", "PROGRAMDATA", "TEMP", "TMP", "PATHEXT",
}

// mergeEnv overlays the server's declared env on the curated base environment
// (see inheritedEnvVars). The full parent environment is intentionally NOT passed
// through, so secrets in pluto's own env don't leak into third-party servers.
func mergeEnv(extra map[string]string) []string {
	env := make([]string, 0, len(inheritedEnvVars)+len(extra))
	for _, k := range inheritedEnvVars {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// stderrLogger forwards a spawned server's stderr to the debug log line by line
// so its diagnostics never corrupt the alt-screen TUI. os/exec writes to it from
// a single goroutine, so it needs no locking; a partial final line (no trailing
// newline) is flushed when the write exceeds maxStderrBuf.
type stderrLogger struct {
	server string
	buf    []byte
}

// maxStderrBuf caps the pending-line buffer so a server that streams without
// newlines can't grow it without bound.
const maxStderrBuf = 64 * 1024

func (l *stderrLogger) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.emit(l.buf[:i])
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > maxStderrBuf {
		l.emit(l.buf)
		l.buf = l.buf[:0]
	}
	return len(p), nil
}

func (l *stderrLogger) emit(line []byte) {
	s := strings.TrimRight(string(line), "\r")
	if s != "" {
		debug.Debug("mcp", "server stderr", "server", l.server, "line", s)
	}
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
