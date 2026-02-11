package protocol

import (
	"testing"
)

func TestSignAndVerify(t *testing.T) {
	cmd := &Command{
		Command: "add_label",
		Payload: map[string]any{"owner": "acme", "repo": "app", "number": float64(42), "label": "bug"},
		Source:  "workflow:auto-labeler",
	}
	secret := "test-secret-key"

	if err := SignCommand(cmd, secret); err != nil {
		t.Fatalf("SignCommand: %v", err)
	}
	if cmd.Signature == "" {
		t.Fatal("expected non-empty signature")
	}
	if !VerifyCommand(cmd, secret) {
		t.Fatal("VerifyCommand returned false for valid signature")
	}
}

func TestVerifyTamperedPayload(t *testing.T) {
	cmd := &Command{
		Command: "add_label",
		Payload: map[string]any{"label": "bug"},
		Source:  "workflow:test",
	}
	secret := "my-secret"

	if err := SignCommand(cmd, secret); err != nil {
		t.Fatalf("SignCommand: %v", err)
	}

	cmd.Payload["label"] = "critical"

	if VerifyCommand(cmd, secret) {
		t.Fatal("VerifyCommand returned true for tampered payload")
	}
}

func TestVerifyTamperedCommand(t *testing.T) {
	cmd := &Command{
		Command: "add_label",
		Payload: map[string]any{"label": "bug"},
		Source:  "workflow:test",
	}
	secret := "my-secret"

	if err := SignCommand(cmd, secret); err != nil {
		t.Fatalf("SignCommand: %v", err)
	}

	cmd.Command = "close_issue"

	if VerifyCommand(cmd, secret) {
		t.Fatal("VerifyCommand returned true for tampered command name")
	}
}

func TestVerifyTamperedSource(t *testing.T) {
	cmd := &Command{
		Command: "add_label",
		Payload: map[string]any{"label": "bug"},
		Source:  "workflow:legit",
	}
	secret := "my-secret"

	if err := SignCommand(cmd, secret); err != nil {
		t.Fatalf("SignCommand: %v", err)
	}

	cmd.Source = "workflow:evil"

	if VerifyCommand(cmd, secret) {
		t.Fatal("VerifyCommand returned true for tampered source")
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	cmd := &Command{
		Command: "send_message",
		Payload: map[string]any{"channel": "general"},
		Source:  "mcp",
	}

	if err := SignCommand(cmd, "secret-a"); err != nil {
		t.Fatalf("SignCommand: %v", err)
	}

	if VerifyCommand(cmd, "secret-b") {
		t.Fatal("VerifyCommand returned true for wrong secret")
	}
}

func TestEmptySecretSkipsSigning(t *testing.T) {
	cmd := &Command{
		Command: "add_label",
		Payload: map[string]any{"label": "bug"},
		Source:  "workflow:test",
	}

	if err := SignCommand(cmd, ""); err != nil {
		t.Fatalf("SignCommand: %v", err)
	}
	if cmd.Signature != "" {
		t.Fatalf("expected empty signature, got %q", cmd.Signature)
	}
}

func TestEmptySecretSkipsVerification(t *testing.T) {
	cmd := &Command{
		Command: "add_label",
		Payload: map[string]any{"label": "bug"},
		Source:  "workflow:test",
	}

	if !VerifyCommand(cmd, "") {
		t.Fatal("VerifyCommand with empty secret should return true")
	}
}

func TestSecretConfiguredNoSignature(t *testing.T) {
	cmd := &Command{
		Command: "add_label",
		Payload: map[string]any{"label": "bug"},
		Source:  "workflow:test",
	}

	if VerifyCommand(cmd, "my-secret") {
		t.Fatal("VerifyCommand should return false when secret configured but no signature present")
	}
}

func TestDeterministicSignature(t *testing.T) {
	secret := "deterministic-test"

	cmd1 := &Command{
		Command: "create_comment",
		Payload: map[string]any{"body": "hello", "number": float64(1)},
		Source:  "workflow:reviewer",
	}
	cmd2 := &Command{
		Command: "create_comment",
		Payload: map[string]any{"body": "hello", "number": float64(1)},
		Source:  "workflow:reviewer",
	}

	if err := SignCommand(cmd1, secret); err != nil {
		t.Fatalf("SignCommand cmd1: %v", err)
	}
	if err := SignCommand(cmd2, secret); err != nil {
		t.Fatalf("SignCommand cmd2: %v", err)
	}

	if cmd1.Signature != cmd2.Signature {
		t.Fatalf("signatures differ: %s vs %s", cmd1.Signature, cmd2.Signature)
	}
}
