package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestDeviceAuthFlow(t *testing.T) {
	var pollCount atomic.Int32

	// Mock device code endpoint.
	deviceMux := http.NewServeMux()
	deviceMux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(deviceCodeResponse{
			DeviceCode:      "test-device-code",
			UserCode:        "ABCD-1234",
			VerificationURL: "https://example.com/device",
			ExpiresIn:       300,
			Interval:        1,
		})
	})
	deviceSrv := httptest.NewServer(deviceMux)
	defer deviceSrv.Close()

	// Mock token endpoint: first call returns pending, second returns token.
	tokenMux := http.NewServeMux()
	tokenMux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		count := pollCount.Add(1)
		if count < 2 {
			json.NewEncoder(w).Encode(tokenResponse{Error: "authorization_pending"})
			return
		}
		json.NewEncoder(w).Encode(tokenResponse{
			AccessToken:  "test-access-token",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			RefreshToken: "test-refresh-token",
		})
	})
	tokenSrv := httptest.NewServer(tokenMux)
	defer tokenSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	token, err := deviceAuthFlowWithURLs(ctx, "test-client-id", "test-client-secret", deviceSrv.URL, tokenSrv.URL, 0)
	if err != nil {
		t.Fatalf("DeviceAuthFlow: %v", err)
	}

	if token.AccessToken != "test-access-token" {
		t.Errorf("access_token = %s, want test-access-token", token.AccessToken)
	}
	if token.RefreshToken != "test-refresh-token" {
		t.Errorf("refresh_token = %s, want test-refresh-token", token.RefreshToken)
	}
}

func TestDeviceAuthFlowDenied(t *testing.T) {
	deviceSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(deviceCodeResponse{
			DeviceCode:      "test-device-code",
			UserCode:        "ABCD-1234",
			VerificationURL: "https://example.com/device",
			ExpiresIn:       300,
			Interval:        1,
		})
	}))
	defer deviceSrv.Close()

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tokenResponse{Error: "access_denied"})
	}))
	defer tokenSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := deviceAuthFlowWithURLs(ctx, "test-client-id", "test-client-secret", deviceSrv.URL, tokenSrv.URL, 0)
	if err == nil {
		t.Fatal("expected error for denied authorization")
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
