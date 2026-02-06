package protocol

import "fmt"

// NATS subject constants and helpers.
const (
	SubjectRegistry = "sekia.registry"
)

func SubjectEvents(source string) string {
	return fmt.Sprintf("sekia.events.%s", source)
}

func SubjectCommands(agentName string) string {
	return fmt.Sprintf("sekia.commands.%s", agentName)
}

func SubjectHeartbeat(agentName string) string {
	return fmt.Sprintf("sekia.heartbeat.%s", agentName)
}
