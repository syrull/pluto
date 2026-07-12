package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/syrull/pluto/internal/tool"
)

// Edit replaces an exact substring in a file; the substring must occur exactly once.
type Edit struct{}

var _ tool.Tool = Edit{}

func (Edit) Name() string { return "edit" }
func (Edit) Description() string {
	return "Replace an exact, unique substring in an existing file with new text."
}

func (Edit) Schema() json.RawMessage {
	return tool.ObjectSchema(map[string]tool.Property{
		"path": {Type: "string", Description: "Filesystem path to edit."},
		"old":  {Type: "string", Description: "Exact text to replace. Must occur exactly once in the file."},
		"new":  {Type: "string", Description: "Replacement text."},
	}, "path", "old", "new").MustJSON()
}

type editArgs struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func (Edit) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a editArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("edit: invalid arguments: %w", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("edit: path is required")
	}
	if a.Old == "" {
		return "", fmt.Errorf("edit: old is required")
	}

	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}
	old := string(data)

	// Require a unique match so the edit target is unambiguous. Distinguish
	// "not found" from "ambiguous" so the model gets actionable guidance.
	switch n := strings.Count(old, a.Old); n {
	case 0:
		return "", fmt.Errorf("edit: old text not found in %s", a.Path)
	case 1:
	default:
		return "", fmt.Errorf("edit: old text occurs %d times in %s; make it unique", n, a.Path)
	}

	new := strings.Replace(old, a.Old, a.New, 1)
	if err := os.WriteFile(a.Path, []byte(new), 0o644); err != nil {
		return "", fmt.Errorf("edit: %w", err)
	}

	return withDiffBody(a.Path, old, new), nil
}
