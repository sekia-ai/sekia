package protocol

// Command is the canonical command envelope published on sekia.commands.<agent>.
type Command struct {
	Command   string         `json:"command"`
	Payload   map[string]any `json:"payload"`
	Source    string         `json:"source"`
	Signature string         `json:"signature,omitempty"`
}
