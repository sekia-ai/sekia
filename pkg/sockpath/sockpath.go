// Package sockpath provides the default Unix socket path for the sekiad daemon.
// sekiad and sekiactl use it to agree on the default.
package sockpath

import (
	"os"
	"path/filepath"
	"strconv"
)

// DefaultSocketPath returns the default path for the sekiad Unix socket.
//
// It prefers $XDG_RUNTIME_DIR/sekia/sekiad.sock. When the variable is unset but
// the user's runtime directory /run/user/<uid> exists (typical for a process
// started by systemd whose SSH sessions do not export XDG_RUNTIME_DIR), that
// directory is used so sekiactl finds the daemon's socket. Otherwise it falls
// back to ~/.config/sekia/sekiad.sock.
func DefaultSocketPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "sekia", "sekiad.sock")
	}
	if uid := os.Getuid(); uid >= 0 {
		runDir := filepath.Join("/run/user", strconv.Itoa(uid))
		if fi, err := os.Stat(runDir); err == nil && fi.IsDir() {
			return filepath.Join(runDir, "sekia", "sekiad.sock")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sekia", "sekiad.sock")
}
