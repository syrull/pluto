package mcp

import (
	"strings"
	"testing"
)

func TestInstallDirectiveMentionsRepoPathAndFormat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("PLUTO_MCP_CONFIG", "")

	repo := "https://github.com/owner/cool-mcp"
	d := InstallDirective(repo)
	for _, want := range []string{
		repo,
		DefaultConfigPath(),
		"mcpServers",
		"prerequisite",
		"restart pluto",
		"stdio",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("directive missing %q", want)
		}
	}
}

func TestConfigExampleValidMarkers(t *testing.T) {
	ex := ConfigExample()
	if !strings.Contains(ex, "mcpServers") || !strings.Contains(ex, "command") || !strings.Contains(ex, "url") {
		t.Fatalf("config example missing expected keys:\n%s", ex)
	}
}
