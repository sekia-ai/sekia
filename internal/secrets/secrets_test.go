package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/spf13/viper"
)

func TestIsEncrypted(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"ENC[abc123]", true},
		{"ENC[YWdlLWVuY3J5cHRpb24=]", true},
		{"plaintext", false},
		{"", false},
		{"ENC[]", false},   // empty payload
		{"ENC[", false},    // no suffix
		{"]", false},       // no prefix
		{"enc[abc]", false}, // wrong case
		{"ENC[abc", false},  // missing ]
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := IsEncrypted(tt.value); got != tt.want {
				t.Errorf("IsEncrypted(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	plaintext := "ghp_super_secret_token_123"
	encrypted, err := Encrypt(plaintext, identity.Recipient())
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if !IsEncrypted(encrypted) {
		t.Fatalf("Encrypt output is not wrapped in ENC[...]: %q", encrypted)
	}

	decrypted, err := Decrypt(encrypted, identity)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("roundtrip mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptDecrypt_EmptyString(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	encrypted, err := Encrypt("", identity.Recipient())
	if err != nil {
		t.Fatalf("Encrypt empty string: %v", err)
	}

	decrypted, err := Decrypt(encrypted, identity)
	if err != nil {
		t.Fatalf("Decrypt empty string: %v", err)
	}

	if decrypted != "" {
		t.Errorf("expected empty string, got %q", decrypted)
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	identity1, _ := age.GenerateX25519Identity()
	identity2, _ := age.GenerateX25519Identity()

	encrypted, err := Encrypt("secret", identity1.Recipient())
	if err != nil {
		t.Fatal(err)
	}

	_, err = Decrypt(encrypted, identity2)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestDecrypt_InvalidBase64(t *testing.T) {
	_, err := Decrypt("ENC[not-valid-base64!!!]", nil)
	if err == nil {
		t.Fatal("expected base64 decode error")
	}
}

func TestDecrypt_NotEncrypted(t *testing.T) {
	_, err := Decrypt("plaintext", nil)
	if err == nil {
		t.Fatal("expected error for non-encrypted value")
	}
}

func TestDecrypt_CorruptCiphertext(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	// Valid base64, but not valid age ciphertext.
	_, err := Decrypt("ENC[dGVzdA==]", identity)
	if err == nil {
		t.Fatal("expected error for corrupt ciphertext")
	}
}

func TestLoadIdentity(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "test.key")

	content := "# test key\n" + identity.String() + "\n"
	if err := os.WriteFile(keyPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	ids, err := LoadIdentity(keyPath)
	if err != nil {
		t.Fatalf("LoadIdentity: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 identity, got %d", len(ids))
	}

	// Verify it can decrypt.
	encrypted, _ := Encrypt("test", identity.Recipient())
	decrypted, err := Decrypt(encrypted, ids...)
	if err != nil {
		t.Fatalf("decrypt with loaded identity: %v", err)
	}
	if decrypted != "test" {
		t.Errorf("got %q, want %q", decrypted, "test")
	}
}

func TestLoadIdentity_NotExist(t *testing.T) {
	_, err := LoadIdentity("/nonexistent/path/age.key")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestIdentityFromString(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	parsed, err := IdentityFromString(identity.String())
	if err != nil {
		t.Fatalf("IdentityFromString: %v", err)
	}

	// Verify it can decrypt.
	encrypted, _ := Encrypt("test", identity.Recipient())
	decrypted, err := Decrypt(encrypted, parsed)
	if err != nil {
		t.Fatalf("decrypt with parsed identity: %v", err)
	}
	if decrypted != "test" {
		t.Errorf("got %q, want %q", decrypted, "test")
	}
}

func TestResolveIdentity_EnvVar(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	t.Setenv(EnvAgeKey, identity.String())
	t.Setenv(EnvAgeKeyFile, "")

	v := viper.New()
	ids, err := ResolveIdentity(v)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if ids == nil {
		t.Fatal("expected identities, got nil")
	}

	encrypted, _ := Encrypt("test", identity.Recipient())
	decrypted, err := Decrypt(encrypted, ids...)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decrypted != "test" {
		t.Errorf("got %q, want %q", decrypted, "test")
	}
}

func TestResolveIdentity_EnvFile(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age.key")
	os.WriteFile(keyPath, []byte(identity.String()+"\n"), 0600)

	t.Setenv(EnvAgeKey, "")
	t.Setenv(EnvAgeKeyFile, keyPath)

	v := viper.New()
	ids, err := ResolveIdentity(v)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if ids == nil {
		t.Fatal("expected identities, got nil")
	}
}

func TestResolveIdentity_ConfigKey(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age.key")
	os.WriteFile(keyPath, []byte(identity.String()+"\n"), 0600)

	t.Setenv(EnvAgeKey, "")
	t.Setenv(EnvAgeKeyFile, "")

	v := viper.New()
	v.Set("secrets.identity", keyPath)
	ids, err := ResolveIdentity(v)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if ids == nil {
		t.Fatal("expected identities, got nil")
	}
}

func TestResolveIdentity_DefaultFile(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv(EnvAgeKey, "")
	t.Setenv(EnvAgeKeyFile, "")

	// Create the default key file location.
	sekiaDir := filepath.Join(tmpHome, ".config", "sekia")
	os.MkdirAll(sekiaDir, 0700)
	os.WriteFile(filepath.Join(sekiaDir, DefaultKeyFilename), []byte(identity.String()+"\n"), 0600)

	v := viper.New()
	ids, err := ResolveIdentity(v)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if ids == nil {
		t.Fatal("expected identities from default file, got nil")
	}
}

func TestResolveIdentity_None(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv(EnvAgeKey, "")
	t.Setenv(EnvAgeKeyFile, "")

	v := viper.New()
	ids, err := ResolveIdentity(v)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if ids != nil {
		t.Fatalf("expected nil identities, got %v", ids)
	}
}

func TestDecryptViperConfig(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()

	enc1, _ := Encrypt("secret_token", identity.Recipient())
	enc2, _ := Encrypt("webhook_secret", identity.Recipient())

	v := viper.New()
	v.Set("github.token", enc1)
	v.Set("webhook.secret", enc2)
	v.Set("webhook.listen", ":8080") // plaintext, should be untouched
	v.Set("poll.enabled", true)       // non-string, should be skipped

	if err := DecryptViperConfig(v, []age.Identity{identity}); err != nil {
		t.Fatalf("DecryptViperConfig: %v", err)
	}

	if got := v.GetString("github.token"); got != "secret_token" {
		t.Errorf("github.token = %q, want %q", got, "secret_token")
	}
	if got := v.GetString("webhook.secret"); got != "webhook_secret" {
		t.Errorf("webhook.secret = %q, want %q", got, "webhook_secret")
	}
	if got := v.GetString("webhook.listen"); got != ":8080" {
		t.Errorf("webhook.listen = %q, want %q", got, ":8080")
	}
	if got := v.GetBool("poll.enabled"); !got {
		t.Error("poll.enabled should still be true")
	}
}

func TestDecryptViperConfig_NoEncValues(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()

	v := viper.New()
	v.Set("github.token", "ghp_plaintext")
	v.Set("poll.enabled", true)

	if err := DecryptViperConfig(v, []age.Identity{identity}); err != nil {
		t.Fatalf("DecryptViperConfig: %v", err)
	}

	if got := v.GetString("github.token"); got != "ghp_plaintext" {
		t.Errorf("github.token = %q, want %q", got, "ghp_plaintext")
	}
}

func TestDecryptViperConfig_DecryptError(t *testing.T) {
	identity1, _ := age.GenerateX25519Identity()
	identity2, _ := age.GenerateX25519Identity()

	enc, _ := Encrypt("secret", identity1.Recipient())

	v := viper.New()
	v.Set("github.token", enc)

	err := DecryptViperConfig(v, []age.Identity{identity2})
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestHasEncryptedValues(t *testing.T) {
	identity, _ := age.GenerateX25519Identity()
	enc, _ := Encrypt("secret", identity.Recipient())

	t.Run("has encrypted", func(t *testing.T) {
		v := viper.New()
		v.Set("token", enc)
		if !HasEncryptedValues(v) {
			t.Error("expected true")
		}
	})

	t.Run("no encrypted", func(t *testing.T) {
		v := viper.New()
		v.Set("token", "plaintext")
		v.Set("enabled", true)
		if HasEncryptedValues(v) {
			t.Error("expected false")
		}
	})
}

func TestGenerateKeyPair(t *testing.T) {
	id, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if id == nil {
		t.Fatal("expected non-nil identity")
	}
	if id.Recipient() == nil {
		t.Fatal("expected non-nil recipient")
	}

	// Verify the keypair works for encrypt/decrypt.
	encrypted, err := Encrypt("test", id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := Decrypt(encrypted, id)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != "test" {
		t.Errorf("got %q, want %q", decrypted, "test")
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~/foo/bar", filepath.Join(home, "foo/bar")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
		{"~", home},
	}
	for _, tt := range tests {
		got := expandHome(tt.input)
		if got != tt.want {
			t.Errorf("expandHome(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
