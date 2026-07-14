package main

import (
	"os"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/tool"
	"github.com/syrull/pluto/internal/tools"
)

// newTestRegistry creates a fresh registry with Read and Find tools for testing.
func newTestRegistry(t *testing.T) *tool.Registry {
	t.Helper()
	reg := tool.NewRegistry()
	reg.MustRegister(tools.Read{})
	reg.MustRegister(tools.Find{})
	return reg
}

// TestBuildSystemPromptNoContextFiles verifies output when no context files
// are present: starts with systemPromptBase, contains "Available tools:",
// and contains no "Project context from" substring.
func TestBuildSystemPromptNoContextFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	reg := newTestRegistry(t)

	prompt := buildSystemPrompt(reg)

	// Must start with systemPromptBase.
	if !strings.HasPrefix(prompt, systemPromptBase) {
		t.Errorf("buildSystemPrompt() does not start with systemPromptBase")
	}

	// Must contain "Available tools:".
	if !strings.Contains(prompt, "Available tools:") {
		t.Errorf("buildSystemPrompt() does not contain \"Available tools:\"")
	}

	// Must NOT contain "Project context from" since no files exist.
	if strings.Contains(prompt, "Project context from") {
		t.Errorf("buildSystemPrompt() contains \"Project context from\" when no context files exist")
	}

	// Sanity check: tools are listed.
	if !strings.Contains(prompt, "- find:") {
		t.Errorf("buildSystemPrompt() does not contain \"- find:\" tool listing")
	}
	if !strings.Contains(prompt, "- read:") {
		t.Errorf("buildSystemPrompt() does not contain \"- read:\" tool listing")
	}
}

