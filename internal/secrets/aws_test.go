package secrets

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/spf13/viper"
)

// --- Mock clients ---

type mockKMSClient struct {
	// Simple in-memory encrypt/decrypt: stores ciphertext as
	// "mock-kms:" + plaintext for round-trip testing.
	encryptErr error
	decryptErr error
}

func (m *mockKMSClient) Encrypt(_ context.Context, input *kms.EncryptInput, _ ...func(*kms.Options)) (*kms.EncryptOutput, error) {
	if m.encryptErr != nil {
		return nil, m.encryptErr
	}
	// Prefix with "mock-kms:" so we can verify in decrypt.
	blob := append([]byte("mock-kms:"), input.Plaintext...)
	return &kms.EncryptOutput{CiphertextBlob: blob}, nil
}

func (m *mockKMSClient) Decrypt(_ context.Context, input *kms.DecryptInput, _ ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	if m.decryptErr != nil {
		return nil, m.decryptErr
	}
	blob := input.CiphertextBlob
	const prefix = "mock-kms:"
	if len(blob) < len(prefix) || string(blob[:len(prefix)]) != prefix {
		return nil, fmt.Errorf("invalid mock ciphertext")
	}
	return &kms.DecryptOutput{Plaintext: blob[len(prefix):]}, nil
}

// --- Detection tests ---

func TestIsKMSEncrypted(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"KMS[abc123]", true},
		{"KMS[dGVzdA==]", true},
		{"plaintext", false},
		{"", false},
		{"KMS[]", false},
		{"KMS[", false},
		{"kms[abc]", false},
		{"ENC[abc]", false},
	}
	for _, tt := range tests {
		if got := IsKMSEncrypted(tt.value); got != tt.want {
			t.Errorf("IsKMSEncrypted(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

// --- KMS tests ---

func TestKMSEncryptDecrypt_Roundtrip(t *testing.T) {
	client := &mockKMSClient{}
	ctx := context.Background()

	plaintext := "ghp_super_secret_token_123"
	encrypted, err := KMSEncrypt(ctx, client, "alias/my-key", plaintext)
	if err != nil {
		t.Fatalf("KMSEncrypt: %v", err)
	}
	if !IsKMSEncrypted(encrypted) {
		t.Fatalf("output not wrapped in KMS[...]: %q", encrypted)
	}

	decrypted, err := KMSDecrypt(ctx, client, encrypted)
	if err != nil {
		t.Fatalf("KMSDecrypt: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("roundtrip mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestKMSDecrypt_InvalidBase64(t *testing.T) {
	client := &mockKMSClient{}
	_, err := KMSDecrypt(context.Background(), client, "KMS[not-valid-base64!!!]")
	if err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestKMSDecrypt_NotKMSEncrypted(t *testing.T) {
	client := &mockKMSClient{}
	_, err := KMSDecrypt(context.Background(), client, "plaintext")
	if err == nil {
		t.Fatal("expected error for non-KMS value")
	}
}

func TestKMSDecrypt_APIError(t *testing.T) {
	client := &mockKMSClient{decryptErr: fmt.Errorf("access denied")}
	// Build a valid KMS[...] value.
	encoded := base64.StdEncoding.EncodeToString([]byte("mock-kms:test"))
	_, err := KMSDecrypt(context.Background(), client, "KMS["+encoded+"]")
	if err == nil {
		t.Fatal("expected KMS API error")
	}
}

func TestKMSEncrypt_APIError(t *testing.T) {
	client := &mockKMSClient{encryptErr: fmt.Errorf("invalid key")}
	_, err := KMSEncrypt(context.Background(), client, "bad-key", "test")
	if err == nil {
		t.Fatal("expected KMS encrypt error")
	}
}

// --- ResolveViperConfig tests ---

// TestResolveViperConfig_NoSecrets verifies no-op when no secrets are present.
func TestResolveViperConfig_NoSecrets(t *testing.T) {
	v := viper.New()
	v.Set("github.token", "ghp_plaintext")
	v.Set("poll.enabled", true)

	if err := ResolveViperConfig(v); err != nil {
		t.Fatalf("ResolveViperConfig: %v", err)
	}
	if got := v.GetString("github.token"); got != "ghp_plaintext" {
		t.Errorf("got %q, want %q", got, "ghp_plaintext")
	}
}
