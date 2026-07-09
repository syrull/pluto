package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFindHappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := "line 1\nmatch_token\nline 3"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	f := Find{}
	args := json.RawMessage(fmt.Sprintf(`{"pattern":"match_token","path":%q}`, tmpDir))
	result, err := f.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Find.Execute() error = %v, want nil", err)
	}

	// Verify result contains the filename, line number 2, and the matched text
	if !strings.Contains(result, "test.txt") {
		t.Fatalf("Find.Execute() result = %q, want to contain filename 'test.txt'", result)
	}
	if !strings.Contains(result, ":2:") {
		t.Fatalf("Find.Execute() result = %q, want to contain line number ':2:'", result)
	}
	if !strings.Contains(result, "match_token") {
		t.Fatalf("Find.Execute() result = %q, want to contain matched text 'match_token'", result)
	}
}

func TestFindGlobFilter(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a .go file with the search token
	goFile := filepath.Join(tmpDir, "code.go")
	if err := os.WriteFile(goFile, []byte("package main\nsearch_token\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Create a .txt file with the same search token
	txtFile := filepath.Join(tmpDir, "readme.txt")
	if err := os.WriteFile(txtFile, []byte("line 1\nsearch_token\n"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	f := Find{}
	// Search with glob "*.go" to match only Go files
	args := json.RawMessage(fmt.Sprintf(`{"pattern":"search_token","path":%q,"glob":"*.go"}`, tmpDir))
	result, err := f.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Find.Execute() error = %v, want nil", err)
	}

	// Verify .go file is in the result
	if !strings.Contains(result, "code.go") {
		t.Fatalf("Find.Execute() result = %q, want to contain 'code.go'", result)
	}

	// Verify .txt file is NOT in the result
	if strings.Contains(result, "readme.txt") {
		t.Fatalf("Find.Execute() result = %q, must not contain 'readme.txt'", result)
	}
}

func TestFindNoMatches(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(tmpFile, []byte("no matches here"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	f := Find{}
	args := json.RawMessage(fmt.Sprintf(`{"pattern":"neverfound123xyz","path":%q}`, tmpDir))
	result, err := f.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Find.Execute() error = %v, want nil", err)
	}
	if result != "no matches" {
		t.Fatalf("Find.Execute() result = %q, want %q", result, "no matches")
	}
}

func TestFindEmptyPatternError(t *testing.T) {
	tmpDir := t.TempDir()
	f := Find{}
	args := json.RawMessage(fmt.Sprintf(`{"pattern":"","path":%q}`, tmpDir))
	result, err := f.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Find.Execute() error = nil, want non-nil; result = %q", result)
	}
}

func TestFindMissingPatternError(t *testing.T) {
	f := Find{}
	// Empty JSON object has no pattern field
	args := json.RawMessage(`{}`)
	result, err := f.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Find.Execute() error = nil, want non-nil; result = %q", result)
	}
}

func TestFindInvalidJSONError(t *testing.T) {
	f := Find{}
	args := json.RawMessage(`{invalid json}`)
	result, err := f.Execute(context.Background(), args)
	if err == nil {
		t.Fatalf("Find.Execute() error = nil, want non-nil; result = %q", result)
	}
}

func TestFindTruncation(t *testing.T) {
	tmpDir := t.TempDir()

	// Create 5 files, each with 50 matching lines (ripgrep caps at 50 per file).
	// This will produce >200 lines of ripgrep output total.
	for fileIdx := range 5 {
		tmpFile := filepath.Join(tmpDir, fmt.Sprintf("large%d.txt", fileIdx))
		var content strings.Builder
		for lineIdx := range 50 {
			content.WriteString(fmt.Sprintf("line %d with search_token\n", lineIdx+1))
		}
		if err := os.WriteFile(tmpFile, []byte(content.String()), 0644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	f := Find{}
	args := json.RawMessage(fmt.Sprintf(`{"pattern":"search_token","path":%q}`, tmpDir))
	result, err := f.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Find.Execute() error = %v, want nil", err)
	}

	// Verify the result contains the truncation notice
	if !strings.Contains(result, "output truncated") {
		t.Fatalf("Find.Execute() result must contain 'output truncated' when truncating, but got: %q", result)
	}

	// Verify the result is bounded (max 32KB)
	if len(result) > 32*1024 {
		t.Fatalf("Find.Execute() result length = %d bytes, want <= %d", len(result), 32*1024)
	}

	// Verify the result has at most ~201 lines (200 matches + truncation line)
	lineCount := strings.Count(result, "\n")
	if lineCount > 202 {
		t.Fatalf("Find.Execute() line count = %d, want <= ~201 (200 matches + truncation notice)", lineCount)
	}
}

func TestFindLargeCorpusBounded(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a genuinely large corpus: 200 files, each with 50 matching lines.
	// This produces ~10,000 lines of potential ripgrep output (well beyond
	// the 200-line and 32KB limits), ensuring the tool must use bounded reads.
	for fileIdx := range 200 {
		tmpFile := filepath.Join(tmpDir, fmt.Sprintf("large%d.txt", fileIdx))
		var content strings.Builder
		for lineIdx := range 50 {
			content.WriteString(fmt.Sprintf("line %d with search_token\n", lineIdx+1))
		}
		if err := os.WriteFile(tmpFile, []byte(content.String()), 0644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	// Run Find with a timeout context to prove it returns before the deadline
	// even though the corpus contains many MB of potential output.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	f := Find{}
	args := json.RawMessage(fmt.Sprintf(`{"pattern":"search_token","path":%q}`, tmpDir))
	result, err := f.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Find.Execute() error = %v, want nil", err)
	}

	// Verify the result is bounded: must include truncation notice.
	if !strings.Contains(result, "output truncated") {
		t.Fatalf("Find.Execute() result must contain 'output truncated' when corpus exceeds limits, but got: %q", result)
	}

	// Verify the result respects the 32KB size limit.
	if len(result) > 32*1024 {
		t.Fatalf("Find.Execute() result length = %d bytes, want <= %d", len(result), 32*1024)
	}

	// Verify the result respects the ~200-line limit (200 matches + 1-2 truncation lines).
	lineCount := strings.Count(result, "\n")
	if lineCount > 202 {
		t.Fatalf("Find.Execute() line count = %d, want <= 202 (200 matches + truncation notices)", lineCount)
	}
}
