package tui

import "charm.land/lipgloss/v2"

var (
	styleUser   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleModel  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleTool   = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleHint   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stylePrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	// Inline-bash input mode: a red `$` prompt and red text while the buffer
	// starts with `!`, signalling the line will run as a shell command.
	styleBashPrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleBashInput  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleWorking    = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	styleThink      = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	styleThinkHdr   = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	styleThinkBox   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("13")).
			Padding(0, 1)
	stylePickSel = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Bold(true)
	styleDiffAdd = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleDiffDel = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleDiffCtx = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleDiffHdr = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	// Intra-line word highlights: the changed span reversed onto the add/del color.
	styleDiffAddHi = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("2"))
	styleDiffDelHi = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("1"))

	styleToolName   = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	styleToolArgs   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleToolBody   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleToolResult = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleReview     = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow: auto-mode review line

	styleShowBtn  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Bold(true)
	styleCopyBtn  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("5")).Bold(true)
	styleCloseBtn = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3")).Bold(true)
	styleErrBtn   = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("1")).Bold(true)
	styleBashBox  = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("4")).
			Padding(0, 1)
	styleModalBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("6")).
			Padding(0, 1)
	styleModalTitle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	// Slash-command autocomplete popup: a magenta-bordered box tying it to the
	// '/' prompt, anchored above the input.
	styleCmdMenuBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("5"))

	styleTreeBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("8"))
	styleTreeBoxFocus = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("6"))
	styleTreeBorder      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleTreeBorderFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleTreeCursor      = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleTreeDir         = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	styleTreeFile        = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))

	// Status line: one readable color per segment, with the active model bold so it stands out.
	styleStatusModel = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleStatusThink = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleStatusCtx   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleStatusMouse = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleStatusGit   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleStatusCwd   = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))

	// Planet banner: the off-white body and purple orbiting dot from the logo.
	stylePlanet     = lipgloss.NewStyle().Foreground(lipgloss.Color("#F4F2EE"))
	stylePlanetMoon = lipgloss.NewStyle().Foreground(lipgloss.Color("#8A6DF0")).Bold(true)
)
