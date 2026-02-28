// Package secrets provides age-based encryption for config values.
//
// Encrypted values use the format ENC[<base64(age-ciphertext)>] and can be
// placed inline in TOML config files. Each binary decrypts independently at
// startup using an age identity resolved from env vars, config, or a default
// key file.
package secrets

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/spf13/viper"
)

const (
	encPrefix = "ENC["
	encSuffix = "]"

	// DefaultKeyFilename is the default age identity filename.
	DefaultKeyFilename = "age.key"

	// EnvAgeKey is the env var for a raw AGE-SECRET-KEY-1... string.
	EnvAgeKey = "SEKIA_AGE_KEY"

	// EnvAgeKeyFile is the env var for a path to an age identity file.
	EnvAgeKeyFile = "SEKIA_AGE_KEY_FILE"
)

// IsEncrypted reports whether value is wrapped in ENC[...].
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, encPrefix) && strings.HasSuffix(value, encSuffix) && len(value) > len(encPrefix)+len(encSuffix)
}

// Encrypt encrypts plaintext for the given recipients and returns an ENC[...] string.
func Encrypt(plaintext string, recipients ...age.Recipient) (string, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return "", fmt.Errorf("create age encryptor: %w", err)
	}
	if _, err := io.WriteString(w, plaintext); err != nil {
		return "", fmt.Errorf("write plaintext: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("finalize encryption: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	return encPrefix + encoded + encSuffix, nil
}

// Decrypt decrypts an ENC[...] value using the provided identities.
func Decrypt(enc string, identities ...age.Identity) (string, error) {
	if !IsEncrypted(enc) {
		return "", fmt.Errorf("value is not encrypted (missing ENC[...] wrapper)")
	}
	inner := enc[len(encPrefix) : len(enc)-len(encSuffix)]
	ciphertext, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), identities...)
	if err != nil {
		return "", fmt.Errorf("age decrypt: %w", err)
	}
	plaintext, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read decrypted data: %w", err)
	}
	return string(plaintext), nil
}

// GenerateKeyPair generates a new X25519 age identity (keypair).
func GenerateKeyPair() (*age.X25519Identity, error) {
	return age.GenerateX25519Identity()
}

// LoadIdentity loads age identities from a key file.
func LoadIdentity(keyPath string) ([]age.Identity, error) {
	f, err := os.Open(keyPath)
	if err != nil {
		return nil, fmt.Errorf("open identity file: %w", err)
	}
	defer f.Close()
	identities, err := age.ParseIdentities(f)
	if err != nil {
		return nil, fmt.Errorf("parse identity file: %w", err)
	}
	return identities, nil
}

// IdentityFromString parses a raw AGE-SECRET-KEY-1... string.
func IdentityFromString(key string) (*age.X25519Identity, error) {
	return age.ParseX25519Identity(strings.TrimSpace(key))
}

// ResolveIdentity finds an age identity from env vars, config, or the default
// key file. Returns (nil, nil) if no identity is configured anywhere.
//
// Priority: SEKIA_AGE_KEY env → SEKIA_AGE_KEY_FILE env → secrets.identity
// config → ~/.config/sekia/age.key default.
func ResolveIdentity(v *viper.Viper) ([]age.Identity, error) {
	// 1. SEKIA_AGE_KEY env var (raw key string).
	if raw := os.Getenv(EnvAgeKey); raw != "" {
		id, err := IdentityFromString(raw)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", EnvAgeKey, err)
		}
		return []age.Identity{id}, nil
	}

	// 2. SEKIA_AGE_KEY_FILE env var (path to key file).
	if path := os.Getenv(EnvAgeKeyFile); path != "" {
		return LoadIdentity(path)
	}

	// 3. secrets.identity config key.
	if path := v.GetString("secrets.identity"); path != "" {
		return LoadIdentity(expandHome(path))
	}

	// 4. Default location: ~/.config/sekia/age.key.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, nil
	}
	defaultPath := filepath.Join(homeDir, ".config", "sekia", DefaultKeyFilename)
	if _, err := os.Stat(defaultPath); err != nil {
		return nil, nil
	}
	return LoadIdentity(defaultPath)
}

// DecryptViperConfig walks all Viper string keys and decrypts any ENC[...]
// values in-place. Non-string keys are safely skipped (their string
// representation never matches the ENC[ prefix).
func DecryptViperConfig(v *viper.Viper, identities []age.Identity) error {
	for _, key := range v.AllKeys() {
		val := v.GetString(key)
		if !IsEncrypted(val) {
			continue
		}
		plaintext, err := Decrypt(val, identities...)
		if err != nil {
			return fmt.Errorf("decrypt config key %q: %w", key, err)
		}
		v.Set(key, plaintext)
	}
	return nil
}

// HasEncryptedValues reports whether any Viper string value uses ENC[...].
func HasEncryptedValues(v *viper.Viper) bool {
	for _, key := range v.AllKeys() {
		if IsEncrypted(v.GetString(key)) {
			return true
		}
	}
	return false
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(homeDir, path[1:])
}
