package linear

import (
	"time"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// completedStates are Linear state names that indicate completion.
var completedStates = map[string]bool{
	"Done":      true,
	"Completed": true,
	"Canceled":  true,
	"Cancelled": true,
}

// MapIssueEvent converts a Linear issue into a sekia Event.
// lastSyncTime is used to determine if the issue was created or updated.
func MapIssueEvent(issue LinearIssue, lastSyncTime time.Time) protocol.Event {
	var sekiaType string

	if issue.CreatedAt.After(lastSyncTime) {
		sekiaType = "linear.issue.created"
	} else if completedStates[issue.State.Name] {
		sekiaType = "linear.issue.completed"
	} else {
		sekiaType = "linear.issue.updated"
	}

	payload := map[string]any{
		"id":         issue.ID,
		"identifier": issue.Identifier,
		"title":      issue.Title,
		"state":      issue.State.Name,
		"priority":   issue.Priority,
		"team":       issue.Team.Key,
		"url":        issue.URL,
	}

	if issue.Description != "" {
		payload["description"] = issue.Description
	}
	if issue.Assignee != nil {
		payload["assignee"] = issue.Assignee.Name
	}

	labels := make([]string, 0, len(issue.Labels.Nodes))
	for _, l := range issue.Labels.Nodes {
		labels = append(labels, l.Name)
	}
	payload["labels"] = labels

	return protocol.NewEvent(sekiaType, "linear", payload)
}

// MapCommentEvent converts a Linear comment into a sekia Event.
func MapCommentEvent(comment LinearComment) protocol.Event {
	payload := map[string]any{
		"id":               comment.ID,
		"body":             comment.Body,
		"author":           comment.User.Name,
		"issue_id":         comment.Issue.ID,
		"issue_identifier": comment.Issue.Identifier,
	}

	return protocol.NewEvent("linear.comment.created", "linear", payload)
}
