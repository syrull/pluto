package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/pluto/harness/internal/tool"
)

// Find searches file contents with ripgrep and returns bounded path:line:text matches.
type Find struct{}

var _ tool.Tool = Find{}

func (Find) Name() string { return "find" }
func (Find) Description() string {
	return "Search file contents across a tree and return `path:line:text` matches. " +
		"This is the primary way to search code — always prefer it over " +
		"grep/rg/ag/find through the bash tool. Backed by ripgrep, it caps matches " +
		"per file, total lines, and output bytes so results can't overflow the " +
		"context window. Pass a regex pattern, an optional path to scope the search, " +
		"and an optional glob to include/exclude files."
}

func (Find) Schema() json.RawMessage {
	return tool.ObjectSchema(map[string]tool.Property{
		"pattern": {Type: "string", Description: "Regex pattern to search for (ripgrep syntax)."},
		"path":    {Type: "string", Description: "File or directory to search (default: current directory)."},
		"glob":    {Type: "string", Description: "Optional glob to include/exclude files, e.g. '*.go' or '!vendor/*'."},
	}, "pattern").MustJSON()
}

type findArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Glob    string `json:"glob"`
}

// Output bounds guarantee the result can't overflow the context window.
const (
	findMaxCountPerFile = 50
	findMaxLines        = 200
	findMaxBytes        = 32 * 1024
)

func (Find) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a findArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("find: invalid arguments: %w", err)
	}
	if strings.TrimSpace(a.Pattern) == "" {
		return "", fmt.Errorf("find: pattern is required")
	}

	path := a.Path
	if strings.TrimSpace(path) == "" {
		path = "."
	}

	rgArgs := []string{
		"--line-number",
		"--with-filename",
		"--color", "never",
		"--no-heading",
		"--max-count", strconv.Itoa(findMaxCountPerFile),
	}
	if g := strings.TrimSpace(a.Glob); g != "" {
		rgArgs = append(rgArgs, "--glob", g)
	}
	rgArgs = append(rgArgs, "--", a.Pattern, path)

	// Read stdout through a bounded reader so a huge match set can't buffer
	// unbounded memory before truncation. We cancel rg as soon as we've read
	// enough bytes to satisfy boundOutput's line/byte caps.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rg", rgArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("find: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("find: %w", err)
	}

	// findReadLimit is the most stdout we ever hold: enough headroom for the
	// byte cap plus a little slack so truncation always operates on full data.
	const findReadLimit = findMaxBytes + 8*1024
	data, _ := io.ReadAll(io.LimitReader(stdout, findReadLimit))
	// Stop rg promptly; we have all we can use. Drain the pipe so the process
	// isn't blocked on a full stdout buffer while it exits.
	cancel()
	_, _ = io.Copy(io.Discard, stdout)
	err = cmd.Wait()

	// ripgrep exits 1 when there are simply no matches; that is not an error
	// for us. A signal kill from our cancel() is expected once we've read our
	// fill. Any other nonzero exit with no output is a real failure.
	var exitErr *exec.ExitError
	if len(data) == 0 {
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "no matches", nil
		}
		if err != nil && !errors.As(err, &exitErr) {
			return "", fmt.Errorf("find: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "no matches", nil
	}

	return boundOutput(string(data)), nil
}

func boundOutput(out string) string {
	out = strings.TrimRight(out, "\n")
	lines := strings.Split(out, "\n")

	truncatedLines := false
	if len(lines) > findMaxLines {
		lines = lines[:findMaxLines]
		truncatedLines = true
	}
	result := strings.Join(lines, "\n")

	truncatedBytes := false
	if len(result) > findMaxBytes {
		// Trim to the last full line within the byte budget.
		result = result[:findMaxBytes]
		if i := strings.LastIndexByte(result, '\n'); i > 0 {
			result = result[:i]
		}
		truncatedBytes = true
	}

	if truncatedLines || truncatedBytes {
		result += "\n... (output truncated; narrow the pattern, path, or glob to see more)"
	}
	return result
}
