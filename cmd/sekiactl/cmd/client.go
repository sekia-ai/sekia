package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

// apiClient returns an http.Client that connects over the Unix socket.
func apiClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}
}

// apiGet performs a GET and decodes the JSON response.
func apiGet(path string, dest any) error {
	resp, err := apiClient().Get("http://sekiad" + path)
	if err != nil {
		return fmt.Errorf("cannot connect to sekiad at %s: %w", socketPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sekiad returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

// apiPost performs a POST and decodes the JSON response.
func apiPost(path string, dest any) error {
	resp, err := apiClient().Post("http://sekiad"+path, "application/json", nil)
	if err != nil {
		return fmt.Errorf("cannot connect to sekiad at %s: %w", socketPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sekiad returned HTTP %d", resp.StatusCode)
	}
	if dest != nil {
		return json.NewDecoder(resp.Body).Decode(dest)
	}
	return nil
}
