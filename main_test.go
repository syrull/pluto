package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
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

// TestSystemPromptBaseHasNoToolGuidance guards issue #73: per-tool guidance is
// re-sent every turn, so it must live only in a tool's Description, never be
// duplicated in systemPromptBase. Each phrase below is carried by a tool
// description and must be absent from the base prompt.
func TestSystemPromptBaseHasNoToolGuidance(t *testing.T) {
	reg := tool.NewRegistry()
	reg.MustRegister(tools.Read{})
	reg.MustRegister(tools.Write{})
	reg.MustRegister(tools.Bash{})
	reg.MustRegister(tools.Edit{})
	reg.MustRegister(tools.Find{})

	var descriptions strings.Builder
	for _, tl := range reg.Tools() {
		descriptions.WriteString(tl.Description())
		descriptions.WriteByte('\n')
	}
	descs := descriptions.String()

	toolPhrases := []string{
		"cat/head/tail",
		"grep/rg/ag",
		"intent",
		"offset",
		"overflow the context window",
	}
	for _, p := range toolPhrases {
		if !strings.Contains(descs, p) {
			t.Errorf("expected a tool Description to carry %q", p)
		}
		if strings.Contains(systemPromptBase, p) {
			t.Errorf("systemPromptBase duplicates per-tool guidance %q; it belongs only in a tool Description", p)
		}
	}

	if !strings.Contains(systemPromptBase, "minimal file-editing agent") {
		t.Errorf("systemPromptBase lost its role framing")
	}
	if !strings.Contains(systemPromptBase, "explore the relevant code first") {
		t.Errorf("systemPromptBase lost the explore-before-acting behavior")
	}
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

// TestBuildSystemPromptIndexesSkills verifies the compact skills index (name +
// summary) rides in the base prompt while a skill's full body is left out — the
// body is loaded on demand via the skill tool, not front-loaded.
func TestBuildSystemPromptIndexesSkills(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	reg := newTestRegistry(t)

	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "SECRET_SKILL_BODY_LINE that must not be front-loaded"
	content := "# Run the test suite\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "run-tests.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	prompt := buildSystemPrompt(reg)

	if !strings.Contains(prompt, "--- Skills (load a full playbook on demand with the skill tool) ---") {
		t.Errorf("buildSystemPrompt() missing skills index header")
	}
	if !strings.Contains(prompt, "- run-tests: Run the test suite") {
		t.Errorf("buildSystemPrompt() missing compact skill entry")
	}
	if strings.Contains(prompt, body) {
		t.Errorf("buildSystemPrompt() front-loaded the skill body; it must be loaded on demand")
	}
}

// TestBuildSystemPromptNoSkillsDir verifies the index section is omitted when no
// skills/ directory exists.
func TestBuildSystemPromptNoSkillsDir(t *testing.T) {
	t.Chdir(t.TempDir())
	reg := newTestRegistry(t)

	if strings.Contains(buildSystemPrompt(reg), "--- Skills") {
		t.Errorf("buildSystemPrompt() emitted skills section with no skills/ dir")
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

// fakeReauther records Reauth calls, standing in for an auxiliary Anthropic
// provider (judge/summarizer) in the /login reauth tests.
type fakeReauther struct {
	name  string
	err   error
	calls int
}

func (f *fakeReauther) Reauth() error { f.calls++; return f.err }
func (f *fakeReauther) Name() string  { return f.name }

// seedStoredToken points HOME at a temp dir holding a valid credentials.json and
// clears the env credentials, so the main provider's reauth (anthropic.New) can
// authenticate deterministically from the store — mimicking a fresh /login.
func seedStoredToken(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	store := filepath.Join(home, ".pluto", "credentials.json")
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{
		"accessToken": "fresh-tok",
		"expiresAt":   time.Now().Add(time.Hour).UnixMilli(),
	})
	if err := os.WriteFile(store, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestReauthProvidersReauthsAuxProviders guards the /login regression: a
// re-login must refresh the judge and summarizer providers, not just the main
// model, or the judge keeps its expired token and the fail-safe policy blocks
// every command.
func TestReauthProvidersReauthsAuxProviders(t *testing.T) {
	seedStoredToken(t)
	ag := agent.New(llm.Stub{}, newTestRegistry(t), "sys")
	judge := &fakeReauther{name: "judge"}
	summarizer := &fakeReauther{name: "summarizer"}

	status, err := reauthProviders(ag, judge, summarizer)
	if err != nil {
		t.Fatalf("reauthProviders() error = %v", err)
	}
	if judge.calls != 1 {
		t.Errorf("judge provider reauthed %d times, want 1", judge.calls)
	}
	if summarizer.calls != 1 {
		t.Errorf("summarizer provider reauthed %d times, want 1", summarizer.calls)
	}
	if !strings.Contains(status, "logged in") {
		t.Errorf("status = %q, want it to report a successful login", status)
	}
}

// TestReauthProvidersContinuesOnAuxFailure verifies a failing auxiliary provider
// does not abort the login or stop the other aux providers from being refreshed,
// and that the failure is surfaced in the status line.
func TestReauthProvidersContinuesOnAuxFailure(t *testing.T) {
	seedStoredToken(t)
	ag := agent.New(llm.Stub{}, newTestRegistry(t), "sys")
	bad := &fakeReauther{name: "judge", err: errors.New("boom")}
	good := &fakeReauther{name: "summarizer"}

	status, err := reauthProviders(ag, bad, good)
	if err != nil {
		t.Fatalf("reauthProviders() error = %v, want nil (main login succeeded)", err)
	}
	if bad.calls != 1 || good.calls != 1 {
		t.Errorf("aux reauth calls: bad=%d good=%d, want both attempted once", bad.calls, good.calls)
	}
	if !strings.Contains(status, "logged in") {
		t.Errorf("status = %q, want it to still report a successful login", status)
	}
	if !strings.Contains(status, "auxiliary") {
		t.Errorf("status = %q, want it to surface the auxiliary reauth failure", status)
	}
}

// TestReauthProvidersFailsWhenMainProviderFails verifies that when the main
// provider cannot authenticate, the login fails and auxiliary providers are not
// touched.
func TestReauthProvidersFailsWhenMainProviderFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	ag := agent.New(llm.Stub{}, newTestRegistry(t), "sys")
	judge := &fakeReauther{name: "judge"}

	if _, err := reauthProviders(ag, judge); err == nil {
		t.Fatal("reauthProviders() expected error when main provider cannot authenticate")
	}
	if judge.calls != 0 {
		t.Errorf("judge provider reauthed %d times, want 0 when main login fails", judge.calls)
	}
}

// TestAuxReauthersFiltersNil verifies nil providers (judge/summarizer that never
// authenticated) are dropped so /login never calls methods on a nil pointer.
func TestAuxReauthersFiltersNil(t *testing.T) {
	if got := auxReauthers(nil, nil); got != nil {
		t.Errorf("auxReauthers(nil, nil) = %v, want nil", got)
	}
}
