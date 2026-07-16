package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/syrull/pluto/internal/debug"
	"github.com/syrull/pluto/internal/skills"
	"github.com/syrull/pluto/internal/tool"
	"github.com/syrull/pluto/internal/workdir"
)

// Skill lists and loads on-demand skill playbooks from the skills/ directory.
type Skill struct{}

var _ tool.Tool = Skill{}

func (Skill) Name() string { return "skill" }
func (Skill) Description() string {
	return "List and load skills — short, single-topic playbooks kept as plain " +
		"text files under the skills/ directory. Call with no arguments to list " +
		"the available skills by name and summary; pass name to load one skill's " +
		"full guidance into context when the current task calls for it, instead of " +
		"carrying every playbook up front. Skills are plain text, so you can also " +
		"read/find them directly."
}

func (Skill) Schema() json.RawMessage {
	return tool.ObjectSchema(map[string]tool.Property{
		"name": {Type: "string", Description: "Name of the skill to load. Omit to list all available skills."},
	}).MustJSON()
}

type skillArgs struct {
	Name string `json:"name"`
}

func (Skill) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a skillArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("skill: invalid arguments: %w", err)
	}
	dir := workdir.Resolve(ctx, skills.DirName)
	name := strings.TrimSpace(a.Name)

	if name == "" {
		timer := debug.NewTimer("tool", "skill list")
		list := skills.List(dir)
		timer.Stop("dir", dir, "count", len(list))
		if len(list) == 0 {
			debug.Debug("tool", "skill list empty", "dir", dir)
			return "No skills available. Add plain-text playbooks under the skills/ directory.", nil
		}
		debug.Info("tool", "skills listed", "dir", dir, "count", len(list))
		return "Available skills (load one by name):\n" + skills.Render(list), nil
	}

	canonical := skills.Canonical(name)
	timer := debug.NewTimer("tool", "skill load")
	body, err := skills.Load(dir, name)
	if err != nil {
		timer.Stop("dir", dir, "name", canonical, "outcome", "error")
		debug.Warn("tool", "skill load failed", "name", canonical, "err", err)
		if list := skills.List(dir); len(list) > 0 {
			return "", fmt.Errorf("%w; available skills:\n%s", err, skills.Render(list))
		}
		return "", err
	}
	timer.Stop("dir", dir, "name", canonical, "outcome", "ok", "chars", len(body))
	debug.Info("tool", "skill loaded", "name", canonical, "chars", len(body))
	return fmt.Sprintf("--- skill: %s ---\n%s", canonical, body), nil
}
