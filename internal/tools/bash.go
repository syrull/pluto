package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/pluto/harness/internal/tool"
)

// Bash runs a shell command via sh -c and returns combined stdout and stderr.
type Bash struct{}

var _ tool.Tool = Bash{}

func (Bash) Name() string { return "bash" }
func (Bash) Description() string {
	return "Run a shell command via `sh -c` and return its combined stdout and stderr. " +
		"To read files use the read tool (not cat/head/tail/sed) and to search file " +
		"contents use the find tool (not grep/rg/ag) — both bound their output so it " +
		"can't overflow the context window. Simple cat/grep/rg commands issued here " +
		"are transparently routed to those tools."
}

func (Bash) Schema() json.RawMessage {
	return tool.ObjectSchema(map[string]tool.Property{
		"command": {Type: "string", Description: "Shell command to execute."},
		"timeout": {Type: "integer", Description: "Timeout in seconds (default 60, max 600)."},
	}, "command").MustJSON()
}

type bashArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// bashDefaultTimeout and bashMaxTimeout bound how long a command may run.
const (
	bashDefaultTimeout = 60 * time.Second
	bashMaxTimeout     = 600 * time.Second
)

func (Bash) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a bashArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("bash: invalid arguments: %w", err)
	}
	if strings.TrimSpace(a.Command) == "" {
		return "", fmt.Errorf("bash: command is required")
	}

	// Redirect trivially simple file-read/content-search commands to the Read
	// and Find tools, whose output is bounded. Anything with shell syntax falls
	// through to a real subshell below.
	if out, err, handled := redirectBash(ctx, a.Command); handled {
		return out, err
	}

	timeout := bashDefaultTimeout
	if a.Timeout > 0 {
		timeout = time.Duration(a.Timeout) * time.Second
		if timeout > bashMaxTimeout {
			timeout = bashMaxTimeout
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", a.Command)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	out := capOutput(buf.String())

	if ctx.Err() == context.DeadlineExceeded {
		return formatBashResult(out, fmt.Sprintf("timed out after %s", timeout)), nil
	}
	if err != nil {
		return formatBashResult(out, err.Error()), nil
	}
	return formatBashResult(out, ""), nil
}

// formatBashResult renders command output, appending a status line when the
// command failed or timed out. Nonzero exits are reported, not raised, so the
// model sees the failure and can react rather than aborting the turn.
func formatBashResult(out, status string) string {
	out = strings.TrimRight(out, "\n")
	switch {
	case status == "" && out == "":
		return "(no output)"
	case status == "":
		return out
	case out == "":
		return "error: " + status
	default:
		return fmt.Sprintf("%s\nerror: %s", out, status)
	}
}

// bashMaxBytes bounds the size of returned command output. Unbounded output
// (e.g. a recursive grep) can otherwise overflow the model context window.
const bashMaxBytes = 32 * 1024

// capOutput truncates output to bashMaxBytes, keeping the tail — the end of a
// command's output (final errors, summaries) is usually the most relevant.
func capOutput(out string) string {
	if len(out) <= bashMaxBytes {
		return out
	}
	trimmed := out[len(out)-bashMaxBytes:]
	if i := strings.IndexByte(trimmed, '\n'); i >= 0 && i+1 < len(trimmed) {
		trimmed = trimmed[i+1:]
	}
	return "... (output truncated; showing last " + strconv.Itoa(len(trimmed)) + " bytes)\n" + trimmed
}
