// Package tools holds concrete tool.Tool implementations.
package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/syrull/pluto/internal/tool"
)

// Read returns the contents of a file on disk, with optional offset/limit for line ranges.
type Read struct{}

var _ tool.Tool = Read{}

func (Read) Name() string { return "read" }
func (Read) Description() string {
	return "Read the contents of a file, returning numbered lines. This is the " +
		"primary way to view files — always prefer it over cat/head/tail/sed/less " +
		"through the bash tool. Pass offset (1-indexed start line) and limit (max " +
		"lines) to page through a large file instead of pulling it in all at once; " +
		"output is bounded so it can't overflow the context window."
}

func (Read) Schema() json.RawMessage {
	return tool.ObjectSchema(map[string]tool.Property{
		"path":   {Type: "string", Description: "Filesystem path to read."},
		"offset": {Type: "integer", Description: "1-indexed line to start from. Omit to read from the first line."},
		"limit":  {Type: "integer", Description: "Maximum number of lines to return. Omit to read to end of file."},
	}, "path").MustJSON()
}

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// readMaxBytes bounds returned file content to prevent context window overflow.
const readMaxBytes = 32 * 1024

func (Read) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a readArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("read: invalid arguments: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("read: path is required")
	}
	if a.Offset < 0 {
		return "", fmt.Errorf("read: offset must be >= 0")
	}
	if a.Limit < 0 {
		return "", fmt.Errorf("read: limit must be >= 0")
	}

	f, err := os.Open(a.Path)
	if err != nil {
		return "", fmt.Errorf("read: %w", err)
	}
	defer f.Close()

	// start is the 1-indexed first line to include; offset 0 or 1 both mean
	// "from the beginning".
	start := a.Offset
	if start < 1 {
		start = 1
	}

	var b strings.Builder
	sc := bufio.NewScanner(f)
	// Allow long lines beyond Scanner's 64KB default. The initial capacity is
	// small; the second arg is the max a token may grow to. A line longer than
	// this errors as "token too long" rather than being silently split.
	const readMaxLine = 1024 * 1024
	sc.Buffer(make([]byte, 0, 4096), readMaxLine)

	lineNo := 0
	emitted := 0
	truncatedBytes := false
	for sc.Scan() {
		lineNo++
		if lineNo < start {
			continue
		}
		if a.Limit > 0 && emitted >= a.Limit {
			break
		}
		line := strconv.Itoa(lineNo) + "\t" + sc.Text() + "\n"
		if b.Len()+len(line) > readMaxBytes {
			truncatedBytes = true
			break
		}
		b.WriteString(line)
		emitted++
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read: %w", err)
	}

	if emitted == 0 {
		if lineNo == 0 {
			return "(empty file)", nil
		}
		return fmt.Sprintf("(no lines: file has %d line(s), offset %d is past end)", lineNo, start), nil
	}
	if truncatedBytes {
		b.WriteString(fmt.Sprintf("... (truncated at %d bytes; use offset/limit to page)\n", readMaxBytes))
	}
	return b.String(), nil
}
