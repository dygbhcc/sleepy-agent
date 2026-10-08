package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PromptTools adapts a model that has no native tool calling (or whose native
// tool calling is unreliable) to the Provider contract. Tools are described in
// the system prompt, the model answers with one JSON object, and the adapter
// parses that object into Response.ToolCalls or a final Content.
//
// This is the adapter Day 3 used implicitly. It is now explicit, so it can be
// compared with native tool calling on the same task (see cmd/compare).
type PromptTools struct {
	inner Provider
}

// NewPromptTools wraps inner.
func NewPromptTools(inner Provider) *PromptTools { return &PromptTools{inner: inner} }

// Name implements Provider.
func (p *PromptTools) Name() string { return p.inner.Name() + " [prompt tools]" }

// Complete implements Provider.
func (p *PromptTools) Complete(ctx context.Context, req Request) (Response, error) {
	out := Request{
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		JSON:        true,
		Messages:    promptMessages(req.Messages, req.Tools),
	}
	resp, err := p.inner.Complete(ctx, out)
	if err != nil {
		return resp, err
	}
	call, final, perr := parseJSONReply(resp.Content)
	if perr != nil {
		return Response{Content: resp.Content, Usage: resp.Usage}, fmt.Errorf("%w: %v (reply with exactly one JSON object)", ErrMalformedReply, perr)
	}
	if call != nil {
		return Response{ToolCalls: []ToolCall{*call}, Usage: resp.Usage}, nil
	}
	return Response{Content: final, Usage: resp.Usage}, nil
}

// promptMessages rewrites the normalized conversation as plain chat turns.
func promptMessages(in []Message, tools []ToolSpec) []Message {
	system := protocolPrompt(tools)
	out := make([]Message, 0, len(in)+1)
	merged := false
	for _, m := range in {
		switch {
		case m.Role == RoleSystem && !merged:
			out = append(out, Message{Role: RoleSystem, Content: m.Content + "\n\n" + system})
			merged = true
		case m.Role == RoleAssistant && len(m.ToolCalls) > 0:
			b, _ := json.Marshal(map[string]any{"tool": m.ToolCalls[0].Name, "args": m.ToolCalls[0].Args})
			out = append(out, Message{Role: RoleAssistant, Content: string(b)})
		case m.Role == RoleTool:
			out = append(out, Message{Role: RoleUser, Content: "Observation: " + m.Content})
		default:
			out = append(out, Message{Role: m.Role, Content: m.Content})
		}
	}
	if !merged {
		out = append([]Message{{Role: RoleSystem, Content: system}}, out...)
	}
	return out
}

func protocolPrompt(tools []ToolSpec) string {
	var b strings.Builder
	b.WriteString("Reply with exactly one JSON object and nothing else.\n")
	b.WriteString(`To call a tool: {"tool": "<name>", "args": {...}}` + "\n")
	b.WriteString(`To finish: {"final": "<your answer>"}` + "\n")
	b.WriteString("After each tool call you will get an Observation. Use it to decide the next step.\n\nTools:\n")
	sorted := append([]ToolSpec(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, t := range sorted {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name, t.Description)
		var schema struct {
			Properties map[string]struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"properties"`
		}
		_ = json.Unmarshal(t.Parameters, &schema)
		keys := make([]string, 0, len(schema.Properties))
		for k := range schema.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "    %s (%s): %s\n", k, schema.Properties[k].Type, schema.Properties[k].Description)
		}
	}
	return b.String()
}

// parseJSONReply reads the first JSON object in the model's answer. Models
// often wrap JSON in code fences or add a sentence around it, so the parser
// looks for the first '{' instead of demanding a clean string.
func parseJSONReply(content string) (call *ToolCall, final string, err error) {
	start := strings.Index(content, "{")
	if start < 0 {
		return nil, "", fmt.Errorf("no JSON object found")
	}
	var raw struct {
		Tool  string         `json:"tool"`
		Args  map[string]any `json:"args"`
		Final *string        `json:"final"`
	}
	if err := json.NewDecoder(strings.NewReader(content[start:])).Decode(&raw); err != nil {
		return nil, "", fmt.Errorf("malformed JSON: %v", err)
	}
	switch {
	case raw.Tool != "" && raw.Final != nil:
		return nil, "", fmt.Errorf(`got both "tool" and "final"`)
	case raw.Final != nil:
		return nil, *raw.Final, nil
	case raw.Tool != "":
		if raw.Args == nil {
			raw.Args = map[string]any{}
		}
		return &ToolCall{Name: raw.Tool, Args: raw.Args}, "", nil
	default:
		return nil, "", fmt.Errorf(`need either "tool" or "final"`)
	}
}
