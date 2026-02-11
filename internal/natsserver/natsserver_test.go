package natsserver

import (
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
)

func TestTokenAuth(t *testing.T) {
	token := "test-secret-token"
	dir := t.TempDir()
	logger := zerolog.Nop()

	// Start a NATS server with token auth on a random TCP port.
	srv, err := New(Config{
		StoreDir: dir,
		Host:     "127.0.0.1",
		Port:     -1, // random port
		Token:    token,
	}, logger)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Shutdown()

	url := srv.ClientURL()

	// Connection WITHOUT token should fail.
	nc, err := nats.Connect(url)
	if err == nil {
		nc.Close()
		t.Fatal("expected connection without token to fail")
	}

	// Connection with WRONG token should fail.
	nc, err = nats.Connect(url, nats.Token("wrong-token"))
	if err == nil {
		nc.Close()
		t.Fatal("expected connection with wrong token to fail")
	}

	// Connection with CORRECT token should succeed.
	nc, err = nats.Connect(url, nats.Token(token))
	if err != nil {
		t.Fatalf("expected connection with correct token to succeed: %v", err)
	}
	nc.Close()
}

func TestNoToken_AllowsAnonymous(t *testing.T) {
	dir := t.TempDir()
	logger := zerolog.Nop()

	// Start a NATS server without token auth on a random TCP port.
	srv, err := New(Config{
		StoreDir: dir,
		Host:     "127.0.0.1",
		Port:     -1,
	}, logger)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer srv.Shutdown()

	// Connection without token should succeed when no auth is configured.
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("expected anonymous connection to succeed: %v", err)
	}
	nc.Close()
}
