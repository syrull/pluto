package tool

import (
	"encoding/json"
	"fmt"
)

// Schema is a typed builder for the JSON Schema a tool advertises for its arguments.
type Schema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

type Property struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// ObjectSchema builds an object Schema from properties and a required field list.
func ObjectSchema(props map[string]Property, required ...string) Schema {
	return Schema{Type: "object", Properties: props, Required: required}
}

// MustJSON marshals the schema to JSON, panicking on failure; schemas are static tool metadata.
func (s Schema) MustJSON() json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("tool: marshal schema: %v", err))
	}
	return b
}
