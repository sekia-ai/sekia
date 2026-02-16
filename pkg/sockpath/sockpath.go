// Package sockpath provides the default Unix socket path for the sekiad daemon.
// All binaries (sekiad, sekiactl, sekia-mcp) use this to agree on the default.
package sockpath

import (
	"os"
	"path/filepath"
)

// DefaultSocketPath returns the default path for the sekiad Unix socket.
// It prefers $XDG_RUNTIME_DIR/sekia/sekiad.sock (standard on Linux, tmpfs-backed),
// falling back to ~/.config/sekia/sekiad.sock.
func DefaultSocketPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "sekia", "sekiad.sock")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sekia", "sekiad.sock")
}
