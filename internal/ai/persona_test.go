package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPersona_FileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "persona.md")
	if err := os.WriteFile(path, []byte("You are a helpful assistant.\n"), 0644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadPersona(path)
	if err != nil {
		t.Fatalf("LoadPersona() error: %v", err)
	}
	if content != "You are a helpful assistant." {
		t.Errorf("content = %q, want %q", content, "You are a helpful assistant.")
	}
}

func TestLoadPersona_FileNotExists(t *testing.T) {
	content, err := LoadPersona("/nonexistent/persona.md")
	if err != nil {
		t.Fatalf("LoadPersona() error: %v", err)
	}
	if content != "" {
		t.Errorf("content = %q, want empty string", content)
	}
}

func TestLoadPersona_EmptyPath(t *testing.T) {
	content, err := LoadPersona("")
	if err != nil {
		t.Fatalf("LoadPersona() error: %v", err)
	}
	if content != "" {
		t.Errorf("content = %q, want empty string", content)
	}
}

func TestLoadPersona_WhitespaceOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "persona.md")
	if err := os.WriteFile(path, []byte("  \n\n  "), 0644); err != nil {
		t.Fatal(err)
	}

	content, err := LoadPersona(path)
	if err != nil {
		t.Fatalf("LoadPersona() error: %v", err)
	}
	if content != "" {
		t.Errorf("content = %q, want empty string", content)
	}
}

func TestAnthropicClient_PersonaPrepended(t *testing.T) {
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
		apiKey:        "test-key",
		baseURL:       srv.URL,
		model:         "test-model",
		maxTokens:     1024,
		personaPrompt: "You are Atlas, a concise technical assistant.",
		http:          http.DefaultClient,
		logger:        testLogger(),
	}

	_, err := c.Complete(context.Background(), CompleteRequest{
		Prompt:      "hello",
		Temperature: -1,
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	want := "You are Atlas, a concise technical assistant."
	if capturedReq.System != want {
		t.Errorf("system = %q, want %q", capturedReq.System, want)
	}
}

func TestAnthropicClient_PersonaWithSystemPrompt(t *testing.T) {
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
		apiKey:        "test-key",
		baseURL:       srv.URL,
		model:         "test-model",
		maxTokens:     1024,
		personaPrompt: "You are Atlas.",
		systemPrompt:  "Be concise.",
		http:          http.DefaultClient,
		logger:        testLogger(),
	}

	_, err := c.Complete(context.Background(), CompleteRequest{
		Prompt:      "hello",
		Temperature: -1,
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	want := "You are Atlas.\n\nBe concise."
	if capturedReq.System != want {
		t.Errorf("system = %q, want %q", capturedReq.System, want)
	}
}

func TestAnthropicClient_PersonaWithOverrideSystemPrompt(t *testing.T) {
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
		apiKey:        "test-key",
		baseURL:       srv.URL,
		model:         "test-model",
		maxTokens:     1024,
		personaPrompt: "You are Atlas.",
		systemPrompt:  "default system",
		http:          http.DefaultClient,
		logger:        testLogger(),
	}

	_, err := c.Complete(context.Background(), CompleteRequest{
		Prompt:       "hello",
		SystemPrompt: "custom system",
		Temperature:  -1,
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	// Per-call system prompt overrides default, persona still prepended.
	want := "You are Atlas.\n\ncustom system"
	if capturedReq.System != want {
		t.Errorf("system = %q, want %q", capturedReq.System, want)
	}
}

func TestAnthropicClient_PersonaWithJSONMode(t *testing.T) {
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
		apiKey:        "test-key",
		baseURL:       srv.URL,
		model:         "test-model",
		maxTokens:     1024,
		personaPrompt: "You are Atlas.",
		http:          http.DefaultClient,
		logger:        testLogger(),
	}

	_, err := c.Complete(context.Background(), CompleteRequest{
		Prompt:      "classify",
		JSONMode:    true,
		Temperature: -1,
	})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}

	// JSON prefix comes first, then persona.
	want := "Respond with valid JSON only. No other text.\n\nYou are Atlas."
	if capturedReq.System != want {
		t.Errorf("system = %q, want %q", capturedReq.System, want)
	}
}

func TestSetPersonaPrompt(t *testing.T) {
	c := &anthropicClient{}
	c.SetPersonaPrompt("new persona")
	if c.personaPrompt != "new persona" {
		t.Errorf("personaPrompt = %q, want %q", c.personaPrompt, "new persona")
	}
}
