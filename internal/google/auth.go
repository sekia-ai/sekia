package google

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"golang.org/x/oauth2"
	googleoauth2 "golang.org/x/oauth2/google"
)

// OAuth scopes required for Gmail + Calendar access.
var oauthScopes = []string{
	"https://www.googleapis.com/auth/gmail.modify",
	"https://www.googleapis.com/auth/gmail.send",
	"https://www.googleapis.com/auth/calendar",
}

// googleoauth2Endpoint is the Google OAuth2 endpoint; overridden in tests.
var googleoauth2Endpoint = googleoauth2.Endpoint

// OAuthConfig builds an oauth2.Config for the Google agent.
func OAuthConfig(clientID, clientSecret string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     googleoauth2Endpoint,
		Scopes:       oauthScopes,
		RedirectURL:  "http://localhost", // port appended at runtime
	}
}

// AuthFlow runs the OAuth2 authorization code flow with a loopback redirect.
// It starts a temporary localhost HTTP server, opens the user's browser to
// Google's consent screen, and captures the authorization code via redirect.
func AuthFlow(ctx context.Context, clientID, clientSecret string) (*oauth2.Token, error) {
	return authFlowWithListener(ctx, clientID, clientSecret, nil)
}

// authFlowWithListener is the testable version that accepts an optional listener.
// If listener is nil, a random localhost port is chosen.
func authFlowWithListener(ctx context.Context, clientID, clientSecret string, listener net.Listener) (*oauth2.Token, error) {
	var err error
	if listener == nil {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("listen on localhost: %w", err)
		}
	}

	// Generate random state for CSRF protection.
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		listener.Close()
		return nil, fmt.Errorf("generate state: %w", err)
	}
	state := hex.EncodeToString(stateBytes)

	return runCallbackServer(ctx, clientID, clientSecret, listener, state, true)
}

// authFlowWithState is the fully testable version with a known state value.
func authFlowWithState(ctx context.Context, clientID, clientSecret string, listener net.Listener, state string) (*oauth2.Token, error) {
	return runCallbackServer(ctx, clientID, clientSecret, listener, state, false)
}

// runCallbackServer starts a temporary HTTP server, waits for the OAuth callback,
// and exchanges the authorization code for a token.
func runCallbackServer(ctx context.Context, clientID, clientSecret string, listener net.Listener, state string, showBrowser bool) (*oauth2.Token, error) {
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://localhost:%d", port)

	cfg := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     googleoauth2Endpoint,
		Scopes:       oauthScopes,
		RedirectURL:  redirectURL,
	}

	if showBrowser {
		authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
		fmt.Printf("\nOpening browser to authorize sekia-google...\n\n")
		fmt.Printf("If the browser doesn't open, visit this URL:\n\n  %s\n\n", authURL)
		openBrowser(authURL)
	}

	// Wait for the callback.
	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			return
		}

		if errParam := r.URL.Query().Get("error"); errParam != "" {
			http.Error(w, "Authorization denied: "+errParam, http.StatusForbidden)
			ch <- result{err: fmt.Errorf("authorization denied: %s", errParam)}
			return
		}

		if r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			ch <- result{err: fmt.Errorf("state mismatch")}
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "No authorization code", http.StatusBadRequest)
			ch <- result{err: fmt.Errorf("no authorization code in callback")}
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h2>Authorization successful!</h2><p>You can close this tab.</p></body></html>")
		ch <- result{code: code}
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)

	var res result
	select {
	case <-ctx.Done():
		srv.Close()
		return nil, ctx.Err()
	case res = <-ch:
	}

	// Gracefully shut down so the handler's HTTP response is fully flushed
	// before we close the connection (srv.Close would kill it immediately).
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutCancel()
	srv.Shutdown(shutCtx)

	if res.err != nil {
		return nil, res.err
	}
	token, err := cfg.Exchange(ctx, res.code)
	if err != nil {
		return nil, fmt.Errorf("exchange code for token: %w", err)
	}
	fmt.Println("Authorization successful!")
	return token, nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	cmd.Start()
}

// PersistentTokenSource wraps an oauth2.TokenSource to persist refreshed
// tokens to disk. Thread-safe for concurrent use by multiple pollers.
type PersistentTokenSource struct {
	mu        sync.Mutex
	source    oauth2.TokenSource
	tokenPath string
	lastToken *oauth2.Token
}

// NewPersistentTokenSource creates a token source that auto-refreshes and
// saves new tokens to disk.
func NewPersistentTokenSource(cfg *oauth2.Config, tokenPath string) (*PersistentTokenSource, error) {
	token, err := LoadTokenFromFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("load token: %w", err)
	}

	source := cfg.TokenSource(context.Background(), token)

	return &PersistentTokenSource{
		source:    source,
		tokenPath: tokenPath,
		lastToken: token,
	}, nil
}

// Token returns a valid token, refreshing and persisting if needed.
func (p *PersistentTokenSource) Token() (*oauth2.Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	token, err := p.source.Token()
	if err != nil {
		return nil, err
	}

	// If the token changed (was refreshed), persist it.
	if token.AccessToken != p.lastToken.AccessToken {
		if saveErr := SaveTokenToFile(p.tokenPath, token); saveErr != nil {
			// Log but don't fail — the token is still valid in memory.
			fmt.Fprintf(os.Stderr, "warning: failed to save refreshed token: %v\n", saveErr)
		}
		p.lastToken = token
	}

	return token, nil
}

// LoadTokenFromFile reads an oauth2.Token from a JSON file.
func LoadTokenFromFile(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parse token file: %w", err)
	}
	return &token, nil
}

// SaveTokenToFile writes an oauth2.Token to a JSON file with 0600 permissions.
func SaveTokenToFile(path string, token *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create token dir: %w", err)
	}

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}
