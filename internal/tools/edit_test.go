package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditSuccessfulReplacement(t *testing.T) {
	e := Edit{}
	dir := t.TempDir()
	filepath := filepath.Join(dir, "test.txt")

	// Seed file with known content.
	original := "The quick brown fox jumps over the lazy dog"
	if err := os.WriteFile(filepath, []byte(original), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	// Edit: replace "quick brown" with "swift golden"
	args := json.RawMessage(`{"path":"` + filepath + `","old":"quick brown","new":"swift golden"}`)
	result, err := e.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Edit.Execute() error = %v, want nil", err)
	}

	// Verify result contains header.
	if !strings.Contains(result, "edited "+filepath) {
		t.Fatalf("Edit.Execute() result missing header; got %q", result)
	}

	// Verify result contains diff stats.
	if !strings.Contains(result, "(+1 -1)") {
		t.Fatalf("Edit.Execute() result missing stats; got %q", result)
	}

	// Verify result contains old and new lines in diff.
	if !strings.Contains(result, "-The quick brown fox") {
		t.Fatalf("Edit.Execute() result missing old diff line; got %q", result)
	}
	if !strings.Contains(result, "+The swift golden fox") {
		t.Fatalf("Edit.Execute() result missing new diff line; got %q", result)
	}

	// Verify file on disk contains replacement.
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	expected := "The swift golden fox jumps over the lazy dog"
	if string(data) != expected {
		t.Fatalf("File content after edit = %q, want %q", string(data), expected)
	}
}

func TestEditZeroMatches(t *testing.T) {
	e := Edit{}
	dir := t.TempDir()
	filepath := filepath.Join(dir, "test.txt")

	// Seed file.
	original := "The quick brown fox"
	if err := os.WriteFile(filepath, []byte(original), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	// Edit: try to replace text not present.
	args := json.RawMessage(`{"path":"` + filepath + `","old":"nonexistent","new":"replacement"}`)
	result, err := e.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Edit.Execute() error = nil, want non-nil")
	}

	// Verify error message.
	if !strings.Contains(err.Error(), "old text not found") {
		t.Fatalf("Edit.Execute() error = %v, want to contain 'old text not found'", err)
	}

	// Verify result is empty.
	if result != "" {
		t.Fatalf("Edit.Execute() result = %q, want empty", result)
	}

	// Verify file unchanged.
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != original {
		t.Fatalf("File content changed after failed edit; got %q, want %q", string(data), original)
	}
}

func TestEditAmbiguousMatches(t *testing.T) {
	e := Edit{}
	dir := t.TempDir()
	filepath := filepath.Join(dir, "test.txt")

	// Seed file with repeated substring.
	original := "foo bar foo baz foo"
	if err := os.WriteFile(filepath, []byte(original), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	// Edit: try to replace "foo" which occurs 3 times.
	args := json.RawMessage(`{"path":"` + filepath + `","old":"foo","new":"qux"}`)
	result, err := e.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Edit.Execute() error = nil, want non-nil")
	}

	// Verify error message contains count and guidance.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "occurs 3 times") {
		t.Fatalf("Edit.Execute() error = %v, want to contain 'occurs 3 times'", err)
	}
	if !strings.Contains(errMsg, "make it unique") {
		t.Fatalf("Edit.Execute() error = %v, want to contain 'make it unique'", err)
	}

	// Verify result is empty.
	if result != "" {
		t.Fatalf("Edit.Execute() result = %q, want empty", result)
	}

	// Verify file unchanged.
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != original {
		t.Fatalf("File content changed after failed edit; got %q, want %q", string(data), original)
	}
}

func TestEditNonexistentFile(t *testing.T) {
	e := Edit{}
	nonexistentPath := "/nonexistent/path/to/file.txt"

	args := json.RawMessage(`{"path":"` + nonexistentPath + `","old":"text","new":"replacement"}`)
	result, err := e.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Edit.Execute() error = nil, want non-nil")
	}

	// Verify result is empty.
	if result != "" {
		t.Fatalf("Edit.Execute() result = %q, want empty", result)
	}

	// Verify error starts with "edit:" prefix.
	if !strings.HasPrefix(err.Error(), "edit:") {
		t.Fatalf("Edit.Execute() error = %v, want to start with 'edit:'", err)
	}
}

func TestEditEmptyPath(t *testing.T) {
	e := Edit{}
	args := json.RawMessage(`{"path":"","old":"text","new":"replacement"}`)
	result, err := e.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Edit.Execute() error = nil, want non-nil")
	}

	// Verify error message.
	if !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("Edit.Execute() error = %v, want to contain 'path is required'", err)
	}

	// Verify result is empty.
	if result != "" {
		t.Fatalf("Edit.Execute() result = %q, want empty", result)
	}
}

