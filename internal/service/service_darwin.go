//go:build darwin

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

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.BinPath}}</string>
		<string>--name</string>
		<string>{{.Name}}</string>
{{- if .ConfigPath}}
		<string>--config</string>
		<string>{{.ConfigPath}}</string>
{{- end}}
	</array>
{{- if .EnvVars}}
	<key>EnvironmentVariables</key>
	<dict>
{{- range $key, $val := .EnvVars}}
		<key>{{$key}}</key>
		<string>{{$val}}</string>
{{- end}}
	</dict>
{{- end}}
	<key>KeepAlive</key>
	<true/>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogPath}}</string>
</dict>
</plist>
`

type plistData struct {
	Label      string
	Name       string
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

func launchAgentsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

func label(name string) string {
	return "com.sekia." + name
}

// ServiceFilePath returns the plist path for a named instance.
func ServiceFilePath(name string) string {
	return filepath.Join(launchAgentsDir(), label(name)+".plist")
}

// Create generates a launchd plist for the named instance.
func Create(opts CreateOpts) error {
	binPath, err := ResolveBinaryPath(opts.Binary)
	if err != nil {
		return err
	}

	if err := EnsureLogDir(); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	plistPath := ServiceFilePath(opts.Name)
	if _, err := os.Stat(plistPath); err == nil {
		return fmt.Errorf("service %q already exists at %s", opts.Name, plistPath)
	}

	data := plistData{
		Label:      label(opts.Name),
		Name:       opts.Name,
		BinPath:    binPath,
		ConfigPath: opts.ConfigPath,
		LogPath:    LogPath(opts.Name),
		EnvVars:    opts.EnvVars,
	}

	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return fmt.Errorf("parse plist template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("render plist: %w", err)
	}

	if err := os.MkdirAll(launchAgentsDir(), 0755); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}

	if err := os.WriteFile(plistPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	return nil
}

// Start loads the launchd plist.
func Start(name string) error {
	plistPath := ServiceFilePath(name)
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return fmt.Errorf("service %q not found (expected %s)", name, plistPath)
	}

	cmd := exec.Command("launchctl", "load", "-w", plistPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Stop unloads the launchd plist.
func Stop(name string) error {
	plistPath := ServiceFilePath(name)
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return fmt.Errorf("service %q not found (expected %s)", name, plistPath)
	}

	cmd := exec.Command("launchctl", "unload", plistPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl unload: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Restart stops then starts the service.
func Restart(name string) error {
	// Stop ignoring errors (may not be loaded).
	_ = Stop(name)
	return Start(name)
}

// Remove stops and deletes the service.
func Remove(name string) error {
	plistPath := ServiceFilePath(name)
	if _, err := os.Stat(plistPath); os.IsNotExist(err) {
		return fmt.Errorf("service %q not found (expected %s)", name, plistPath)
	}

	// Best-effort stop.
	_ = Stop(name)

	if err := os.Remove(plistPath); err != nil {
		return fmt.Errorf("remove plist: %w", err)
	}
	return nil
}

// List returns all sekia services managed by launchd.
func List() ([]ServiceInfo, error) {
	pattern := filepath.Join(launchAgentsDir(), "com.sekia.*.plist")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob plist files: %w", err)
	}

	var services []ServiceInfo
	for _, path := range matches {
		base := filepath.Base(path)
		// com.sekia.<name>.plist → <name>
		name := strings.TrimPrefix(base, "com.sekia.")
		name = strings.TrimSuffix(name, ".plist")

		info := ServiceInfo{
			Name:   name,
			Binary: parseBinaryFromPlist(path),
			Status: "stopped",
		}

		// Check if running via launchctl list.
		if pid, running := launchctlStatus(label(name)); running {
			info.Status = "running"
			info.PID = pid
		}

		services = append(services, info)
	}

	return services, nil
}

// parseBinaryFromPlist extracts the binary name from ProgramArguments in a plist.
func parseBinaryFromPlist(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	content := string(data)
	// Find the first <string> after <key>ProgramArguments</key><array>
	idx := strings.Index(content, "<key>ProgramArguments</key>")
	if idx < 0 {
		return "unknown"
	}
	rest := content[idx:]
	// Find first <string>...</string> in the array.
	start := strings.Index(rest, "<string>")
	if start < 0 {
		return "unknown"
	}
	rest = rest[start+len("<string>"):]
	end := strings.Index(rest, "</string>")
	if end < 0 {
		return "unknown"
	}
	binPath := rest[:end]
	return filepath.Base(binPath)
}

// launchctlStatus checks if a launchd job is loaded and returns its PID.
func launchctlStatus(lbl string) (int, bool) {
	cmd := exec.Command("launchctl", "list", lbl)
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}

	// Output format (tab-separated):
	// "PID"	"Status"	"Label"
	// or the first line is headers, then data.
	// Actually, `launchctl list <label>` outputs key-value pairs.
	// But checking for PID in output:
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "\"PID\"") || strings.HasPrefix(line, "\"pid\"") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				pidStr := strings.TrimSpace(parts[1])
				pidStr = strings.Trim(pidStr, ";")
				pidStr = strings.TrimSpace(pidStr)
				if pid, err := strconv.Atoi(pidStr); err == nil && pid > 0 {
					return pid, true
				}
			}
		}
	}

	// Fallback: if launchctl list succeeded, the job is at least loaded.
	// Try `launchctl list` (no args) and grep for the label.
	cmdAll := exec.Command("launchctl", "list")
	outAll, err := cmdAll.Output()
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(outAll), "\n") {
		if strings.Contains(line, lbl) {
			fields := strings.Fields(line)
			if len(fields) >= 1 && fields[0] != "-" {
				if pid, err := strconv.Atoi(fields[0]); err == nil && pid > 0 {
					return pid, true
				}
			}
			// Label found but PID is "-" means loaded but not running.
			return 0, false
		}
	}

	return 0, false
}
