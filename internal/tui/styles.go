package tui

import "github.com/charmbracelet/lipgloss"

var (
	styleUser        = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleModel       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleTool        = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleErr         = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleHint        = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stylePrompt      = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	styleModelStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	styleThink       = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	styleThinkHdr    = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	styleThinkBox    = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("13")).
				Padding(0, 1)
	stylePickSel = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Bold(true)
	styleDiffAdd = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleDiffDel = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleDiffCtx = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleDiffHdr = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)

	styleToolName   = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	styleToolArgs   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleToolBody   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleToolResult = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
)
