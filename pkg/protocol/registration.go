package protocol

// Registration is published on sekia.registry when an agent starts.
type Registration struct {
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	Capabilities []string       `json:"capabilities"`
	Commands     []string       `json:"commands"`
	ConfigSchema map[string]any `json:"config_schema,omitempty"`
}
