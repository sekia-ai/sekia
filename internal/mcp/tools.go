package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

func (s *MCPServer) handleGetStatus(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	status, err := s.api.GetStatus(ctx)
	if err != nil {
		return textError("failed to get status: " + err.Error()), nil
	}
	return textJSON(status)
}

func (s *MCPServer) handleListAgents(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agents, err := s.api.GetAgents(ctx)
	if err != nil {
		return textError("failed to list agents: " + err.Error()), nil
	}
	return textJSON(agents.Agents)
}

func (s *MCPServer) handleListWorkflows(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	workflows, err := s.api.GetWorkflows(ctx)
	if err != nil {
		return textError("failed to list workflows: " + err.Error()), nil
	}
	return textJSON(workflows.Workflows)
}

func (s *MCPServer) handleReloadWorkflows(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	if err := s.api.ReloadWorkflows(ctx); err != nil {
		return textError("failed to reload workflows: " + err.Error()), nil
	}
	return textResult(`{"status":"reloaded"}`), nil
}

func (s *MCPServer) handlePublishEvent(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	source, err := req.RequireString("source")
	if err != nil {
		return textError("missing required parameter: source"), nil
	}
	eventType, err := req.RequireString("event_type")
	if err != nil {
		return textError("missing required parameter: event_type"), nil
	}

	var payload map[string]any
	args := req.GetArguments()
	if raw, ok := args["payload"]; ok && raw != nil {
		if m, ok := raw.(map[string]any); ok {
			payload = m
		}
	}
	if payload == nil {
		payload = map[string]any{}
	}

	ev := protocol.NewEvent(eventType, "mcp:"+source, payload)
	data, err := json.Marshal(ev)
	if err != nil {
		return textError("failed to marshal event: " + err.Error()), nil
	}

	subject := protocol.SubjectEvents(source)
	if err := s.nc.Publish(subject, data); err != nil {
		return textError("failed to publish event: " + err.Error()), nil
	}
	s.nc.Flush()

	return textResult(fmt.Sprintf(`{"status":"published","event_id":"%s","subject":"%s"}`, ev.ID, subject)), nil
}

func (s *MCPServer) handleSendCommand(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	agentName, err := req.RequireString("agent")
	if err != nil {
		return textError("missing required parameter: agent"), nil
	}
	command, err := req.RequireString("command")
	if err != nil {
		return textError("missing required parameter: command"), nil
	}

	var payload map[string]any
	args := req.GetArguments()
	if raw, ok := args["payload"]; ok && raw != nil {
		if m, ok := raw.(map[string]any); ok {
			payload = m
		}
	}
	if payload == nil {
		return textError("missing required parameter: payload"), nil
	}

	cmdMsg := map[string]any{
		"command": command,
		"payload": payload,
		"source":  "mcp",
	}
	data, err := json.Marshal(cmdMsg)
	if err != nil {
		return textError("failed to marshal command: " + err.Error()), nil
	}

	subject := protocol.SubjectCommands(agentName)
	if err := s.nc.Publish(subject, data); err != nil {
		return textError("failed to send command: " + err.Error()), nil
	}
	s.nc.Flush()

	return textResult(fmt.Sprintf(`{"status":"sent","agent":"%s","command":"%s"}`, agentName, command)), nil
}

// textResult returns a successful text result.
func textResult(text string) *mcplib.CallToolResult {
	return &mcplib.CallToolResult{
		Content: []mcplib.Content{
			mcplib.TextContent{Type: "text", Text: text},
		},
	}
}

// textError returns an error text result.
func textError(msg string) *mcplib.CallToolResult {
	return &mcplib.CallToolResult{
		Content: []mcplib.Content{
			mcplib.TextContent{Type: "text", Text: msg},
		},
		IsError: true,
	}
}

// textJSON marshals v to indented JSON and returns it as a text result.
func textJSON(v any) (*mcplib.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return textError("failed to marshal response: " + err.Error()), nil
	}
	return textResult(string(data)), nil
}
