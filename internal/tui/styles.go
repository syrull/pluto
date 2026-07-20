package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/syrull/pluto/internal/debug"
)

// palette is the set of semantic color roles the TUI renders from. A theme is
// one palette; buildStyles assembles every style var from it so a mode can swap
// the whole look (e.g. the red-forward CTF theme) by choosing a different
// palette rather than editing styles one by one.
type palette struct {
	user       color.Color // prompt / user accent
	model      color.Color // assistant text
	tool       color.Color // tool calls
	danger     color.Color // errors and deletions (kept semantic red)
	muted      color.Color // hints / dim text
	prompt     color.Color // '/' prompt and its magenta accents
	working    color.Color // busy / spinner state
	done       color.Color // finished, unread
	review     color.Color // auto-mode review / warnings
	accent     color.Color // borders, selection, status model
	success    color.Color // git / additions
	learn      color.Color // learn-mode badge
	think      color.Color // thinking header/box
	cwd        color.Color // working-directory status
	white      color.Color // tree files
	onAccent   color.Color // foreground on a colored background
	diffAdd    color.Color // diff additions
	diffDel    color.Color // diff deletions
	ctf        color.Color // CTF mode badge
	planet     color.Color // home banner body
	planetMoon color.Color // home banner orbiting dot
}

// defaultPalette is the standard code-development look, reproducing the original
// hardcoded lipgloss colors exactly.
var defaultPalette = palette{
	user:       lipgloss.Color("6"),
	model:      lipgloss.Color("2"),
	tool:       lipgloss.Color("4"),
	danger:     lipgloss.Color("1"),
	muted:      lipgloss.Color("8"),
	prompt:     lipgloss.Color("5"),
	working:    lipgloss.Color("3"),
	done:       lipgloss.Color("2"),
	review:     lipgloss.Color("3"),
	accent:     lipgloss.Color("6"),
	success:    lipgloss.Color("2"),
	learn:      lipgloss.Color("10"),
	think:      lipgloss.Color("13"),
	cwd:        lipgloss.Color("5"),
	white:      lipgloss.Color("7"),
	onAccent:   lipgloss.Color("0"),
	diffAdd:    lipgloss.Color("2"),
	diffDel:    lipgloss.Color("1"),
	ctf:        lipgloss.Color("9"),
	planet:     lipgloss.Color("#F4F2EE"),
	planetMoon: lipgloss.Color("#8A6DF0"),
}

// ctfPalette is the red-forward CTF engagement look: prompt, accents, borders,
// and the working/spinner state go red, while readability roles (assistant text,
// diffs, git, learn) and the semantic error red stay put.
var ctfPalette = func() palette {
	p := defaultPalette
	red := lipgloss.Color("9")
	p.user = red
	p.prompt = red
	p.accent = red
	p.working = red
	p.think = red
	return p
}()

// themeName identifies the active palette.
type themeName string

const (
	themeDefault themeName = "default"
	themeCTF     themeName = "ctf"
)

// activeTheme is the currently applied palette name. Reads and writes happen on
// the single Bubbletea UI goroutine (construction and rendering), so no lock is
// needed.
var activeTheme = themeDefault

var (
	styleUser   lipgloss.Style
	styleModel  lipgloss.Style
	styleTool   lipgloss.Style
	styleErr    lipgloss.Style
	styleHint   lipgloss.Style
	stylePrompt lipgloss.Style
	// Inline-bash input mode: a red `$` prompt and red text while the buffer
	// starts with `!`, signalling the line will run as a shell command.
	styleBashPrompt lipgloss.Style
	styleBashInput  lipgloss.Style
	styleWorking    lipgloss.Style
	styleDone       lipgloss.Style // agent finished, unread
	styleThink      lipgloss.Style
	styleThinkHdr   lipgloss.Style
	styleThinkBox   lipgloss.Style
	stylePickSel    lipgloss.Style
	styleDiffAdd    lipgloss.Style
	styleDiffDel    lipgloss.Style
	styleDiffCtx    lipgloss.Style
	styleDiffHdr    lipgloss.Style
	// Intra-line word highlights: the changed span reversed onto the add/del color.
	styleDiffAddHi lipgloss.Style
	styleDiffDelHi lipgloss.Style

	styleToolName   lipgloss.Style
	styleToolArgs   lipgloss.Style
	styleToolBody   lipgloss.Style
	styleToolResult lipgloss.Style
	styleReview     lipgloss.Style // auto-mode review line

	styleShowBtn    lipgloss.Style
	styleCopyBtn    lipgloss.Style
	styleCloseBtn   lipgloss.Style
	styleErrBtn     lipgloss.Style
	styleAddBtn     lipgloss.Style
	styleBashBox    lipgloss.Style
	styleModalBox   lipgloss.Style
	styleModalTitle lipgloss.Style
	// Slash-command autocomplete popup, anchored above the input.
	styleCmdMenuBox lipgloss.Style

	styleTreeBox         lipgloss.Style
	styleTreeBoxFocus    lipgloss.Style
	styleTreeBorder      lipgloss.Style
	styleTreeBorderFocus lipgloss.Style
	styleTreeCursor      lipgloss.Style
	styleTreeDir         lipgloss.Style
	styleTreeFile        lipgloss.Style

	// Status line: one readable color per segment, with the active model bold so it stands out.
	styleStatusModel   lipgloss.Style
	styleStatusThink   lipgloss.Style
	styleStatusCtx     lipgloss.Style
	styleStatusMouse   lipgloss.Style
	styleStatusLearn   lipgloss.Style
	styleStatusGoal    lipgloss.Style
	styleStatusGit     lipgloss.Style
	styleStatusCwd     lipgloss.Style
	styleStatusAttach  lipgloss.Style
	styleStatusContext lipgloss.Style
	styleStatusCTF     lipgloss.Style // persistent CTF mode badge

	// Planet banner: the off-white body and purple orbiting dot from the logo.
	stylePlanet     lipgloss.Style
	stylePlanetMoon lipgloss.Style
)

