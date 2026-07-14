package agent

import (
	"testing"

	"github.com/syrull/pluto/internal/llm"
	"github.com/syrull/pluto/internal/tool"
)

func TestAddContextAppendsUserTurn(t *testing.T) {
	a := New(llm.Stub{}, tool.NewRegistry(), "sys")
	a.AddContext("$ git status\nclean")

	msgs := a.Snapshot()
	idx := findUser(msgs, "$ git status\nclean")
	if idx < 0 {
		t.Fatalf("AddContext did not append a user turn; roles=%v", roles(msgs))
	}
	if msgs[0].Role != llm.RoleSystem {
		t.Fatalf("system prompt must stay at the head, got %v", msgs[0].Role)
	}
}
