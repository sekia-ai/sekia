package protocol

import "time"

// Heartbeat is published on sekia.heartbeat.<agent-name> every 30s.
type Heartbeat struct {
	Name            string    `json:"name"`
	Status          string    `json:"status"`
	LastEvent       time.Time `json:"last_event"`
	EventsProcessed int64    `json:"events_processed"`
	Errors          int64    `json:"errors"`
}
