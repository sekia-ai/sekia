package linear

import (
	"context"
	"fmt"
)

// extractString extracts a required string field from the payload.
func extractString(payload map[string]any, key string) (string, error) {
	val, ok := payload[key]
	if !ok {
		return "", fmt.Errorf("missing required field: %s", key)
	}
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return s, nil
}

func cmdCreateIssue(ctx context.Context, lc LinearClient, payload map[string]any) error {
	teamID, err := extractString(payload, "team_id")
	if err != nil {
		return err
	}
	title, err := extractString(payload, "title")
	if err != nil {
		return err
	}
	description, _ := extractString(payload, "description") // optional
	_, err = lc.CreateIssue(ctx, teamID, title, description)
	return err
}

func cmdUpdateIssue(ctx context.Context, lc LinearClient, payload map[string]any) error {
	issueID, err := extractString(payload, "issue_id")
	if err != nil {
		return err
	}
	input := make(map[string]any)
	if v, ok := payload["state_id"]; ok {
		input["stateId"] = v
	}
	if v, ok := payload["assignee_id"]; ok {
		input["assigneeId"] = v
	}
	if v, ok := payload["priority"]; ok {
		input["priority"] = v
	}
	if len(input) == 0 {
		return fmt.Errorf("update_issue requires at least one of: state_id, assignee_id, priority")
	}
	return lc.UpdateIssue(ctx, issueID, input)
}

func cmdCreateComment(ctx context.Context, lc LinearClient, payload map[string]any) error {
	issueID, err := extractString(payload, "issue_id")
	if err != nil {
		return err
	}
	body, err := extractString(payload, "body")
	if err != nil {
		return err
	}
	return lc.CreateComment(ctx, issueID, body)
}

func cmdAddLabel(ctx context.Context, lc LinearClient, payload map[string]any) error {
	issueID, err := extractString(payload, "issue_id")
	if err != nil {
		return err
	}
	labelID, err := extractString(payload, "label_id")
	if err != nil {
		return err
	}
	return lc.AddLabel(ctx, issueID, labelID)
}
