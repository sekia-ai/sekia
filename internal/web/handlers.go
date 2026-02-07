package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// DashboardData is the top-level template data for the dashboard page.
type DashboardData struct {
	Status    StatusData
	Agents    []protocol.AgentInfo
	Workflows []protocol.WorkflowInfo
	Events    []EventData
}

// StatusData holds system status for the template.
type StatusData struct {
	Status        string
	Uptime        string
	StartedAt     time.Time
	AgentCount    int
	WorkflowCount int
}

// EventData holds a single event for the template.
type EventData struct {
	Time    string
	Type    string
	Payload string
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := DashboardData{
		Status:    s.buildStatus(),
		Agents:    s.registry.Agents(),
		Workflows: s.buildWorkflows(),
		Events:    s.buildRecentEvents(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.templates.ExecuteTemplate(w, "layout", data)
}

func (s *Server) handlePartialStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.templates.ExecuteTemplate(w, "status", s.buildStatus())
}

func (s *Server) handlePartialAgents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.templates.ExecuteTemplate(w, "agents", s.registry.Agents())
}

func (s *Server) handlePartialWorkflows(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.templates.ExecuteTemplate(w, "workflows", s.buildWorkflows())
}

func (s *Server) buildStatus() StatusData {
	wfCount := 0
	if s.engine != nil {
		wfCount = s.engine.Count()
	}
	return StatusData{
		Status:        "ok",
		Uptime:        time.Since(s.startedAt).Truncate(time.Second).String(),
		StartedAt:     s.startedAt,
		AgentCount:    s.registry.Count(),
		WorkflowCount: wfCount,
	}
}

func (s *Server) buildWorkflows() []protocol.WorkflowInfo {
	if s.engine == nil {
		return nil
	}
	var workflows []protocol.WorkflowInfo
	for _, wf := range s.engine.Workflows() {
		workflows = append(workflows, protocol.WorkflowInfo{
			Name:     wf.Name,
			FilePath: wf.FilePath,
			Handlers: wf.Handlers,
			Patterns: wf.Patterns,
			LoadedAt: wf.LoadedAt,
			Events:   wf.Events,
			Errors:   wf.Errors,
		})
	}
	return workflows
}

func (s *Server) buildRecentEvents() []EventData {
	raw := s.eventBus.Recent()
	events := make([]EventData, 0, len(raw))
	for _, data := range raw {
		var evt protocol.Event
		if err := json.Unmarshal(data, &evt); err != nil {
			continue
		}
		payload, _ := json.Marshal(evt.Payload)
		events = append(events, EventData{
			Time:    time.Unix(evt.Timestamp, 0).Format("2006-01-02 15:04:05"),
			Type:    evt.Type,
			Payload: string(payload),
		})
	}
	return events
}
