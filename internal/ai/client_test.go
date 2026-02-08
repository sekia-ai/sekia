package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/rs/zerolog"
)

func testLogger() zerolog.Logger {
	return zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
}

func TestAnthropicClient_Complete(t *testing.T) {
	var capturedReq messagesRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers.
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q, want %q", got, "test-key")
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("anthropic-version = %q, want %q", got, "2023-06-01")
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}

		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{
				{Type: "text", Text: "bug"},
			},
		})
	}))
	defer srv.Close()

	c := &anthropicClient{
		apiKey:      "test-key",
		baseURL:     srv.URL,
		model:       "claude-sonnet-4-20250514",
		maxTokens:   1024,
		temperature: 0.0,
		http:        http.DefaultClient,
		logger:      testLogger(),
	}

	result, err := c.Complete(context.Background(), CompleteRequest{
		Prompt: "classify this",
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if result != "bug" {
		t.Errorf("result = %q, want %q", result, "bug")
	}

	// Verify request body.
	if capturedReq.Model != "claude-sonnet-4-20250514" {
		t.Errorf("model = %q, want %q", capturedReq.Model, "claude-sonnet-4-20250514")
	}
	if capturedReq.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d, want %d", capturedReq.MaxTokens, 1024)
	}
	if len(capturedReq.Messages) != 1 || capturedReq.Messages[0].Content != "classify this" {
		t.Errorf("messages = %+v, want single user message", capturedReq.Messages)
	}
}

func TestAnthropicClient_Overrides(t *testing.T) {
	var capturedReq messagesRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{{Type: "text", Text: "ok"}},
		})
	}))
	defer srv.Close()

	c := &anthropicClient{
		apiKey:       "test-key",
		baseURL:      srv.URL,
		model:        "default-model",
		maxTokens:    1024,
		temperature:  0.5,
		systemPrompt: "default system",
		http:         http.DefaultClient,
		logger:       testLogger(),
	}

	_, err := c.Complete(context.Background(), CompleteRequest{
		Prompt:       "test",
		Model:        "custom-model",
		MaxTokens:    256,
		Temperature:  0.0,
		SystemPrompt: "custom system",
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	if capturedReq.Model != "custom-model" {
		t.Errorf("model = %q, want %q", capturedReq.Model, "custom-model")
	}
	if capturedReq.MaxTokens != 256 {
		t.Errorf("max_tokens = %d, want %d", capturedReq.MaxTokens, 256)
	}
	if capturedReq.Temperature != 0.0 {
		t.Errorf("temperature = %f, want %f", capturedReq.Temperature, 0.0)
	}
	if capturedReq.System != "custom system" {
		t.Errorf("system = %q, want %q", capturedReq.System, "custom system")
	}
}

func TestAnthropicClient_JSONMode(t *testing.T) {
	var capturedReq messagesRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{{Type: "text", Text: `{"label":"bug"}`}},
		})
	}))
	defer srv.Close()

	c := &anthropicClient{
		apiKey:  "test-key",
		baseURL: srv.URL,
		model:   "test-model",
		maxTokens: 1024,
		http:    http.DefaultClient,
		logger:  testLogger(),
	}

	result, err := c.Complete(context.Background(), CompleteRequest{
		Prompt:      "classify",
		JSONMode:    true,
		Temperature: -1,
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if result != `{"label":"bug"}` {
		t.Errorf("result = %q, want JSON", result)
	}

	// Verify JSON mode prepends system prompt.
	want := "Respond with valid JSON only. No other text."
	if capturedReq.System != want {
		t.Errorf("system = %q, want %q", capturedReq.System, want)
	}
}

func TestAnthropicClient_JSONModeWithSystemPrompt(t *testing.T) {
	var capturedReq messagesRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &capturedReq)

		json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{{Type: "text", Text: `{}`}},
		})
	}))
	defer srv.Close()

	c := &anthropicClient{
		apiKey:       "test-key",
		baseURL:      srv.URL,
		model:        "test-model",
		maxTokens:    1024,
		systemPrompt: "Be concise.",
		http:         http.DefaultClient,
		logger:       testLogger(),
	}

	_, err := c.Complete(context.Background(), CompleteRequest{
		Prompt:      "test",
		JSONMode:    true,
		Temperature: -1,
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	want := "Respond with valid JSON only. No other text.\n\nBe concise."
	if capturedReq.System != want {
		t.Errorf("system = %q, want %q", capturedReq.System, want)
	}
}

func TestAnthropicClient_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit","message":"too many requests"}}`))
	}))
	defer srv.Close()

	c := &anthropicClient{
		apiKey:  "test-key",
		baseURL: srv.URL,
		model:   "test-model",
		maxTokens: 1024,
		http:    http.DefaultClient,
		logger:  testLogger(),
	}

	_, err := c.Complete(context.Background(), CompleteRequest{
		Prompt:      "test",
		Temperature: -1,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	want := "anthropic API error (status 429)"
	if got := err.Error(); len(got) < len(want) || got[:len(want)] != want {
		t.Errorf("error = %q, want prefix %q", got, want)
	}
}

func TestAnthropicClient_EmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(messagesResponse{
			Content: []contentBlock{},
		})
	}))
	defer srv.Close()

	c := &anthropicClient{
		apiKey:  "test-key",
		baseURL: srv.URL,
		model:   "test-model",
		maxTokens: 1024,
		http:    http.DefaultClient,
		logger:  testLogger(),
	}

	_, err := c.Complete(context.Background(), CompleteRequest{
		Prompt:      "test",
		Temperature: -1,
	})
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
}
