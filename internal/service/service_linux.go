//go:build linux

package service

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

const unitTemplate = `[Unit]
Description=sekia {{.Name}} ({{.Binary}})
After=network.target

[Service]
ExecStart={{.BinPath}} --name {{.Name}}{{if .ConfigPath}} --config {{.ConfigPath}}{{end}}
Restart=always
RestartSec=5
{{- range $key, $val := .EnvVars}}
Environment={{$key}}={{$val}}
{{- end}}
StandardOutput=append:{{.LogPath}}
StandardError=append:{{.LogPath}}

[Install]
WantedBy=default.target
`

type unitData struct {
	Name       string
	Binary     string
	BinPath    string
	ConfigPath string
	LogPath    string
	EnvVars    map[string]string
}

// CreateOpts are options for creating a service.
type CreateOpts struct {
	Binary     string
	Name       string
	ConfigPath string
	EnvVars    map[string]string
}

func systemdUserDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

func unitName(name string) string {
	return "sekia-" + name + ".service"
}

// ServiceFilePath returns the systemd unit path for a named instance.
func ServiceFilePath(name string) string {
	return filepath.Join(systemdUserDir(), unitName(name))
}

// Create generates a systemd user unit for the named instance.
func Create(opts CreateOpts) error {
	binPath, err := ResolveBinaryPath(opts.Binary)
	if err != nil {
		return err
	}

	if err := EnsureLogDir(); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	unitPath := ServiceFilePath(opts.Name)
	if _, err := os.Stat(unitPath); err == nil {
		return fmt.Errorf("service %q already exists at %s", opts.Name, unitPath)
	}

	data := unitData{
		Name:       opts.Name,
		Binary:     opts.Binary,
		BinPath:    binPath,
		ConfigPath: opts.ConfigPath,
		LogPath:    LogPath(opts.Name),
		EnvVars:    opts.EnvVars,
	}

	tmpl, err := template.New("unit").Parse(unitTemplate)
	if err != nil {
		return fmt.Errorf("parse unit template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("render unit: %w", err)
	}

	if err := os.MkdirAll(systemdUserDir(), 0750); err != nil {
		return fmt.Errorf("create systemd user directory: %w", err)
	}

	if err := os.WriteFile(unitPath, buf.Bytes(), 0600); err != nil { // #nosec G306 -- may contain env vars with secrets
		return fmt.Errorf("write unit file: %w", err)
	}

	// Reload systemd to pick up the new unit.
	cmd := exec.Command("systemctl", "--user", "daemon-reload")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return nil
}

// Start enables and starts the systemd user service.
func Start(name string) error {
	unitPath := ServiceFilePath(name)
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return fmt.Errorf("service %q not found (expected %s)", name, unitPath)
	}

	svc := unitName(name)

	// Enable so it starts on boot.
	cmd := exec.Command("systemctl", "--user", "enable", svc) // #nosec G204 -- svc derived from validated name
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable: %s: %w", strings.TrimSpace(string(out)), err)
	}

	cmd = exec.Command("systemctl", "--user", "start", svc) // #nosec G204 -- svc derived from validated name
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl start: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Stop stops the systemd user service.
func Stop(name string) error {
	unitPath := ServiceFilePath(name)
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return fmt.Errorf("service %q not found (expected %s)", name, unitPath)
	}

	cmd := exec.Command("systemctl", "--user", "stop", unitName(name)) // #nosec G204 -- name validated by caller
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl stop: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Restart restarts the systemd user service.
func Restart(name string) error {
	unitPath := ServiceFilePath(name)
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return fmt.Errorf("service %q not found (expected %s)", name, unitPath)
	}

	cmd := exec.Command("systemctl", "--user", "restart", unitName(name)) // #nosec G204 -- name validated by caller
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl restart: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Remove stops, disables, and deletes the service.
func Remove(name string) error {
	unitPath := ServiceFilePath(name)
	if _, err := os.Stat(unitPath); os.IsNotExist(err) {
		return fmt.Errorf("service %q not found (expected %s)", name, unitPath)
	}

	svc := unitName(name)

	// Best-effort stop and disable.
	_ = exec.Command("systemctl", "--user", "stop", svc).Run()    // #nosec G204 -- svc derived from validated name
	_ = exec.Command("systemctl", "--user", "disable", svc).Run() // #nosec G204 -- svc derived from validated name

	if err := os.Remove(unitPath); err != nil {
		return fmt.Errorf("remove unit file: %w", err)
	}

	// Reload to forget the unit.
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()

	return nil
}

// List returns all sekia services managed by systemd user units.
func List() ([]ServiceInfo, error) {
	pattern := filepath.Join(systemdUserDir(), "sekia-*.service")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob unit files: %w", err)
	}

	var services []ServiceInfo
	for _, path := range matches {
		base := filepath.Base(path)
		// sekia-<name>.service → <name>
		name := strings.TrimPrefix(base, "sekia-")
		name = strings.TrimSuffix(name, ".service")

		info := ServiceInfo{
			Name:   name,
			Binary: parseBinaryFromUnit(path),
			Status: "stopped",
		}

		// Check status via systemctl show.
		if state, pid := systemctlStatus(unitName(name)); state == "active" {
			info.Status = "running"
			info.PID = pid
		}

		services = append(services, info)
	}

	return services, nil
}

// parseBinaryFromUnit extracts the binary name from ExecStart in a unit file.
func parseBinaryFromUnit(path string) string {
	data, err := os.ReadFile(path) // #nosec G304 -- path from filepath.Glob on known directory
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ExecStart=") {
			execStart := strings.TrimPrefix(line, "ExecStart=")
			parts := strings.Fields(execStart)
			if len(parts) > 0 {
				return filepath.Base(parts[0])
			}
		}
	}
	return "unknown"
}

// systemctlStatus queries systemd for the active state and PID of a unit.
func systemctlStatus(svc string) (string, int) {
	cmd := exec.Command("systemctl", "--user", "show", svc, "--property=ActiveState,MainPID") // #nosec G204 -- svc derived from validated name
	out, err := cmd.Output()
	if err != nil {
		return "unknown", 0
	}

	var state string
	var pid int
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "ActiveState=") {
			state = strings.TrimPrefix(line, "ActiveState=")
		}
		if strings.HasPrefix(line, "MainPID=") {
			pidStr := strings.TrimPrefix(line, "MainPID=")
			pid, _ = strconv.Atoi(pidStr)
		}
	}
	return state, pid
}