func TestEditEmptyOld(t *testing.T) {
	e := Edit{}
	dir := t.TempDir()
	filepath := filepath.Join(dir, "test.txt")

	// Seed file (path must exist to test the old-is-empty check).
	if err := os.WriteFile(filepath, []byte("content"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	args := json.RawMessage(`{"path":"` + filepath + `","old":"","new":"replacement"}`)
	result, err := e.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Edit.Execute() error = nil, want non-nil")
	}

	// Verify error message.
	if !strings.Contains(err.Error(), "old is required") {
		t.Fatalf("Edit.Execute() error = %v, want to contain 'old is required'", err)
	}

	// Verify result is empty.
	if result != "" {
		t.Fatalf("Edit.Execute() result = %q, want empty", result)
	}
}

func TestEditNoChange(t *testing.T) {
	e := Edit{}
	dir := t.TempDir()
	filepath := filepath.Join(dir, "test.txt")

	// Seed file.
	original := "The quick brown fox"
	if err := os.WriteFile(filepath, []byte(original), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	// Edit: replace "brown" with "brown" (no-op).
	args := json.RawMessage(`{"path":"` + filepath + `","old":"brown","new":"brown"}`)
	result, err := e.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Edit.Execute() error = %v, want nil", err)
	}

	// Verify result contains header and "(no change)".
	if !strings.Contains(result, "edited "+filepath) {
		t.Fatalf("Edit.Execute() result missing header; got %q", result)
	}
	if !strings.Contains(result, "(no change)") {
		t.Fatalf("Edit.Execute() result missing '(no change)'; got %q", result)
	}

	// Verify file unchanged.
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != original {
		t.Fatalf("File content changed; got %q, want %q", string(data), original)
	}
}

// TestEditMultilineReplacement verifies that editing multiline content works
// correctly with proper diff stats.
func TestEditMultilineReplacement(t *testing.T) {
	e := Edit{}
	dir := t.TempDir()
	filepath := filepath.Join(dir, "test.txt")

	// Seed file with multiline content.
	original := "line 1\nreplace me\nline 3\n"
	if err := os.WriteFile(filepath, []byte(original), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	// Edit: replace "replace me" with "replaced"
	args := json.RawMessage(`{"path":"` + filepath + `","old":"replace me","new":"replaced"}`)
	result, err := e.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Edit.Execute() error = %v, want nil", err)
	}

	// Verify result contains header and diff.
	if !strings.Contains(result, "edited "+filepath) {
		t.Fatalf("Edit.Execute() result missing header; got %q", result)
	}
	if !strings.Contains(result, "(+1 -1)") {
		t.Fatalf("Edit.Execute() result missing stats; got %q", result)
	}

	// Verify file on disk contains replacement.
	data, err := os.ReadFile(filepath)
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	expected := "line 1\nreplaced\nline 3\n"
	if string(data) != expected {
		t.Fatalf("File content after edit = %q, want %q", string(data), expected)
	}
}

func TestEditLargeFileShowsHunkNotWholeFile(t *testing.T) {
	e := Edit{}
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")

	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, "line "+string(rune('A'+i%26))+string(rune('0'+i/26)))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	target := lines[30]
	args := json.RawMessage(`{"path":"` + path + `","old":"` + target + `","new":"CHANGED"}`)
	result, err := e.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Edit.Execute() error = %v, want nil", err)
	}

	if !strings.Contains(result, "-"+target) || !strings.Contains(result, "+CHANGED") {
		t.Fatalf("result should show the change; got:\n%s", result)
	}
	if strings.Contains(result, lines[0]) || strings.Contains(result, lines[59]) {
		t.Fatalf("result should not include far-away context lines; got:\n%s", result)
	}
	if !strings.Contains(result, "unchanged line(s)") {
		t.Fatalf("result should elide distant context with a gap marker; got:\n%s", result)
	}
	if got := strings.Count(result, "\n"); got > 12 {
		t.Fatalf("expected a compact hunk, got %d body lines:\n%s", got, result)
	}
}

func TestEditInvalidJSON(t *testing.T) {
	e := Edit{}
	args := json.RawMessage(`{invalid json}`)
	result, err := e.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Edit.Execute() error = nil, want non-nil")
	}

	// Verify result is empty.
	if result != "" {
		t.Fatalf("Edit.Execute() result = %q, want empty", result)
	}
}
