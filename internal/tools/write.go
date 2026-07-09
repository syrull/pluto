package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pluto/harness/internal/diff"
	"github.com/pluto/harness/internal/tool"
)

// Write is a tool that writes content to a file, creating parent directories.
type Write struct{}

var _ tool.Tool = Write{}

func (Write) Name() string { return "write" }
func (Write) Description() string {
	return "Write content to a file, creating parent directories as needed."
}

func (Write) Schema() json.RawMessage {
	return tool.ObjectSchema(map[string]tool.Property{
		"path":    {Type: "string", Description: "Filesystem path to write."},
		"content": {Type: "string", Description: "Content to write to the file."},
	}, "path", "content").MustJSON()
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (Write) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a writeArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("write: invalid arguments: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("write: path is required")
	}

	old, _ := os.ReadFile(a.Path)

	if dir := filepath.Dir(a.Path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("write: create dir: %w", err)
		}
	}
	if err := os.WriteFile(a.Path, []byte(a.Content), 0o644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}

	return formatWriteResult(a.Path, string(old), a.Content), nil
}
func formatWriteResult(path, old, new string) string {
	header := fmt.Sprintf("wrote %d bytes to %s", len(new), path)
	if old == new {
		return header + " (no change)"
	}
	result := diff.Compute(old, new)
	if result.TooLarge {
		return header
	}
	added, removed := diff.Stats(result.Lines)
	return fmt.Sprintf("%s (+%d -%d)", header, added, removed)
}

func withDiffBody(header, old, new string) string {
	if old == new {
		return header + " (no change)"
	}
	result := diff.Compute(old, new)
	if result.TooLarge {
		return header
	}
	added, removed := diff.Stats(result.Lines)
	body := diff.Format(result.Lines)
	return fmt.Sprintf("%s (+%d -%d)\n%s", header, added, removed, body)
}
