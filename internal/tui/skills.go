package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/skills"
)

// skillsStatus renders the /skills status block: the on-demand skills discovered
// under the active agent's skills/ directory (name + summary), or a hint when
// none exist. It mirrors what the agent sees via the skill tool, which loads a
// skill's full body on demand.
func (m *model) skillsStatus() string {
	dir := filepath.Join(m.activeCwd(), skills.DirName)
	timer := debug.NewTimer(dbgTUI, "skills status")
	list := skills.List(dir)
	timer.Stop("dir", dir, "count", len(list))
	if len(list) == 0 {
		debug.Info(dbgTUI, "skills status shown", "dir", dir, "count", 0)
		return styleHint.Render("no skills found under " + dir + " — add one as skills/<name>/SKILL.md (YAML name + description, then Markdown instructions)")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", styleReview.Render(fmt.Sprintf("◎ skills (%d)", len(list))))
	for _, s := range list {
		fmt.Fprintf(&b, "%s %s\n", stylePrompt.Render("§ "+s.Name), styleHint.Render(truncCells(oneLine(s.Summary), 160)))
	}
	fmt.Fprintf(&b, "%s", styleHint.Render("the agent loads a skill's full instructions on demand via the skill tool"))
	debug.Info(dbgTUI, "skills status shown", "dir", dir, "count", len(list))
	return b.String()
}
