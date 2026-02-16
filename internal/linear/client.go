package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const linearAPIURL = "https://api.linear.app/graphql"

// LinearClient abstracts the Linear API methods used by the poller and commands.
type LinearClient interface {
	// Polling
	FetchUpdatedIssues(ctx context.Context, since time.Time, teamFilter string) ([]LinearIssue, error)
	FetchUpdatedComments(ctx context.Context, since time.Time) ([]LinearComment, error)

	// Commands
	CreateIssue(ctx context.Context, teamID, title, description string) (string, error)
	UpdateIssue(ctx context.Context, issueID string, input map[string]any) error
	CreateComment(ctx context.Context, issueID, body string) error
	AddLabel(ctx context.Context, issueID, labelID string) error
}

// LinearIssue is a minimal representation of a Linear issue from the API.
type LinearIssue struct {
	ID          string    `json:"id"`
	Identifier  string    `json:"identifier"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	State       struct {
		Name string `json:"name"`
	} `json:"state"`
	Assignee *struct {
		Name string `json:"name"`
	} `json:"assignee"`
	Priority float64 `json:"priority"`
	Team     struct {
		Key string `json:"key"`
	} `json:"team"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	URL string `json:"url"`
}

// LinearComment is a minimal representation of a Linear comment.
type LinearComment struct {
	ID        string    `json:"id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	User      struct {
		Name string `json:"name"`
	} `json:"user"`
	Issue struct {
		ID         string `json:"id"`
		Identifier string `json:"identifier"`
	} `json:"issue"`
}

// realLinearClient implements LinearClient using Linear's GraphQL API.
type realLinearClient struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

func newRealLinearClient(apiKey string) *realLinearClient {
	return &realLinearClient{
		apiKey:  apiKey,
		baseURL: linearAPIURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *realLinearClient) graphql(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.apiKey)

	resp, err := c.http.Do(req) // #nosec G704 -- URL is configured API base, not user input
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("linear API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var gqlResp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data, nil
}

func (c *realLinearClient) FetchUpdatedIssues(ctx context.Context, since time.Time, teamFilter string) ([]LinearIssue, error) {
	query := `query($since: DateTime!, $cursor: String) {
		issues(
			filter: { updatedAt: { gte: $since } }
			first: 50
			after: $cursor
			orderBy: updatedAt
		) {
			nodes {
				id identifier title description
				createdAt updatedAt priority url
				state { name }
				assignee { name }
				team { key }
				labels { nodes { name } }
			}
			pageInfo { hasNextPage endCursor }
		}
	}`

	vars := map[string]any{"since": since.Format(time.RFC3339)}

	var allIssues []LinearIssue
	var cursor *string

	for {
		if cursor != nil {
			vars["cursor"] = *cursor
		}
		data, err := c.graphql(ctx, query, vars)
		if err != nil {
			return nil, fmt.Errorf("fetch issues: %w", err)
		}

		var result struct {
			Issues struct {
				Nodes    []LinearIssue `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("unmarshal issues: %w", err)
		}

		for _, issue := range result.Issues.Nodes {
			if teamFilter != "" && issue.Team.Key != teamFilter {
				continue
			}
			allIssues = append(allIssues, issue)
		}

		if !result.Issues.PageInfo.HasNextPage {
			break
		}
		cursor = &result.Issues.PageInfo.EndCursor
	}

	return allIssues, nil
}

func (c *realLinearClient) FetchUpdatedComments(ctx context.Context, since time.Time) ([]LinearComment, error) {
	query := `query($since: DateTime!) {
		comments(
			filter: { createdAt: { gte: $since } }
			first: 50
			orderBy: createdAt
		) {
			nodes {
				id body createdAt
				user { name }
				issue { id identifier }
			}
		}
	}`

	data, err := c.graphql(ctx, query, map[string]any{"since": since.Format(time.RFC3339)})
	if err != nil {
		return nil, fmt.Errorf("fetch comments: %w", err)
	}

	var result struct {
		Comments struct {
			Nodes []LinearComment `json:"nodes"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("unmarshal comments: %w", err)
	}

	return result.Comments.Nodes, nil
}

func (c *realLinearClient) CreateIssue(ctx context.Context, teamID, title, description string) (string, error) {
	query := `mutation($teamId: String!, $title: String!, $description: String) {
		issueCreate(input: { teamId: $teamId, title: $title, description: $description }) {
			issue { id identifier }
		}
	}`

	data, err := c.graphql(ctx, query, map[string]any{
		"teamId":      teamID,
		"title":       title,
		"description": description,
	})
	if err != nil {
		return "", fmt.Errorf("create issue: %w", err)
	}

	var result struct {
		IssueCreate struct {
			Issue struct {
				ID         string `json:"id"`
				Identifier string `json:"identifier"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unmarshal create issue: %w", err)
	}

	return result.IssueCreate.Issue.ID, nil
}

func (c *realLinearClient) UpdateIssue(ctx context.Context, issueID string, input map[string]any) error {
	query := `mutation($id: String!, $input: IssueUpdateInput!) {
		issueUpdate(id: $id, input: $input) {
			issue { id }
		}
	}`

	_, err := c.graphql(ctx, query, map[string]any{
		"id":    issueID,
		"input": input,
	})
	if err != nil {
		return fmt.Errorf("update issue: %w", err)
	}
	return nil
}

func (c *realLinearClient) CreateComment(ctx context.Context, issueID, body string) error {
	query := `mutation($issueId: String!, $body: String!) {
		commentCreate(input: { issueId: $issueId, body: $body }) {
			comment { id }
		}
	}`

	_, err := c.graphql(ctx, query, map[string]any{
		"issueId": issueID,
		"body":    body,
	})
	if err != nil {
		return fmt.Errorf("create comment: %w", err)
	}
	return nil
}

func (c *realLinearClient) AddLabel(ctx context.Context, issueID, labelID string) error {
	query := `mutation($issueId: String!, $labelId: String!) {
		issueAddLabel(id: $issueId, labelId: $labelId) {
			issue { id }
		}
	}`

	_, err := c.graphql(ctx, query, map[string]any{
		"issueId": issueID,
		"labelId": labelID,
	})
	if err != nil {
		return fmt.Errorf("add label: %w", err)
	}
	return nil
}
