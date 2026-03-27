package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const githubGraphQLURL = "https://api.github.com/graphql"

// ProjectField represents a field value to set on a GitHub Project item.
type ProjectField struct {
	FieldID             string  `json:"field_id"`
	Text                *string `json:"text,omitempty"`
	Number              *float64 `json:"number,omitempty"`
	Date                *string `json:"date,omitempty"`
	SingleSelectOptionID *string `json:"single_select_option_id,omitempty"`
	IterationID         *string `json:"iteration_id,omitempty"`
}

// fieldValue converts the ProjectField into the GraphQL ProjectV2FieldValue input.
func (f ProjectField) fieldValue() map[string]any {
	v := map[string]any{}
	if f.Text != nil {
		v["text"] = *f.Text
	}
	if f.Number != nil {
		v["number"] = *f.Number
	}
	if f.Date != nil {
		v["date"] = *f.Date
	}
	if f.SingleSelectOptionID != nil {
		v["singleSelectOptionId"] = *f.SingleSelectOptionID
	}
	if f.IterationID != nil {
		v["iterationId"] = *f.IterationID
	}
	return v
}

// graphqlClient sends raw GraphQL requests to the GitHub API.
type graphqlClient struct {
	http  *http.Client
	token string
}

func (g *graphqlClient) do(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal graphql request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", githubGraphQLURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.token)

	resp, err := g.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read graphql response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graphql request failed (HTTP %d): %s", resp.StatusCode, respBody)
	}

	var gqlResp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("unmarshal graphql response: %w", err)
	}
	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
	}
	return gqlResp.Data, nil
}

// getPRNodeID resolves the GraphQL node ID for a pull request.
func (g *graphqlClient) getPRNodeID(ctx context.Context, owner, repo string, number int) (string, error) {
	data, err := g.do(ctx, `query($owner: String!, $repo: String!, $number: Int!) {
		repository(owner: $owner, name: $repo) {
			pullRequest(number: $number) { id }
		}
	}`, map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": number,
	})
	if err != nil {
		return "", err
	}
	var result struct {
		Repository struct {
			PullRequest struct {
				ID string `json:"id"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unmarshal PR node ID: %w", err)
	}
	if result.Repository.PullRequest.ID == "" {
		return "", fmt.Errorf("pull request %s/%s#%d not found", owner, repo, number)
	}
	return result.Repository.PullRequest.ID, nil
}

// addItemToProject adds a content node to a project and returns the project item ID.
func (g *graphqlClient) addItemToProject(ctx context.Context, projectID, contentID string) (string, error) {
	data, err := g.do(ctx, `mutation($projectId: ID!, $contentId: ID!) {
		addProjectV2ItemById(input: {projectId: $projectId, contentId: $contentId}) {
			item { id }
		}
	}`, map[string]any{
		"projectId": projectID,
		"contentId": contentID,
	})
	if err != nil {
		return "", err
	}
	var result struct {
		AddProjectV2ItemById struct {
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"addProjectV2ItemById"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unmarshal project item: %w", err)
	}
	return result.AddProjectV2ItemById.Item.ID, nil
}

// updateItemField sets a single field value on a project item.
func (g *graphqlClient) updateItemField(ctx context.Context, projectID, itemID, fieldID string, value map[string]any) error {
	_, err := g.do(ctx, `mutation($projectId: ID!, $itemId: ID!, $fieldId: ID!, $value: ProjectV2FieldValue!) {
		updateProjectV2ItemFieldValue(input: {projectId: $projectId, itemId: $itemId, fieldId: $fieldId, value: $value}) {
			projectV2Item { id }
		}
	}`, map[string]any{
		"projectId": projectID,
		"itemId":    itemID,
		"fieldId":   fieldID,
		"value":     value,
	})
	return err
}

func (c *realGitHubClient) AddToProject(ctx context.Context, owner, repo string, number int, projectID string, fields []ProjectField) (string, error) {
	gql := &graphqlClient{http: c.httpClient, token: c.token}

	nodeID, err := gql.getPRNodeID(ctx, owner, repo, number)
	if err != nil {
		return "", fmt.Errorf("resolve PR node ID: %w", err)
	}

	itemID, err := gql.addItemToProject(ctx, projectID, nodeID)
	if err != nil {
		return "", fmt.Errorf("add to project: %w", err)
	}

	for _, f := range fields {
		if err := gql.updateItemField(ctx, projectID, itemID, f.FieldID, f.fieldValue()); err != nil {
			return itemID, fmt.Errorf("set field %s: %w", f.FieldID, err)
		}
	}

	return itemID, nil
}
