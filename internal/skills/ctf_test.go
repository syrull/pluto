package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCTFSkillsHiddenByDefault(t *testing.T) {
	SetCTFMode(false)
	dir := t.TempDir()
	if got := List(dir); got != nil {
		t.Fatalf("no skills expected with CTF off and empty dir, got %v", got)
	}
	if _, err := Load(dir, "cred-spray"); err == nil {
		t.Fatal("cred-spray should not load with CTF mode off")
	}
}

func TestCTFSkillsAppearWhenEnabled(t *testing.T) {
	SetCTFMode(true)
	defer SetCTFMode(false)

	dir := t.TempDir()
	list := List(dir)
	names := make(map[string]bool)
	for _, s := range list {
		names[s.Name] = true
	}
	for _, want := range []string{"recon-fanout", "web-fingerprint", "jwt-attacks", "k8s-kubelet-exec", "cred-spray"} {
		if !names[want] {
			t.Fatalf("CTF skill %q missing from list %v", want, names)
		}
	}
	body, err := Load(dir, "cred-spray")
	if err != nil {
		t.Fatalf("Load(cred-spray) failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(body), "spray") {
		t.Fatalf("cred-spray body missing expected content:\n%s", body)
	}
	if strings.Contains(body, "---\nname:") {
		t.Fatal("loaded body should have frontmatter stripped")
	}
}

func TestOnDiskSkillOverridesEmbedded(t *testing.T) {
	SetCTFMode(true)
	defer SetCTFMode(false)

	dir := t.TempDir()
	writeSkill(t, dir, "cred-spray", "custom override recipe")

	body, err := Load(dir, "cred-spray")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !strings.Contains(body, "custom override recipe") {
		t.Fatalf("on-disk skill should win over embedded, got:\n%s", body)
	}
	// The list should carry only one cred-spray entry (the on-disk one).
	count := 0
	for _, s := range List(dir) {
		if s.Name == "cred-spray" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected a single cred-spray entry, got %d", count)
	}
}

func writeSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	sub := filepath.Join(dir, name)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: on-disk test skill for " + name + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(sub, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
