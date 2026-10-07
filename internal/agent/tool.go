// Package agent is the framework free agent loop.
//
// The loop is small on purpose: ask the model what to do, check the answer,
// let a Decider approve the action, run the tool, feed the result back, repeat.
// Every safety rule lives in code (validation, Decider, step limit), not in the
// prompt.
package agent

import (
	"context"
	"fmt"
	"sort"
)

// Tool is something the agent may call.
type Tool interface {
	// Name is the identifier the model uses to call the tool.
	Name() string
	// Description tells the model what the tool does.
	Description() string
	// Schema describes the arguments the tool accepts.
	Schema() Schema
	// Run executes the tool. A returned error becomes an observation for the
	// model, it never crashes the loop.
	Run(ctx context.Context, args map[string]any) (string, error)
}

// Prop describes one argument of a tool.
type Prop struct {
	Type        string // "string", "number", "integer" or "boolean"
	Description string
}

// Schema is a deliberately small subset of JSON Schema: a flat object with
// typed properties and a list of required ones.
type Schema struct {
	Properties map[string]Prop
	Required   []string
}

// Validate checks args against the schema. The model's arguments are
// untrusted input, so unknown fields are rejected too.
func (s Schema) Validate(args map[string]any) error {
	for _, name := range s.Required {
		if _, ok := args[name]; !ok {
			return fmt.Errorf("missing required argument %q", name)
		}
	}
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		prop, ok := s.Properties[name]
		if !ok {
			return fmt.Errorf("unknown argument %q", name)
		}
		if err := checkType(name, prop.Type, args[name]); err != nil {
			return err
		}
	}
	return nil
}

func checkType(name, want string, v any) error {
	ok := false
	switch want {
	case "string":
		_, ok = v.(string)
	case "boolean":
		_, ok = v.(bool)
	case "number":
		_, ok = v.(float64)
	case "integer":
		f, isNum := v.(float64)
		ok = isNum && f == float64(int64(f))
	default:
		return fmt.Errorf("argument %q has unsupported schema type %q", name, want)
	}
	if !ok {
		return fmt.Errorf("argument %q must be of type %s, got %T", name, want, v)
	}
	return nil
}
