package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

const (
	deviceCodeURL = "https://oauth2.googleapis.com/device/code"
	tokenURL      = "https://oauth2.googleapis.com/token"
)

// OAuthConfig builds an oauth2.Config for the Google agent.
func OAuthConfig(clientID, clientSecret string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint:     googleoauth2.Endpoint,
		Scopes:       oauthScopes,
	}
}

// deviceCodeResponse is returned by the device code endpoint.
type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// tokenResponse is returned by the token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// DeviceAuthFlow runs the OAuth2 device authorization grant flow.
// It prints the verification URL and user code to stdout, then polls
// Google's token endpoint until the user authorizes.
func DeviceAuthFlow(ctx context.Context, clientID, clientSecret string) (*oauth2.Token, error) {
	return deviceAuthFlowWithURLs(ctx, clientID, clientSecret, deviceCodeURL, tokenURL, 5*time.Second)
}

// deviceAuthFlowWithURLs is the testable implementation with configurable endpoints.
func deviceAuthFlowWithURLs(ctx context.Context, clientID, clientSecret, deviceURL, tokenEndpoint string, minInterval time.Duration) (*oauth2.Token, error) {
	// Step 1: Request device code.
	resp, err := http.PostForm(deviceURL, url.Values{
		"client_id": {clientID},
		"scope":     {joinScopes(oauthScopes)},
	})
	if err != nil {
		return nil, fmt.Errorf("request device code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read device code response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed (%d): %s", resp.StatusCode, body)
	}

	var dcResp deviceCodeResponse
	if err := json.Unmarshal(body, &dcResp); err != nil {
		return nil, fmt.Errorf("parse device code response: %w", err)
	}

	// Step 2: Show instructions to user.
	fmt.Printf("\nTo authorize sekia-google, visit:\n\n  %s\n\nAnd enter code: %s\n\nWaiting for authorization...\n", dcResp.VerificationURL, dcResp.UserCode)

	// Step 3: Poll for token.
	interval := time.Duration(dcResp.Interval) * time.Second
	if interval < minInterval {
		interval = minInterval
	}
	deadline := time.Now().Add(time.Duration(dcResp.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device authorization expired")
		}

		token, err := pollToken(tokenEndpoint, clientID, clientSecret, dcResp.DeviceCode)
		if err != nil {
			return nil, err
		}
		if token != nil {
			return token, nil
		}
		// nil token means authorization_pending — keep polling.
	}
}

func pollToken(tokenEndpoint, clientID, clientSecret, deviceCode string) (*oauth2.Token, error) {
	resp, err := http.PostForm(tokenEndpoint, url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"device_code":   {deviceCode},
		"grant_type":    {"urn:ietf:params:oauth:grant-type:device_code"},
	})
	if err != nil {
		return nil, fmt.Errorf("poll token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	var tResp tokenResponse
	if err := json.Unmarshal(body, &tResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	switch tResp.Error {
	case "":
		// Success.
		return &oauth2.Token{
			AccessToken:  tResp.AccessToken,
			TokenType:    tResp.TokenType,
			RefreshToken: tResp.RefreshToken,
			Expiry:       time.Now().Add(time.Duration(tResp.ExpiresIn) * time.Second),
		}, nil
	case "authorization_pending":
		return nil, nil
	case "slow_down":
		return nil, nil
	case "access_denied":
		return nil, fmt.Errorf("authorization denied by user")
	case "expired_token":
		return nil, fmt.Errorf("device code expired")
	default:
		return nil, fmt.Errorf("token error: %s", tResp.Error)
	}
}

func joinScopes(scopes []string) string {
	result := ""
	for i, s := range scopes {
		if i > 0 {
			result += " "
		}
		result += s
	}
	return result
}

// PersistentTokenSource wraps an oauth2.TokenSource to persist refreshed
// tokens to disk. Thread-safe for concurrent use by multiple pollers.
type PersistentTokenSource struct {
	mu         sync.Mutex
	source     oauth2.TokenSource
	tokenPath  string
	lastToken  *oauth2.Token
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
