package github

import (
	"context"
	"fmt"

	gh "github.com/google/go-github/v68/github"
)

// GitHubClient abstracts the GitHub API methods used by commands.
type GitHubClient interface {
	AddLabels(ctx context.Context, owner, repo string, number int, labels []string) error
	RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error
	CreateComment(ctx context.Context, owner, repo string, number int, body string) error
	EditIssueState(ctx context.Context, owner, repo string, number int, state string) error
}

// realGitHubClient wraps the google/go-github client.
type realGitHubClient struct {
	client *gh.Client
}

func (c *realGitHubClient) AddLabels(ctx context.Context, owner, repo string, number int, labels []string) error {
	_, _, err := c.client.Issues.AddLabelsToIssue(ctx, owner, repo, number, labels)
	return err
}

func (c *realGitHubClient) RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error {
	_, err := c.client.Issues.RemoveLabelForIssue(ctx, owner, repo, number, label)
	return err
}

func (c *realGitHubClient) CreateComment(ctx context.Context, owner, repo string, number int, body string) error {
	_, _, err := c.client.Issues.CreateComment(ctx, owner, repo, number, &gh.IssueComment{
		Body: &body,
	})
	return err
}

func (c *realGitHubClient) EditIssueState(ctx context.Context, owner, repo string, number int, state string) error {
	_, _, err := c.client.Issues.Edit(ctx, owner, repo, number, &gh.IssueRequest{
		State: &state,
	})
	return err
}

// extractRepoRef extracts owner, repo, and issue/PR number from a command payload.
func extractRepoRef(payload map[string]any) (owner, repo string, number int, err error) {
	ownerVal, ok := payload["owner"]
	if !ok {
		return "", "", 0, fmt.Errorf("missing required field: owner")
	}
	owner, ok = ownerVal.(string)
	if !ok {
		return "", "", 0, fmt.Errorf("owner must be a string")
	}

	repoVal, ok := payload["repo"]
	if !ok {
		return "", "", 0, fmt.Errorf("missing required field: repo")
	}
	repo, ok = repoVal.(string)
	if !ok {
		return "", "", 0, fmt.Errorf("repo must be a string")
	}

	numVal, ok := payload["number"]
	if !ok {
		return "", "", 0, fmt.Errorf("missing required field: number")
	}
	// JSON numbers arrive as float64.
	switch n := numVal.(type) {
	case float64:
		number = int(n)
	case int:
		number = n
	default:
		return "", "", 0, fmt.Errorf("number must be a number")
	}

	return owner, repo, number, nil
}

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

func cmdAddLabel(ctx context.Context, ghc GitHubClient, payload map[string]any) error {
	owner, repo, number, err := extractRepoRef(payload)
	if err != nil {
		return err
	}
	label, err := extractString(payload, "label")
	if err != nil {
		return err
	}
	return ghc.AddLabels(ctx, owner, repo, number, []string{label})
}

func cmdRemoveLabel(ctx context.Context, ghc GitHubClient, payload map[string]any) error {
	owner, repo, number, err := extractRepoRef(payload)
	if err != nil {
		return err
	}
	label, err := extractString(payload, "label")
	if err != nil {
		return err
	}
	return ghc.RemoveLabel(ctx, owner, repo, number, label)
}

func cmdCreateComment(ctx context.Context, ghc GitHubClient, payload map[string]any) error {
	owner, repo, number, err := extractRepoRef(payload)
	if err != nil {
		return err
	}
	body, err := extractString(payload, "body")
	if err != nil {
		return err
	}
	return ghc.CreateComment(ctx, owner, repo, number, body)
}

func cmdCloseIssue(ctx context.Context, ghc GitHubClient, payload map[string]any) error {
	owner, repo, number, err := extractRepoRef(payload)
	if err != nil {
		return err
	}
	return ghc.EditIssueState(ctx, owner, repo, number, "closed")
}

func cmdReopenIssue(ctx context.Context, ghc GitHubClient, payload map[string]any) error {
	owner, repo, number, err := extractRepoRef(payload)
	if err != nil {
		return err
	}
	return ghc.EditIssueState(ctx, owner, repo, number, "open")
}
