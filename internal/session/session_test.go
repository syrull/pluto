package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syrull/pluto/internal/llm"
)

func sampleMessages() []llm.Message {
	return []llm.Message{
		{Role: llm.RoleSystem, Content: "you are a test agent"},
		{Role: llm.RoleUser, Content: "read the file"},
		{
			Role:        llm.RoleModel,
			Content:     "on it",
			Thinking:    "the user wants a read",
			ThinkingSig: "sig-abc",
			ToolCalls:   []llm.ToolCall{{ID: "call-1", Name: "read", Args: json.RawMessage(`{"path":"main.go"}`)}},
		},
		{Role: llm.RoleTool, ToolName: "read", ToolCallID: "call-1", Content: "package main"},
		{Role: llm.RoleModel, Content: "done"},
	}
}

func TestSaveResumePreservesAttachments(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	store, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	data := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a}
	in := &Session{ID: "with-image", Title: "look", Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "look", Attachments: []llm.Attachment{
			{Kind: llm.AttachmentImage, MediaType: "image/png", Data: data, Name: "shot.png"},
		}},
	}}
	if err := store.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load("with-image")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	atts := got.Messages[0].Attachments
	if len(atts) != 1 {
		t.Fatalf("attachments = %+v, want one", atts)
	}
	a := atts[0]
	if a.Kind != llm.AttachmentImage || a.MediaType != "image/png" || a.Name != "shot.png" {
		t.Fatalf("attachment metadata not preserved: %+v", a)
	}
	if string(a.Data) != string(data) {
		t.Fatalf("attachment bytes not preserved: got %v want %v", a.Data, data)
	}
}

func TestSaveResumeRoundTrip(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	store, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	in := &Session{ID: "my-session", Title: "read the file", Model: "anthropic/test", Messages: sampleMessages()}
	if err := store.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load("my-session")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != formatVersion {
		t.Fatalf("Version = %d, want %d", got.Version, formatVersion)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not stamped: created=%v updated=%v", got.CreatedAt, got.UpdatedAt)
	}
	if len(got.Messages) != len(in.Messages) {
		t.Fatalf("message count = %d, want %d", len(got.Messages), len(in.Messages))
	}
	// Tool calls and thinking blocks must survive verbatim for replay.
	model := got.Messages[2]
	if model.Thinking != "the user wants a read" || model.ThinkingSig != "sig-abc" {
		t.Fatalf("thinking block not preserved: %+v", model)
	}
	if len(model.ToolCalls) != 1 || model.ToolCalls[0].ID != "call-1" || model.ToolCalls[0].Name != "read" {
		t.Fatalf("tool call not preserved: %+v", model.ToolCalls)
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(model.ToolCalls[0].Args, &args); err != nil || args.Path != "main.go" {
		t.Fatalf("tool call args not preserved (err=%v): %s", err, model.ToolCalls[0].Args)
	}
	if tr := got.Messages[3]; tr.Role != llm.RoleTool || tr.ToolCallID != "call-1" || tr.Content != "package main" {
		t.Fatalf("tool result not preserved: %+v", tr)
	}
}

func TestSaveIsAtomicAndResavePreservesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLUTO_SESSIONS_DIR", dir)
	store, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	s := &Session{ID: "s", Messages: sampleMessages()}
	if err := store.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	created := s.CreatedAt

	// No leftover temp files after an atomic write.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}

	resave := &Session{ID: "s", Messages: sampleMessages()}
	if prev, err := store.Load("s"); err == nil {
		resave.CreatedAt = prev.CreatedAt
	}
	if err := store.Save(resave); err != nil {
		t.Fatalf("resave: %v", err)
	}
	if !resave.CreatedAt.Equal(created) {
		t.Fatalf("resave CreatedAt = %v, want preserved %v", resave.CreatedAt, created)
	}
}

func TestLoadMissingReturnsNotFound(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	store, _ := Open()
	if _, err := store.Load("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load(missing) err = %v, want ErrNotFound", err)
	}
}

