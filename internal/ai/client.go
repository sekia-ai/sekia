package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

const anthropicAPIURL = "https://api.anthropic.com/v1/messages"

// LLMClient abstracts LLM API calls for testability.
type LLMClient interface {
	Complete(ctx context.Context, req CompleteRequest) (string, error)
}

// CompleteRequest holds parameters for an LLM completion call.
type CompleteRequest struct {
	Prompt       string
	SystemPrompt string  // overrides config default if non-empty
	Model        string  // overrides config default if non-empty
	MaxTokens    int     // overrides config default if > 0
	Temperature  float64 // -1 means use config default
	JSONMode     bool
}

// anthropicClient implements LLMClient using the Anthropic Messages API.
type anthropicClient struct {
	apiKey       string
	baseURL      string
	model        string
	maxTokens    int
	temperature  float64
	systemPrompt string
	http         *http.Client
	logger       zerolog.Logger
}

// NewAnthropicClient creates an LLM client for the Anthropic Messages API.
func NewAnthropicClient(cfg Config, logger zerolog.Logger) *anthropicClient {
	return &anthropicClient{
		apiKey:       cfg.APIKey,
		baseURL:      anthropicAPIURL,
		model:        cfg.Model,
		maxTokens:    cfg.MaxTokens,
		temperature:  cfg.Temperature,
		systemPrompt: cfg.SystemPrompt,
		http:         &http.Client{Timeout: 60 * time.Second},
		logger:       logger.With().Str("component", "ai").Logger(),
	}
}

// messagesRequest is the Anthropic Messages API request body.
type messagesRequest struct {
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
	System      string    `json:"system,omitempty"`
	Messages    []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// messagesResponse is the Anthropic Messages API response body.
type messagesResponse struct {
	Content []contentBlock `json:"content"`
	Error   *apiError      `json:"error,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (c *anthropicClient) Complete(ctx context.Context, req CompleteRequest) (string, error) {
	model := c.model
	if req.Model != "" {
		model = req.Model
	}

	maxTokens := c.maxTokens
	if req.MaxTokens > 0 {
		maxTokens = req.MaxTokens
	}

	temperature := c.temperature
	if req.Temperature >= 0 {
		temperature = req.Temperature
	}

	systemPrompt := c.systemPrompt
	if req.SystemPrompt != "" {
		systemPrompt = req.SystemPrompt
	}
	if req.JSONMode {
		prefix := "Respond with valid JSON only. No other text."
		if systemPrompt != "" {
			systemPrompt = prefix + "\n\n" + systemPrompt
		} else {
			systemPrompt = prefix
		}
	}

	body := messagesRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: temperature,
		System:      systemPrompt,
		Messages: []message{
			{Role: "user", Content: req.Prompt},
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	c.logger.Debug().
		Str("model", model).
		Int("max_tokens", maxTokens).
		Msg("calling Anthropic API")

	resp, err := c.http.Do(httpReq) // #nosec G704 -- URL is configured API base, not user input
	if err != nil {
		return "", fmt.Errorf("anthropic API request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var msgResp messagesResponse
	if err := json.Unmarshal(respBody, &msgResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if len(msgResp.Content) == 0 {
		return "", fmt.Errorf("anthropic API returned empty content")
	}

	return msgResp.Content[0].Text, nil
}
