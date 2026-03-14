package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a loaded skill definition from a SKILL.md file.
type Skill struct {
	Name         string   `yaml:"name" json:"name"`
	Description  string   `yaml:"description" json:"description"`
	Triggers     []string `yaml:"triggers" json:"triggers"`
	Version      string   `yaml:"version" json:"version"`
	Instructions string   `yaml:"-" json:"-"`
	Dir          string   `yaml:"-" json:"dir"`
	HasHandler   bool     `yaml:"-" json:"has_handler"`
}

// SkillInfo is the API response type for a loaded skill.
type SkillInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Triggers    []string `json:"triggers"`
	Version     string   `json:"version"`
	HasHandler  bool     `json:"has_handler"`
}

// LoadSkill reads a SKILL.md file from a directory and parses it.
func LoadSkill(dir string) (*Skill, error) {
	path := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	content := string(data)
	skill, err := parseFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("parse SKILL.md in %s: %w", dir, err)
	}

	skill.Dir = dir
	skill.HasHandler = fileExists(filepath.Join(dir, "handler.lua"))

	return skill, nil
}

// LoadSkillsDir scans a directory for skill subdirectories (each containing SKILL.md).
func LoadSkillsDir(dir string) ([]*Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read skills dir: %w", err)
	}

	var skills []*Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(dir, entry.Name())
		if !fileExists(filepath.Join(skillDir, "SKILL.md")) {
			continue
		}
		skill, err := LoadSkill(skillDir)
		if err != nil {
			return skills, fmt.Errorf("load skill %s: %w", entry.Name(), err)
		}
		skills = append(skills, skill)
	}

	return skills, nil
}

// BuildSkillsIndex returns a compact text summary of all skills for AI prompt injection.
func BuildSkillsIndex(skills []*Skill) string {
	if len(skills) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Available Skills\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "- **%s**: %s", s.Name, s.Description)
		if len(s.Triggers) > 0 {
			fmt.Fprintf(&b, " (triggers: %s)", strings.Join(s.Triggers, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// parseFrontmatter extracts YAML frontmatter delimited by --- and the remaining body.
func parseFrontmatter(content string) (*Skill, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return &Skill{Instructions: content}, nil
	}

	// Find closing ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return &Skill{Instructions: content}, nil
	}

	frontmatter := rest[:idx]
	body := strings.TrimSpace(rest[idx+4:])

	var skill Skill
	if err := yaml.Unmarshal([]byte(frontmatter), &skill); err != nil {
		return nil, fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	skill.Instructions = body

	return &skill, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