// TestBuildSystemPromptOnlyClaudeFile verifies that when only CLAUDE.md
// is present, its content is injected with the correct header and no AGENTS.md
// header appears.
func TestBuildSystemPromptOnlyClaudeFile(t *testing.T) {
	t.Chdir(t.TempDir())
	reg := newTestRegistry(t)

	claudeContent := "hello claude"
	if err := os.WriteFile("CLAUDE.md", []byte(claudeContent), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	prompt := buildSystemPrompt(reg)

	// Must contain CLAUDE.md header.
	if !strings.Contains(prompt, "--- Project context from CLAUDE.md ---") {
		t.Errorf("buildSystemPrompt() does not contain CLAUDE.md header")
	}

	// Must contain the content.
	if !strings.Contains(prompt, claudeContent) {
		t.Errorf("buildSystemPrompt() does not contain CLAUDE.md content")
	}

	// Must NOT contain AGENTS.md header.
	if strings.Contains(prompt, "--- Project context from AGENTS.md ---") {
		t.Errorf("buildSystemPrompt() contains AGENTS.md header when only CLAUDE.md exists")
	}
}

// TestBuildSystemPromptBothContextFiles verifies that when both CLAUDE.md and
// AGENTS.md are present, both headers appear and CLAUDE.md appears before AGENTS.md.
func TestBuildSystemPromptBothContextFiles(t *testing.T) {
	t.Chdir(t.TempDir())
	reg := newTestRegistry(t)

	claudeContent := "claude instructions"
	agentsContent := "agents instructions"

	if err := os.WriteFile("CLAUDE.md", []byte(claudeContent), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	if err := os.WriteFile("AGENTS.md", []byte(agentsContent), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	prompt := buildSystemPrompt(reg)

	// Both headers must be present.
	if !strings.Contains(prompt, "--- Project context from CLAUDE.md ---") {
		t.Errorf("buildSystemPrompt() does not contain CLAUDE.md header")
	}
	if !strings.Contains(prompt, "--- Project context from AGENTS.md ---") {
		t.Errorf("buildSystemPrompt() does not contain AGENTS.md header")
	}

	// CLAUDE.md must appear before AGENTS.md (string ordering).
	claudeIdx := strings.Index(prompt, "--- Project context from CLAUDE.md ---")
	agentsIdx := strings.Index(prompt, "--- Project context from AGENTS.md ---")
	if claudeIdx >= agentsIdx {
		t.Errorf("CLAUDE.md header (idx %d) does not appear before AGENTS.md header (idx %d)", claudeIdx, agentsIdx)
	}

	// Both contents must be present.
	if !strings.Contains(prompt, claudeContent) {
		t.Errorf("buildSystemPrompt() does not contain CLAUDE.md content")
	}
	if !strings.Contains(prompt, agentsContent) {
		t.Errorf("buildSystemPrompt() does not contain AGENTS.md content")
	}
}

// TestBuildSystemPromptIncludesRepoSnapshot verifies the auto-detected repo
// snapshot is appended after any project context and reflects the project type.
func TestBuildSystemPromptIncludesRepoSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	reg := newTestRegistry(t)

	if err := os.WriteFile("go.mod", []byte("module example.com/demo\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	prompt := buildSystemPrompt(reg)

	if !strings.Contains(prompt, "Repository snapshot") {
		t.Errorf("buildSystemPrompt() does not contain the repo snapshot")
	}
	if !strings.Contains(prompt, "Go module (example.com/demo)") {
		t.Errorf("buildSystemPrompt() snapshot does not report the detected project type")
	}
}

// TestBuildSystemPromptSnapshotDisabled verifies PLUTO_REPO_SCAN=off suppresses
// the snapshot entirely.
func TestBuildSystemPromptSnapshotDisabled(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PLUTO_REPO_SCAN", "off")
	reg := newTestRegistry(t)

	if strings.Contains(buildSystemPrompt(reg), "Repository snapshot") {
		t.Errorf("buildSystemPrompt() included snapshot despite PLUTO_REPO_SCAN=off")
	}
}

// TestBuildSystemPromptEmptyClaudeFileSkipped verifies that when CLAUDE.md
// contains only whitespace, it is skipped and no header is emitted.
func TestBuildSystemPromptEmptyClaudeFileSkipped(t *testing.T) {
	t.Chdir(t.TempDir())
	reg := newTestRegistry(t)

	// Write whitespace-only content.
	if err := os.WriteFile("CLAUDE.md", []byte("   \n\t"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	prompt := buildSystemPrompt(reg)

	// No CLAUDE.md header should appear.
	if strings.Contains(prompt, "--- Project context from CLAUDE.md ---") {
		t.Errorf("buildSystemPrompt() contains CLAUDE.md header for empty/whitespace file")
	}

	// Still contains available tools (sanity check).
	if !strings.Contains(prompt, "Available tools:") {
		t.Errorf("buildSystemPrompt() does not contain \"Available tools:\"")
	}
}

// TestBuildSystemPromptContentTrimmed verifies that file content is trimmed
// of surrounding whitespace. A file with "  padded  \n" should inject "padded"
// with no surrounding blank padding.
func TestBuildSystemPromptContentTrimmed(t *testing.T) {
	t.Chdir(t.TempDir())
	reg := newTestRegistry(t)

	// Write padded content.
	if err := os.WriteFile("CLAUDE.md", []byte("  padded  \n"), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	prompt := buildSystemPrompt(reg)

	// The header and content must be present.
	if !strings.Contains(prompt, "--- Project context from CLAUDE.md ---") {
		t.Errorf("buildSystemPrompt() does not contain CLAUDE.md header")
	}

	// The content "padded" must appear directly after the header with no extra
	// surrounding whitespace. The format is "\n\n--- Project context from CLAUDE.md ---\n<content>"
	// so we should see "---\npadded" (header immediately followed by the trimmed content).
	headerAndContent := "--- Project context from CLAUDE.md ---\npadded"
	if !strings.Contains(prompt, headerAndContent) {
		t.Errorf("buildSystemPrompt() does not contain expected trimmed content pattern.\nLooking for: %q\nGot: %q", headerAndContent, prompt)
	}

	// Verify the padded version does NOT appear (i.e., not "  padded  ").
	if strings.Contains(prompt, "  padded  ") {
		t.Errorf("buildSystemPrompt() contains untrimmmed padded content")
	}
}
