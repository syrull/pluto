package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
	"github.com/syrull/pluto/internal/worker"
)

func newTestModelWithPool(t *testing.T) (model, *worker.Pool) {
	t.Helper()
	pool := worker.NewPool(context.Background(), worker.Config{
		Provider: llm.Stub{},
		Registry: tool.NewRegistry(),
	})
	var m tea.Model = model{pool: pool, md: newRenderer(80), input: newInput(80)}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m.(model), pool
}

func TestWorkersStatusEmpty(t *testing.T) {
	m, _ := newTestModelWithPool(t)
	out := m.workersStatus()
	if !strings.Contains(out, "no workers dispatched") {
		t.Fatalf("empty workers status = %q, want a 'no workers' hint", out)
	}
}

func TestWorkersStatusNilPool(t *testing.T) {
	var m tea.Model = model{md: newRenderer(80), input: newInput(80)}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm := m.(model)
	if out := mm.workersStatus(); !strings.Contains(out, "not available") {
		t.Fatalf("nil-pool workers status = %q, want 'not available'", out)
	}
}

func TestWorkersStatusListsDispatched(t *testing.T) {
	m, pool := newTestModelWithPool(t)
	ids := pool.Dispatch(context.Background(), []worker.Spec{{Task: "recon the target"}})
	if len(ids) != 1 {
		t.Fatalf("dispatched %d workers, want 1", len(ids))
	}
	// The stub finishes on the first turn; wait for done so the row is stable.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && pool.Poll(ids)[0].State != worker.StateDone {
		time.Sleep(5 * time.Millisecond)
	}
	out := m.workersStatus()
	if !strings.Contains(out, ids[0]) {
		t.Fatalf("workers status %q missing worker id %q", out, ids[0])
	}
	if !strings.Contains(out, "workers (1") {
		t.Fatalf("workers status %q missing the header count", out)
	}
}

func TestWorkerTranscriptUnknownID(t *testing.T) {
	m, _ := newTestModelWithPool(t)
	if out := m.workerTranscript("nope"); !strings.Contains(out, "no worker with id") {
		t.Fatalf("transcript for unknown id = %q, want an error hint", out)
	}
}

func TestWorkersCommandDispatch(t *testing.T) {
	m, pool := newTestModelWithPool(t)
	pool.Dispatch(context.Background(), []worker.Spec{{Task: "scan"}})
	status, _ := m.handleCommand("/workers")
	if !strings.Contains(status, "workers (1") {
		t.Fatalf("/workers status = %q, want a worker listing", status)
	}
}
