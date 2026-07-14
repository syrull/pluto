package tui

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// writePNG writes a minimal valid PNG to dir and returns its path.
func writePNG(t *testing.T, dir string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write png: %v", err)
	}
	return path
}

func TestLoadImageAttachmentValidPNG(t *testing.T) {
	path := writePNG(t, t.TempDir())
	att, err := loadImageAttachment(path)
	if err != nil {
		t.Fatalf("loadImageAttachment: %v", err)
	}
	if att.Kind != llm.AttachmentImage || att.MediaType != "image/png" {
		t.Fatalf("attachment = %+v, want image/png", att)
	}
	if att.Name != "shot.png" || len(att.Data) == 0 {
		t.Fatalf("attachment name/data not set: %+v", att)
	}
}

func TestLoadImageAttachmentRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("just some text"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if _, err := loadImageAttachment(txt); err == nil {
		t.Fatal("a text file should be rejected as an unsupported image")
	}
}

func TestLoadImageAttachmentMissingFile(t *testing.T) {
	if _, err := loadImageAttachment(filepath.Join(t.TempDir(), "nope.png")); err == nil {
		t.Fatal("a missing file should error")
	}
}

func TestImageCommandStagesAttachment(t *testing.T) {
	path := writePNG(t, t.TempDir())
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""), md: newRenderer(80), width: 80}

	status, cmd := m.handleCommand("/image " + path)
	if status != "" || cmd != nil {
		t.Fatalf("/image should stage quietly, got status %q cmd %v", status, cmd)
	}
	if len(m.attachments) != 1 || m.attachments[0].MediaType != "image/png" {
		t.Fatalf("attachment not staged: %+v", m.attachments)
	}
	if !strings.Contains(m.notice, "attached") {
		t.Fatalf("notice = %q, want it to confirm the attachment", m.notice)
	}
}

func TestImageCommandBadPathReports(t *testing.T) {
	m := &model{agent: agent.New(llm.Stub{}, tool.NewRegistry(), ""), md: newRenderer(80), width: 80}
	status, _ := m.handleCommand("/image /does/not/exist.png")
	if !strings.Contains(status, "✗") {
		t.Fatalf("bad /image path should report an error, got %q", status)
	}
	if len(m.attachments) != 0 {
		t.Fatalf("nothing should be staged on error, got %+v", m.attachments)
	}
}

func TestSubmitSendsAndClearsAttachments(t *testing.T) {
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var tm tea.Model = model{
		agent: ag, md: newRenderer(80), input: newInput(80),
		attachments: []llm.Attachment{{Kind: llm.AttachmentImage, MediaType: "image/png", Data: []byte{1}, Name: "shot.png"}},
	}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	for _, r := range "describe this" {
		tm, _ = tm.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := tm.(model)

	if len(got.attachments) != 0 {
		t.Fatalf("attachments should be cleared after send, got %+v", got.attachments)
	}
	if !got.busy {
		t.Fatal("submitting a message should start a run")
	}
	if joined := got.transcript(); !strings.Contains(joined, "shot.png") {
		t.Fatalf("transcript should show the attachment chip, got:\n%s", joined)
	}
}

func TestImageCommandDoesNotConsumeStaged(t *testing.T) {
	path := writePNG(t, t.TempDir())
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var tm tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80)}
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	for _, r := range "/image " + path {
		tm, _ = tm.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	tm, _ = tm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got := tm.(model)

	if len(got.attachments) != 1 {
		t.Fatalf("/image should leave the image staged, got %+v", got.attachments)
	}
	if got.busy {
		t.Fatal("/image must not start a run")
	}
}

func TestAttachmentChip(t *testing.T) {
	if got := attachmentChip(nil); got != "" {
		t.Fatalf("no attachments = %q, want empty", got)
	}
	one := []llm.Attachment{{Name: "a.png"}}
	if got := attachmentChip(one); !strings.Contains(got, "a.png") {
		t.Fatalf("single chip = %q, want the filename", got)
	}
	two := []llm.Attachment{{Name: "a.png"}, {Name: "b.png"}}
	if got := attachmentChip(two); !strings.Contains(got, "2") {
		t.Fatalf("multi chip = %q, want the count", got)
	}
}
