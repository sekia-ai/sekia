package ai

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LoadPersona reads a persona file and returns its content as a string.
// If the file does not exist, it returns an empty string and no error.
func LoadPersona(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	base := filepath.Base(cleanPath)

	root, err := os.OpenRoot(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("open persona directory: %w", err)
	}
	defer root.Close()

	f, err := root.Open(base)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read persona file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read persona file: %w", err)
	}

	content := strings.TrimSpace(string(data))
	return content, nil
}
