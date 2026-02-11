package protocol

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// signingPayload is the subset of Command fields that are signed.
// A dedicated struct ensures deterministic JSON marshal order.
type signingPayload struct {
	Command string         `json:"command"`
	Payload map[string]any `json:"payload"`
	Source  string         `json:"source"`
}

// SignCommand computes an HMAC-SHA256 signature for the command and sets cmd.Signature.
// If secret is empty, the command is left unsigned.
func SignCommand(cmd *Command, secret string) error {
	if secret == "" {
		return nil
	}
	canonical, err := json.Marshal(signingPayload{
		Command: cmd.Command,
		Payload: cmd.Payload,
		Source:  cmd.Source,
	})
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(canonical)
	cmd.Signature = hex.EncodeToString(mac.Sum(nil))
	return nil
}

// VerifyCommand checks the HMAC-SHA256 signature on a command.
// If secret is empty, verification is skipped (returns true).
// If the command has no signature but a secret is configured, returns false.
func VerifyCommand(cmd *Command, secret string) bool {
	if secret == "" {
		return true
	}
	if cmd.Signature == "" {
		return false
	}
	canonical, err := json.Marshal(signingPayload{
		Command: cmd.Command,
		Payload: cmd.Payload,
		Source:  cmd.Source,
	})
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(canonical)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(cmd.Signature))
}