func TestLoadCorruptAndForeignFilesError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLUTO_SESSIONS_DIR", dir)
	store, _ := Open()

	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("bad"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Load(corrupt) err = %v, want a descriptive error", err)
	}

	// Valid JSON but no version: a foreign file, not a session.
	if err := os.WriteFile(filepath.Join(dir, "foreign.json"), []byte(`{"hello":"world"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("foreign"); err == nil {
		t.Fatalf("Load(foreign) err = nil, want unsupported-version error")
	}
}

func TestListSkipsUnreadableAndSortsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PLUTO_SESSIONS_DIR", dir)
	store, _ := Open()

	if err := store.Save(&Session{ID: "older", Messages: sampleMessages()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(&Session{ID: "newer", Messages: sampleMessages()}); err != nil {
		t.Fatal(err)
	}
	// Foreign and non-session files must be ignored by List.
	_ = os.WriteFile(filepath.Join(dir, "junk.json"), []byte("{bad"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o600)

	metas, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("List returned %d sessions, want 2: %+v", len(metas), metas)
	}
	if metas[0].ID != "newer" || metas[1].ID != "older" {
		t.Fatalf("List order = [%s %s], want newest first [newer older]", metas[0].ID, metas[1].ID)
	}
	if metas[0].Count != len(sampleMessages()) {
		t.Fatalf("Meta.Count = %d, want %d", metas[0].Count, len(sampleMessages()))
	}
}

func TestListForCwdScopesByPath(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	store, _ := Open()

	if err := store.Save(&Session{ID: "here", Cwd: "/work/here", Messages: sampleMessages()}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(&Session{ID: "there", Cwd: "/work/there", Messages: sampleMessages()}); err != nil {
		t.Fatal(err)
	}
	// A legacy session with no recorded path must never be hidden.
	if err := store.Save(&Session{ID: "legacy", Messages: sampleMessages()}); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListForCwd("/work/here/")
	if err != nil {
		t.Fatalf("ListForCwd: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	if !ids["here"] || !ids["legacy"] {
		t.Fatalf("ListForCwd(/work/here) should include the folder's session and legacy, got %v", ids)
	}
	if ids["there"] {
		t.Fatalf("ListForCwd(/work/here) must not include another folder's session, got %v", ids)
	}

	if all, _ := store.ListForCwd(""); len(all) != 3 {
		t.Fatalf("ListForCwd(\"\") should return every session, got %d", len(all))
	}
}

func TestListMissingDirIsEmpty(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
	store := &Store{dir: Dir()}
	metas, err := store.List()
	if err != nil {
		t.Fatalf("List(missing dir) err = %v, want nil", err)
	}
	if len(metas) != 0 {
		t.Fatalf("List(missing dir) = %d, want 0", len(metas))
	}
}

func TestSanitizePreventsTraversalAndUnsafeChars(t *testing.T) {
	cases := map[string]string{
		"my session":       "my-session",
		"../../etc/passwd": "passwd",
		"a/b/c":            "c",
		"weird!@#name":     "weird-name",
		"  spaced  ":       "spaced",
		"...":              "",
		"/":                "",
		"..":               "",
		"keep_this.name-1": "keep_this.name-1",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSaveRejectsEmptyID(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", t.TempDir())
	store, _ := Open()
	if err := store.Save(&Session{ID: "...", Messages: sampleMessages()}); err == nil {
		t.Fatal("Save with an id that sanitizes to empty should error")
	}
}

func TestNewIDAndTitleFromMessages(t *testing.T) {
	title := TitleFromMessages(sampleMessages())
	if title != "read the file" {
		t.Fatalf("TitleFromMessages = %q, want %q", title, "read the file")
	}
	if got := TitleFromMessages(nil); got != "untitled" {
		t.Fatalf("TitleFromMessages(nil) = %q, want untitled", got)
	}

	id := NewID("Fix the bug!")
	if strings.ContainsAny(id, " /\\") {
		t.Fatalf("NewID produced an unsafe id: %q", id)
	}
	if !strings.HasSuffix(id, "-fix-the-bug") {
		t.Fatalf("NewID = %q, want a slug suffix -fix-the-bug", id)
	}
	if Sanitize(id) != id {
		t.Fatalf("NewID id %q is not already sanitized", id)
	}
}

func TestDirDefaultAndOverride(t *testing.T) {
	t.Setenv("PLUTO_SESSIONS_DIR", "/tmp/custom-sessions")
	if got := Dir(); got != "/tmp/custom-sessions" {
		t.Fatalf("Dir() with override = %q, want /tmp/custom-sessions", got)
	}
	t.Setenv("PLUTO_SESSIONS_DIR", "")
	if got := Dir(); !strings.HasSuffix(got, filepath.Join(".pluto", "sessions")) {
		t.Fatalf("Dir() default = %q, want a ~/.pluto/sessions path", got)
	}
}
