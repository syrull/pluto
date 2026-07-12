// Package tool defines the contract every pluto capability implements and provides tool registration.
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Tool is a capability the agent can invoke; implementations must be safe to call concurrently.
type Tool interface {
	// Name returns the stable identifier used to select this tool.
	Name() string
	// Description returns a one-line summary surfaced to the model.
	Description() string
	// Schema returns a JSON schema describing the tool's arguments; it is advisory metadata only.
	Schema() json.RawMessage
	// Execute runs the tool against raw JSON arguments and returns a result string or error.
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry is an ordered, name-indexed collection of tools.
type Registry struct {
	byName map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Tool)}
}

// Register adds a tool; it returns an error on duplicate names so wiring mistakes surface at startup.
func (r *Registry) Register(t Tool) error {
	name := t.Name()
	if name == "" {
		return fmt.Errorf("tool: refusing to register tool with empty name")
	}
	if _, exists := r.byName[name]; exists {
		return fmt.Errorf("tool: %q already registered", name)
	}
	r.byName[name] = t
	return nil
}

// MustRegister registers a tool, panicking on error; for use in static startup wiring.
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Lookup returns the tool registered under name, or false.
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// Invoke resolves name and executes the tool, wrapping unknown-tool errors.
func (r *Registry) Invoke(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.Lookup(name)
	if !ok {
		return "", fmt.Errorf("tool: unknown tool %q", name)
	}
	return t.Execute(ctx, args)
}

// Tools returns all registered tools sorted by name for stable listing.
func (r *Registry) Tools() []Tool {
	out := make([]Tool, 0, len(r.byName))
	for _, t := range r.byName {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
