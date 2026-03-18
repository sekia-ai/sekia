package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// ServiceInfo represents a managed sekia service instance.
type ServiceInfo struct {
	Name   string // instance name (e.g., "github-work")
	Binary string // binary name (e.g., "sekia-github")
	Status string // "running", "stopped", "unknown"
	PID    int    // process ID if running, 0 otherwise
}

// validBinaries is the allowlist of agent binaries that can be managed as services.
var validBinaries = map[string]bool{
	"sekiad":        true,
	"sekia-github":  true,
	"sekia-slack":   true,
	"sekia-linear":  true,
	"sekia-google":  true,
}

// nameRegex allows alphanumeric characters and hyphens.
var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

// ValidateBinary checks that the binary is a known sekia agent.
func ValidateBinary(binary string) error {
	if !validBinaries[binary] {
		return fmt.Errorf("unknown binary %q; valid binaries: sekiad, sekia-github, sekia-slack, sekia-linear, sekia-google", binary)
	}
	return nil
}

// ValidateName checks that the instance name is well-formed.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("instance name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("instance name must be 64 characters or fewer")
	}
	if !nameRegex.MatchString(name) {
		return fmt.Errorf("instance name must contain only alphanumeric characters and hyphens, and not start or end with a hyphen")
	}
	return nil
}

// ResolveBinaryPath finds the absolute path to the binary.
func ResolveBinaryPath(binary string) (string, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("binary %q not found in PATH: %w", binary, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path for %q: %w", binary, err)
	}
	return abs, nil
}

// LogDir returns the directory for service log files.
func LogDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sekia", "logs")
}

// LogPath returns the log file path for a named instance.
func LogPath(name string) string {
	return filepath.Join(LogDir(), name+".log")
}

// EnsureLogDir creates the log directory if it doesn't exist.
func EnsureLogDir() error {
	return os.MkdirAll(LogDir(), 0750)
}
