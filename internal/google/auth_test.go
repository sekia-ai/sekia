package google

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestAuthFlow(t *testing.T) {
	// Mock token endpoint: returns a token when called with a valid code.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("code") != "test-auth-code" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "test-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "test-refresh-token",
		})
	}))
	defer tokenSrv.Close()

	// Override the Google endpoint to point to our mock.
	origEndpoint := googleoauth2Endpoint
	googleoauth2Endpoint = oauth2.Endpoint{
		AuthURL:  "https://accounts.google.com/o/oauth2/auth", // not actually hit
		TokenURL: tokenSrv.URL,
	}
	defer func() { googleoauth2Endpoint = origEndpoint }()

	// Create a listener for the callback server.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run auth flow in background.
	type result struct {
		token *oauth2.Token
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		token, err := authFlowWithListener(ctx, "test-client-id", "test-client-secret", listener)
		ch <- result{token, err}
	}()

	// Simulate the browser redirect callback.
	// We need to figure out the state parameter. Since we can't predict the random
	// state, we'll hit the callback and check the response. Instead, let's make a
	// request to the auth URL and extract the state from the redirect.
	// Actually, the simpler approach: hit the callback with the wrong state first
	// to verify CSRF check, then we need the real state. Since the state is random,
	// we'll need to get it from the auth URL. But in the loopback flow the auth URL
	// is just printed to stdout. For testing, let's accept that we test the token
	// exchange and callback mechanics by directly hitting the callback.

	// Wait a moment for the server to start.
	time.Sleep(50 * time.Millisecond)

	// Since we can't know the random state, send a request with the error param
	// to test error handling.
	resp, err := http.Get("http://localhost:" + itoa(port) + "/?error=access_denied")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	resp.Body.Close()

	res := <-ch
	if res.err == nil {
		t.Fatal("expected error for denied authorization")
	}
	if res.token != nil {
		t.Error("expected nil token for denied authorization")
	}
}

func TestAuthFlowSuccess(t *testing.T) {
	// Mock token endpoint.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "test-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "test-refresh-token",
		})
	}))
	defer tokenSrv.Close()

	// Override endpoint.
	origEndpoint := googleoauth2Endpoint
	googleoauth2Endpoint = oauth2.Endpoint{
		AuthURL:  "https://accounts.google.com/o/oauth2/auth",
		TokenURL: tokenSrv.URL,
	}
	defer func() { googleoauth2Endpoint = origEndpoint }()

	// We'll use authFlowWithExchange to test the full flow with a known state.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Capture the state by intercepting stdout isn't practical.
	// Instead, test using authFlowWithState for full control.
	type result struct {
		token *oauth2.Token
		err   error
	}
	ch := make(chan result, 1)

	knownState := "test-state-123"
	go func() {
		token, err := authFlowWithState(ctx, "test-client-id", "test-client-secret", listener, knownState)
		ch <- result{token, err}
	}()

	time.Sleep(50 * time.Millisecond)

	// Simulate successful callback with matching state.
	callbackURL := "http://localhost:" + itoa(port) + "/?code=test-auth-code&state=" + url.QueryEscape(knownState)
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	resp.Body.Close()

	res := <-ch
	if res.err != nil {
		t.Fatalf("AuthFlow: %v", res.err)
	}
	if res.token.AccessToken != "test-access-token" {
		t.Errorf("access_token = %s, want test-access-token", res.token.AccessToken)
	}
	if res.token.RefreshToken != "test-refresh-token" {
		t.Errorf("refresh_token = %s, want test-refresh-token", res.token.RefreshToken)
	}
}

func TestAuthFlowStateMismatch(t *testing.T) {
	origEndpoint := googleoauth2Endpoint
	googleoauth2Endpoint = oauth2.Endpoint{
		AuthURL:  "https://accounts.google.com/o/oauth2/auth",
		TokenURL: "http://localhost:1/unused",
	}
	defer func() { googleoauth2Endpoint = origEndpoint }()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type result struct {
		token *oauth2.Token
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		token, err := authFlowWithState(ctx, "test-client-id", "test-client-secret", listener, "correct-state")
		ch <- result{token, err}
	}()

	time.Sleep(50 * time.Millisecond)

	// Send callback with wrong state.
	resp, err := http.Get("http://localhost:" + itoa(port) + "/?code=test-code&state=wrong-state")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}

	res := <-ch
	if res.err == nil {
		t.Fatal("expected error for state mismatch")
	}
}

func TestTokenFilePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token.json")

	original := &oauth2.Token{
		AccessToken:  "access-123",
		TokenType:    "Bearer",
		RefreshToken: "refresh-456",
		Expiry:       time.Now().Add(time.Hour).Truncate(time.Second),
	}

	if err := SaveTokenToFile(path, original); err != nil {
		t.Fatalf("SaveTokenToFile: %v", err)
	}

	// Verify file permissions.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("permissions = %o, want 0600", perm)
	}

	loaded, err := LoadTokenFromFile(path)
	if err != nil {
		t.Fatalf("LoadTokenFromFile: %v", err)
	}

	if loaded.AccessToken != original.AccessToken {
		t.Errorf("access_token = %s, want %s", loaded.AccessToken, original.AccessToken)
	}
	if loaded.RefreshToken != original.RefreshToken {
		t.Errorf("refresh_token = %s, want %s", loaded.RefreshToken, original.RefreshToken)
	}
}

func TestTokenFileNested(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dir", "token.json")

	token := &oauth2.Token{AccessToken: "test", TokenType: "Bearer"}
	if err := SaveTokenToFile(path, token); err != nil {
		t.Fatalf("SaveTokenToFile (nested): %v", err)
	}

	loaded, err := LoadTokenFromFile(path)
	if err != nil {
		t.Fatalf("LoadTokenFromFile: %v", err)
	}
	if loaded.AccessToken != "test" {
		t.Errorf("access_token = %s, want test", loaded.AccessToken)
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
