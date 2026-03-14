package skills

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog"
)

// Manager loads and manages skills from a directory.
type Manager struct {
	mu     sync.RWMutex
	skills []*Skill
	index  string
	dir    string
	logger zerolog.Logger
}

// NewManager creates a skills manager for the given directory.
func NewManager(dir string, logger zerolog.Logger) *Manager {
	return &Manager{
		dir:    dir,
		logger: logger.With().Str("component", "skills").Logger(),
	}
}

// LoadAll loads all skills from the configured directory.
func (m *Manager) LoadAll() error {
	skills, err := LoadSkillsDir(m.dir)
	if err != nil {
		return fmt.Errorf("load skills: %w", err)
	}

	index := BuildSkillsIndex(skills)

	m.mu.Lock()
	m.skills = skills
	m.index = index
	m.mu.Unlock()

	m.logger.Info().
		Int("count", len(skills)).
		Str("dir", m.dir).
		Msg("skills loaded")

	return nil
}

// ReloadAll reloads all skills from disk.
func (m *Manager) ReloadAll() error {
	return m.LoadAll()
}

// Skills returns a snapshot of all loaded skills.
func (m *Manager) Skills() []*Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Skill, len(m.skills))
	copy(result, m.skills)
	return result
}

// SkillInfos returns API-friendly skill info.
func (m *Manager) SkillInfos() []SkillInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	infos := make([]SkillInfo, len(m.skills))
	for i, s := range m.skills {
		infos[i] = SkillInfo{
			Name:        s.Name,
			Description: s.Description,
			Triggers:    s.Triggers,
			Version:     s.Version,
			HasHandler:  s.HasHandler,
		}
	}
	return infos
}

// Index returns the compact skills index for AI prompt injection.
func (m *Manager) Index() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.index
}

// FullInstructions returns the full instructions for a named skill.
func (m *Manager) FullInstructions(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.skills {
		if s.Name == name {
			return s.Instructions
		}
	}
	return ""
}

// HandlerPaths returns paths to handler.lua files for skills that have them.
func (m *Manager) HandlerPaths() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	paths := make(map[string]string)
	for _, s := range m.skills {
		if s.HasHandler {
			paths[s.Name] = filepath.Join(s.Dir, "handler.lua")
		}
	}
	return paths
}
