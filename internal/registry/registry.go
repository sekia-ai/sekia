package registry

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// agentState holds the combined registration + last heartbeat data.
type agentState struct {
	Registration  protocol.Registration
	RegisteredAt  time.Time
	LastHeartbeat protocol.Heartbeat
	LastSeen      time.Time
}

// Registry tracks connected agents.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]*agentState
	nc     *nats.Conn
	logger zerolog.Logger
	subs   []*nats.Subscription
}

// New creates a Registry and subscribes to NATS subjects.
func New(nc *nats.Conn, logger zerolog.Logger) (*Registry, error) {
	r := &Registry{
		agents: make(map[string]*agentState),
		nc:     nc,
		logger: logger.With().Str("component", "registry").Logger(),
	}

	regSub, err := nc.Subscribe(protocol.SubjectRegistry, r.handleRegistration)
	if err != nil {
		return nil, err
	}
	hbSub, err := nc.Subscribe("sekia.heartbeat.>", r.handleHeartbeat)
	if err != nil {
		regSub.Unsubscribe()
		return nil, err
	}
	r.subs = []*nats.Subscription{regSub, hbSub}

	r.logger.Info().Msg("agent registry started")
	return r, nil
}

func (r *Registry) handleRegistration(msg *nats.Msg) {
	var reg protocol.Registration
	if err := json.Unmarshal(msg.Data, &reg); err != nil {
		r.logger.Error().Err(err).Msg("bad registration message")
		return
	}
	r.mu.Lock()
	if existing, ok := r.agents[reg.Name]; ok {
		existing.Registration = reg
		existing.LastSeen = time.Now()
	} else {
		r.agents[reg.Name] = &agentState{
			Registration: reg,
			RegisteredAt: time.Now(),
			LastSeen:     time.Now(),
		}
	}
	r.mu.Unlock()
	r.logger.Info().Str("agent", reg.Name).Str("version", reg.Version).Msg("agent registered")
}

func (r *Registry) handleHeartbeat(msg *nats.Msg) {
	var hb protocol.Heartbeat
	if err := json.Unmarshal(msg.Data, &hb); err != nil {
		r.logger.Error().Err(err).Msg("bad heartbeat message")
		return
	}
	r.mu.Lock()
	if state, ok := r.agents[hb.Name]; ok {
		state.LastHeartbeat = hb
		state.LastSeen = time.Now()
	} else {
		r.agents[hb.Name] = &agentState{
			Registration:  protocol.Registration{Name: hb.Name},
			RegisteredAt:  time.Now(),
			LastHeartbeat: hb,
			LastSeen:      time.Now(),
		}
	}
	r.mu.Unlock()
}

// Agents returns a snapshot of all known agents.
func (r *Registry) Agents() []protocol.AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]protocol.AgentInfo, 0, len(r.agents))
	for _, s := range r.agents {
		status := "unknown"
		if s.LastHeartbeat.Status != "" {
			status = s.LastHeartbeat.Status
		}
		result = append(result, protocol.AgentInfo{
			Name:            s.Registration.Name,
			Version:         s.Registration.Version,
			Status:          status,
			Capabilities:    s.Registration.Capabilities,
			Commands:        s.Registration.Commands,
			RegisteredAt:    s.RegisteredAt,
			LastHeartbeat:   s.LastSeen,
			EventsProcessed: s.LastHeartbeat.EventsProcessed,
			Errors:          s.LastHeartbeat.Errors,
		})
	}
	return result
}

// Count returns the number of known agents.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

// Close unsubscribes from NATS.
func (r *Registry) Close() {
	for _, sub := range r.subs {
		sub.Unsubscribe()
	}
}
