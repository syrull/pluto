package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteExecuteCreateNewFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "newfile.txt")

	content := "first line\nsecond line\nthird line"
	args, _ := json.Marshal(map[string]string{"path": filePath, "content": content})

	result, err := Write{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Check header has correct byte count and file was created
	// "first line\nsecond line\nthird line" = 33 bytes
	if !strings.Contains(result, "wrote 33 bytes to") {
		t.Fatalf("header missing or incorrect byte count:\n%s", result)
	}

	// Check file was actually written
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("file not created: %v", err)
	}
	if string(fileContent) != content {
		t.Fatalf("file content = %q, want %q", string(fileContent), content)
	}

	// Check result contains diff stats.
	if !strings.Contains(result, "(+3 -0)") {
		t.Fatalf("result missing (+3 -0) stats:\n%s", result)
	}

	// Result must not echo the written content.
	if strings.Contains(result, "first line") {
		t.Fatalf("result should not echo file contents:\n%s", result)
	}
}

func TestWriteExecuteModifyFileWithChangedLine(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "existing.txt")

	// Write initial content
	oldContent := "line one\nline two\nline three"
	if err := os.WriteFile(filePath, []byte(oldContent), 0o644); err != nil {
		t.Fatalf("failed to write initial content: %v", err)
	}

	// Now modify the file (change middle line)
	newContent := "line one\nLINE TWO\nline three"
	args, _ := json.Marshal(map[string]string{"path": filePath, "content": newContent})

	result, err := Write{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Result reports stats but does not echo file contents.
	if strings.Contains(result, "line two") || strings.Contains(result, "LINE TWO") {
		t.Fatalf("result should not echo file contents:\n%s", result)
	}

	// Verify file was updated
	fileContent, _ := os.ReadFile(filePath)
	if string(fileContent) != newContent {
		t.Fatalf("file content not updated: %q", string(fileContent))
	}

	// Verify stats show 1 added and 1 removed
	if !strings.Contains(result, "(+1 -1)") {
		t.Fatalf("result should show (+1 -1) stats:\n%s", result)
	}
}

func TestWriteExecuteIdenticalContentNoChangeMessage(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "unchanged.txt")

	// Write initial content
	content := "content stays the same"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write initial content: %v", err)
	}

	// Overwrite with identical content
	args, _ := json.Marshal(map[string]string{"path": filePath, "content": content})

	result, err := Write{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Should have "(no change)" with no diff body
	if !strings.Contains(result, "(no change)") {
		t.Fatalf("result should contain '(no change)':\n%s", result)
	}

	// Should NOT contain a newline followed by diff content (result should be a single line)
	if strings.Count(result, "\n") > 0 {
		t.Fatalf("result with no change should not have newline/body:\n%q", result)
	}

	// Verify no diff stats in result
	if strings.Contains(result, "(+") || strings.Contains(result, "-)") {
		t.Fatalf("result with no change should not have diff stats:\n%s", result)
	}
}

func TestWriteExecuteLargeFileCap(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "largefile.txt")

	// Build old content: 2001 lines
	var oldBuilder strings.Builder
	for i := range 2001 {
		oldBuilder.WriteString(fmt.Sprintf("line %d\n", i))
	}
	oldContent := oldBuilder.String()

	// Write initial large content
	if err := os.WriteFile(filePath, []byte(oldContent), 0o644); err != nil {
		t.Fatalf("failed to write initial large content: %v", err)
	}

	// Build new content: 2001 different lines
	var newBuilder strings.Builder
	for i := range 2001 {
		newBuilder.WriteString(fmt.Sprintf("LINE %d\n", i))
	}
	newContent := newBuilder.String()

	args, _ := json.Marshal(map[string]string{"path": filePath, "content": newContent})

	result, err := Write{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Result should be header only: "wrote N bytes to PATH" with NO stats, NO body
	lines := strings.Split(result, "\n")
	if len(lines) > 1 && lines[1] != "" {
		t.Fatalf("large file result should be header-only (no body), but got:\n%s", result)
	}

	// Should NOT contain diff stats
	if strings.Contains(result, "(+") || strings.Contains(result, "-)") {
		t.Fatalf("large file result should not have diff stats:\n%s", result)
	}

	// Should contain "wrote" and the path
	if !strings.Contains(result, "wrote") {
		t.Fatalf("result should contain 'wrote':\n%s", result)
	}
}

func TestWriteExecuteLargeFileNewSide(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "large-new.txt")

	// Old content: small
	oldContent := "line 1\nline 2"

	// Write initial small content
	if err := os.WriteFile(filePath, []byte(oldContent), 0o644); err != nil {
		t.Fatalf("failed to write initial content: %v", err)
	}

	// New content: 2001 lines
	var newBuilder strings.Builder
	for i := range 2001 {
		newBuilder.WriteString(fmt.Sprintf("line %d\n", i))
	}
	newContent := newBuilder.String()

	args, _ := json.Marshal(map[string]string{"path": filePath, "content": newContent})

	result, err := Write{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Result should be header-only (no stats, no body)
	lines := strings.Split(result, "\n")
	if len(lines) > 1 && lines[1] != "" {
		t.Fatalf("large file (new side) result should be header-only, but got:\n%s", result)
	}

	if strings.Contains(result, "(+") || strings.Contains(result, "-)") {
		t.Fatalf("large file result should not have diff stats:\n%s", result)
	}
}

func TestWriteExecuteCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "parent", "child", "file.txt")

	content := "nested file"
	args, _ := json.Marshal(map[string]string{"path": filePath, "content": content})

	result, err := Write{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Check parent dirs were created and file exists
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("parent dirs not created or file not written: %v", err)
	}
	if string(fileContent) != content {
		t.Fatalf("file content mismatch: %q", string(fileContent))
	}

	// Should still have a diff result
	if !strings.Contains(result, "wrote") {
		t.Fatalf("result should be valid:\n%s", result)
	}
}

func TestWriteExecuteHandlesMissingPath(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"path": "", "content": "test"})

	_, err := Write{}.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected error about missing path, got: %v", err)
	}
}

func TestWriteExecuteByteCountAccuracy(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "bytecount.txt")

	// UTF-8 content with multi-byte chars (emoji, accents, etc.)
	content := "hello\nwörld\n😀"
	expectedBytes := len([]byte(content))

	args, _ := json.Marshal(map[string]string{"path": filePath, "content": content})

	result, err := Write{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Extract byte count from result header
	byteCountStr := fmt.Sprintf("wrote %d bytes", expectedBytes)
	if !strings.Contains(result, byteCountStr) {
		t.Fatalf("result should contain 'wrote %d bytes':\n%s", expectedBytes, result)
	}
}
