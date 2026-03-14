package ai

import (
	"fmt"
	"os"
	"strings"
)

// LoadPersona reads a persona file and returns its content as a string.
// If the file does not exist, it returns an empty string and no error.
func LoadPersona(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read persona file: %w", err)
	}

	content := strings.TrimSpace(string(data))
	return content, nil
}
