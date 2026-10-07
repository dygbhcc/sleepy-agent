package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sleepy-agent/internal/errs"
)

// Model names change (a hardcoded default returned HTTP 404 on the first real
// call), so there is deliberately no default model.
const groqBaseURL = "https://api.groq.com/openai/v1"

// OpenAICompat talks to any OpenAI compatible chat completions endpoint:
// Groq, OpenAI, or a local server. It uses only net/http.
type OpenAICompat struct {
	name    string
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewGroq returns a provider for Groq. An empty model picks a default.
func NewGroq(apiKey, model string) *OpenAICompat {
	return NewOpenAICompat("groq", groqBaseURL, apiKey, model)
}

// NewOpenAICompat returns a provider for baseURL, for example
// "https://api.openai.com/v1".
func NewOpenAICompat(name, baseURL, apiKey, model string) *OpenAICompat {
	return &OpenAICompat{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Name implements Provider.
func (p *OpenAICompat) Name() string { return p.name + ":" + p.model }

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	// ResponseFormat forces a JSON object reply. Without it, some models
	// (gpt-oss on Groq) try a native tool call and the API answers HTTP 400.
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete implements Provider. Rate limits and server errors come back as
// errs.TransientError so callers can retry them, everything else is permanent.
func (p *OpenAICompat) Complete(ctx context.Context, req Request) (Response, error) {
	body := chatRequest{Model: p.model, Temperature: req.Temperature, MaxTokens: req.MaxTokens}
	if req.JSON {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, chatMessage{Role: string(m.Role), Content: m.Content})
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("%s: encode request: %w", p.name, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Response{}, fmt.Errorf("%s: build request: %w", p.name, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		// Network failures are worth retrying. The error text never contains
		// the API key, it is only sent in a header.
		return Response{}, errs.NewTransient(p.name, 0, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Response{}, errs.NewTransient(p.name, resp.StatusCode, err)
	}

	var parsed chatResponse
	_ = json.Unmarshal(raw, &parsed) // an unparseable body is handled by the status checks below

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg += ": " + parsed.Error.Message
		}
		cause := errors.New(msg)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return Response{}, errs.NewTransient(p.name, resp.StatusCode, cause)
		}
		return Response{}, fmt.Errorf("%s: %w", p.name, cause)
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("%s: response had no choices", p.name)
	}

	return Response{
		Content: parsed.Choices[0].Message.Content,
		Usage: Usage{
			PromptTokens:     parsed.Usage.PromptTokens,
			CompletionTokens: parsed.Usage.CompletionTokens,
		},
	}, nil
}
