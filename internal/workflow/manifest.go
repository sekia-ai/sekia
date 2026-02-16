package workflow

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestFilename is the name of the SHA256 manifest file in the workflow directory.
const ManifestFilename = "workflows.sha256"

// Manifest represents a SHA256 hash manifest for workflow files.
type Manifest struct {
	entries map[string]string // filename -> hex-encoded SHA256
}

// LoadManifest reads and parses a workflows.sha256 file from the given directory.
// Returns nil, nil if the file does not exist.
func LoadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, ManifestFilename)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open manifest: %w", err)
	}
	defer f.Close()

	m := &Manifest{entries: make(map[string]string)}
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// sha256sum format: "<64-hex>  <filename>" (two-space separator)
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != 64 {
			return nil, fmt.Errorf("manifest line %d: invalid format", lineNum)
		}
		hash := strings.ToLower(parts[0])
		filename := strings.TrimSpace(parts[1])
		if _, err := hex.DecodeString(hash); err != nil {
			return nil, fmt.Errorf("manifest line %d: invalid hex: %w", lineNum, err)
		}
		m.entries[filename] = hash
	}
	return m, scanner.Err()
}

// Verify checks that the file at filePath matches the expected hash in the manifest.
func (m *Manifest) Verify(filename, filePath string) error {
	expected, ok := m.entries[filename]
	if !ok {
		return fmt.Errorf("file %q not in manifest", filename)
	}
	actual, err := HashFile(filePath)
	if err != nil {
		return fmt.Errorf("hash %s: %w", filename, err)
	}
	if actual != expected {
		return fmt.Errorf("hash mismatch for %s: expected %s, got %s", filename, expected, actual)
	}
	return nil
}

// Count returns the number of entries in the manifest.
func (m *Manifest) Count() int {
	return len(m.entries)
}

// HashFile computes the SHA256 hash of a file and returns the lowercase hex string.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// GenerateManifest scans a directory for .lua files and produces a manifest with their SHA256 hashes.
func GenerateManifest(dir string) (*Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	m := &Manifest{entries: make(map[string]string)}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lua") {
			continue
		}
		hash, err := HashFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("hash %s: %w", entry.Name(), err)
		}
		m.entries[entry.Name()] = hash
	}
	return m, nil
}

// WriteTo writes the manifest in sha256sum-compatible format.
func (m *Manifest) WriteTo(w io.Writer) (int64, error) {
	names := make([]string, 0, len(m.entries))
	for name := range m.entries {
		names = append(names, name)
	}
	sort.Strings(names)

	var total int64
	for _, name := range names {
		n, err := fmt.Fprintf(w, "%s  %s\n", m.entries[name], name)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// WriteFile writes the manifest to the standard location in the given directory.
func (m *Manifest) WriteFile(dir string) error {
	path := filepath.Join(dir, ManifestFilename)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = m.WriteTo(f)
	return err
}
