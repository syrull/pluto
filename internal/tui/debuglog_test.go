package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/agent"
	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

// enableTUILog points the debug logger at a temp file and returns a reader.
func enableTUILog(t *testing.T, level string) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "debug.log")
	t.Setenv("PLUTO_DEBUG", "1")
	t.Setenv("PLUTO_DEBUG_FILE", path)
	t.Setenv("PLUTO_DEBUG_LEVEL", level)
	t.Setenv("PLUTO_DEBUG_COMPONENTS", "")
	t.Setenv("PLUTO_DEBUG_FRAMES", "coalesced")
	_ = debug.Close()
	if _, err := debug.Init(); err != nil {
		t.Fatalf("debug.Init: %v", err)
	}
	t.Cleanup(func() { _ = debug.Close() })
	return func() string {
		_ = debug.Close()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		return string(data)
	}
}

func TestUpdateLogsTriggerAndState(t *testing.T) {
	read := enableTUILog(t, "debug")
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	var m tea.Model = model{agent: ag, md: newRenderer(80), input: newInput(80)}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	out := read()
	if !strings.Contains(out, "[tui] update resize") {
		t.Errorf("resize trigger not logged:\n%s", out)
	}
	if !strings.Contains(out, "[tui] update key key=tab") {
		t.Errorf("key trigger not logged:\n%s", out)
	}
	if !strings.Contains(out, "[tui] layout") {
		t.Errorf("layout computation not logged:\n%s", out)
	}
	if !strings.Contains(out, "[tui] state") {
		t.Errorf("resulting state not logged:\n%s", out)
	}
}

func TestViewFramesCoalesceAtTrace(t *testing.T) {
	read := enableTUILog(t, "trace")
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	m := model{agent: ag, md: newRenderer(80), input: newInput(80)}
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = tm.(model)
	// Three identical renders should coalesce; a fourth after a change should not.
	m.View()
	m.View()
	m.View()
	m.notice = "changed"
	m.View()
	out := read()
	if !strings.Contains(out, "[tui] frame render") {
		t.Errorf("frame renders not logged at trace:\n%s", out)
	}
	if !strings.Contains(out, "frame unchanged") {
		t.Errorf("identical frames not coalesced:\n%s", out)
	}
}

func TestViewFramesSilentAtDebug(t *testing.T) {
	read := enableTUILog(t, "debug")
	ag := agent.New(llm.Stub{}, tool.NewRegistry(), "")
	m := model{agent: ag, md: newRenderer(80), input: newInput(80)}
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = tm.(model)
	m.View()
	m.View()
	out := read()
	if strings.Contains(out, "frame render") {
		t.Errorf("frames should be silent at DEBUG level:\n%s", out)
	}
}