// buildStyles assembles every style var from palette p.
func buildStyles(p palette) {
	styleUser = lipgloss.NewStyle().Foreground(p.user).Bold(true)
	styleModel = lipgloss.NewStyle().Foreground(p.model)
	styleTool = lipgloss.NewStyle().Foreground(p.tool)
	styleErr = lipgloss.NewStyle().Foreground(p.danger).Bold(true)
	styleHint = lipgloss.NewStyle().Foreground(p.muted)
	stylePrompt = lipgloss.NewStyle().Foreground(p.prompt).Bold(true)
	styleBashPrompt = lipgloss.NewStyle().Foreground(p.danger).Bold(true)
	styleBashInput = lipgloss.NewStyle().Foreground(p.danger)
	styleWorking = lipgloss.NewStyle().Foreground(p.working).Bold(true)
	styleDone = lipgloss.NewStyle().Foreground(p.done).Bold(true)
	styleThink = lipgloss.NewStyle().Foreground(p.muted).Italic(true)
	styleThinkHdr = lipgloss.NewStyle().Foreground(p.think).Bold(true)
	styleThinkBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.think).
		Padding(0, 1)
	stylePickSel = lipgloss.NewStyle().Foreground(p.onAccent).Background(p.accent).Bold(true)
	styleDiffAdd = lipgloss.NewStyle().Foreground(p.diffAdd)
	styleDiffDel = lipgloss.NewStyle().Foreground(p.diffDel)
	styleDiffCtx = lipgloss.NewStyle().Foreground(p.muted)
	styleDiffHdr = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	styleDiffAddHi = lipgloss.NewStyle().Foreground(p.onAccent).Background(p.diffAdd)
	styleDiffDelHi = lipgloss.NewStyle().Foreground(p.onAccent).Background(p.diffDel)

	styleToolName = lipgloss.NewStyle().Foreground(p.tool).Bold(true)
	styleToolArgs = lipgloss.NewStyle().Foreground(p.accent)
	styleToolBody = lipgloss.NewStyle().Foreground(p.muted)
	styleToolResult = lipgloss.NewStyle().Foreground(p.tool)
	styleReview = lipgloss.NewStyle().Foreground(p.review)

	styleShowBtn = lipgloss.NewStyle().Foreground(p.onAccent).Background(p.accent).Bold(true)
	styleCopyBtn = lipgloss.NewStyle().Foreground(p.onAccent).Background(p.prompt).Bold(true)
	styleCloseBtn = lipgloss.NewStyle().Foreground(p.onAccent).Background(p.review).Bold(true)
	styleErrBtn = lipgloss.NewStyle().Foreground(p.onAccent).Background(p.danger).Bold(true)
	styleAddBtn = lipgloss.NewStyle().Foreground(p.onAccent).Background(p.success).Bold(true)
	styleBashBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.tool).
		Padding(0, 1)
	styleModalBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.accent).
		Padding(0, 1)
	styleModalTitle = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	styleCmdMenuBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.prompt)

	styleTreeBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.muted)
	styleTreeBoxFocus = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.accent)
	styleTreeBorder = lipgloss.NewStyle().Foreground(p.muted)
	styleTreeBorderFocus = lipgloss.NewStyle().Foreground(p.accent)
	styleTreeCursor = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	styleTreeDir = lipgloss.NewStyle().Foreground(p.tool).Bold(true)
	styleTreeFile = lipgloss.NewStyle().Foreground(p.white)

	styleStatusModel = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	styleStatusThink = lipgloss.NewStyle().Foreground(p.prompt)
	styleStatusCtx = lipgloss.NewStyle().Foreground(p.review)
	styleStatusMouse = lipgloss.NewStyle().Foreground(p.tool)
	styleStatusLearn = lipgloss.NewStyle().Foreground(p.learn).Bold(true)
	styleStatusGoal = lipgloss.NewStyle().Foreground(p.review).Bold(true)
	styleStatusGit = lipgloss.NewStyle().Foreground(p.success)
	styleStatusCwd = lipgloss.NewStyle().Foreground(p.cwd)
	styleStatusAttach = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	styleStatusContext = lipgloss.NewStyle().Foreground(p.success).Bold(true)
	styleStatusCTF = lipgloss.NewStyle().Foreground(p.onAccent).Background(p.ctf).Bold(true)

	stylePlanet = lipgloss.NewStyle().Foreground(p.planet)
	stylePlanetMoon = lipgloss.NewStyle().Foreground(p.planetMoon).Bold(true)
}

func init() { buildStyles(defaultPalette) }

// paletteFor returns the palette backing a theme.
func paletteFor(name themeName) palette {
	if name == themeCTF {
		return ctfPalette
	}
	return defaultPalette
}

// setTheme swaps the active palette and rebuilds every style var so the next
// render reflects it. It is a no-op when the theme is already active. It must be
// called on the UI goroutine (it mutates the shared style vars).
func setTheme(name themeName) {
	if name != themeCTF {
		name = themeDefault
	}
	if name == activeTheme {
		return
	}
	buildStyles(paletteFor(name))
	debug.Info("ctf", "theme switched", "from", string(activeTheme), "to", string(name))
	activeTheme = name
}
