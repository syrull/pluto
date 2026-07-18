package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/mcp"
)

// dbgMCP tags the /install-mcp command flow in the debug log.
const dbgMCP = "mcp"

// handleInstallMCP dispatches /install-mcp <repo>: it validates the repository
// reference, then hands the agent a detailed install directive (explore the
// repo, verify prerequisites, merge an entry into mcp.json, or walk the user
// through anything ambiguous) and starts a turn to carry it out.
func (m *model) handleInstallMCP(line string) (string, tea.Cmd) {
	fields := strings.Fields(line)
	repo := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
	if repo == "" {
		debug.Debug(dbgMCP, "install rejected", "reason", "no repo")
		return styleErr.Render("✗ usage: /install-mcp <github repo url or owner/repo>"), nil
	}
	if !looksLikeRepo(repo) {
		debug.Debug(dbgMCP, "install rejected", "reason", "bad repo", "repo", repo)
		return styleErr.Render("✗ /install-mcp expects a GitHub repository, e.g. /install-mcp https://github.com/owner/repo or owner/repo"), nil
	}
	directive := mcp.InstallDirective(repo)
	debug.Info(dbgMCP, "install started", "repo", repo, "config", mcp.DefaultConfigPath())
	m.showHome = false
	m.busy = true
	m.notice = "→ installing MCP server — exploring the repo and its prerequisites"
	return m.renderInstallMCP(repo), m.runAgent(directive, nil)
}

// renderInstallMCP is the transcript banner shown when an install begins.
func (m *model) renderInstallMCP(repo string) string {
	return styleReview.Render("→ install-mcp — "+truncCells(oneLine(repo), 200)) + "\n" +
		styleHint.Render("Exploring the repository, checking prerequisites, and updating "+mcp.DefaultConfigPath()+". Restart pluto afterwards to load the server. esc stops.")
}

// looksLikeRepo accepts a github URL or an owner/repo shorthand and rejects
// obvious non-references (whitespace, no slash) so a typo doesn't silently kick
// off a turn.
func looksLikeRepo(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return false
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "git@") {
		return true
	}
	// owner/repo shorthand: exactly one slash, non-empty halves.
	owner, repo, ok := strings.Cut(s, "/")
	return ok && owner != "" && repo != "" && !strings.Contains(repo, "/")
}
