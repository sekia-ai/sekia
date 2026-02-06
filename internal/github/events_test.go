package github

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestMapWebhookEvent_IssueOpened(t *testing.T) {
	payload := issueWebhookJSON("opened", "myorg", "myrepo", 42, "Bug: crash on startup", "alice")

	ev, ok := MapWebhookEvent("issues", payload)
	if !ok {
		t.Fatal("expected event to be mapped")
	}
	if ev.Type != "github.issue.opened" {
		t.Errorf("type = %s, want github.issue.opened", ev.Type)
	}
	if ev.Source != "github" {
		t.Errorf("source = %s, want github", ev.Source)
	}
	if ev.Payload["owner"] != "myorg" {
		t.Errorf("owner = %v, want myorg", ev.Payload["owner"])
	}
	if ev.Payload["repo"] != "myrepo" {
		t.Errorf("repo = %v, want myrepo", ev.Payload["repo"])
	}
	if ev.Payload["number"] != 42 {
		t.Errorf("number = %v, want 42", ev.Payload["number"])
	}
	if ev.Payload["title"] != "Bug: crash on startup" {
		t.Errorf("title = %v, want Bug: crash on startup", ev.Payload["title"])
	}
	if ev.Payload["author"] != "alice" {
		t.Errorf("author = %v, want alice", ev.Payload["author"])
	}
}

func TestMapWebhookEvent_IssueClosed(t *testing.T) {
	payload := issueWebhookJSON("closed", "myorg", "myrepo", 42, "Bug: crash on startup", "alice")

	ev, ok := MapWebhookEvent("issues", payload)
	if !ok {
		t.Fatal("expected event to be mapped")
	}
	if ev.Type != "github.issue.closed" {
		t.Errorf("type = %s, want github.issue.closed", ev.Type)
	}
}

func TestMapWebhookEvent_PROpened(t *testing.T) {
	payload := prWebhookJSON("opened", "myorg", "myrepo", 10, "Add feature", "bob", false)

	ev, ok := MapWebhookEvent("pull_request", payload)
	if !ok {
		t.Fatal("expected event to be mapped")
	}
	if ev.Type != "github.pr.opened" {
		t.Errorf("type = %s, want github.pr.opened", ev.Type)
	}
	if ev.Payload["author"] != "bob" {
		t.Errorf("author = %v, want bob", ev.Payload["author"])
	}
}

func TestMapWebhookEvent_PRMerged(t *testing.T) {
	payload := prWebhookJSON("closed", "myorg", "myrepo", 10, "Add feature", "bob", true)

	ev, ok := MapWebhookEvent("pull_request", payload)
	if !ok {
		t.Fatal("expected event to be mapped")
	}
	if ev.Type != "github.pr.merged" {
		t.Errorf("type = %s, want github.pr.merged", ev.Type)
	}
}

func TestMapWebhookEvent_PRClosed(t *testing.T) {
	payload := prWebhookJSON("closed", "myorg", "myrepo", 10, "Add feature", "bob", false)

	ev, ok := MapWebhookEvent("pull_request", payload)
	if !ok {
		t.Fatal("expected event to be mapped")
	}
	if ev.Type != "github.pr.closed" {
		t.Errorf("type = %s, want github.pr.closed", ev.Type)
	}
}

func TestMapWebhookEvent_Push(t *testing.T) {
	payload := pushWebhookJSON("myorg", "myrepo", "refs/heads/main", "abc123", "def456", "alice", "fix: typo")

	ev, ok := MapWebhookEvent("push", payload)
	if !ok {
		t.Fatal("expected event to be mapped")
	}
	if ev.Type != "github.push" {
		t.Errorf("type = %s, want github.push", ev.Type)
	}
	if ev.Payload["ref"] != "refs/heads/main" {
		t.Errorf("ref = %v, want refs/heads/main", ev.Payload["ref"])
	}
	if ev.Payload["head_commit_message"] != "fix: typo" {
		t.Errorf("head_commit_message = %v, want fix: typo", ev.Payload["head_commit_message"])
	}
}

func TestMapWebhookEvent_CommentCreated(t *testing.T) {
	payload := commentWebhookJSON("created", "myorg", "myrepo", 42, 999, "Looks good!", "carol")

	ev, ok := MapWebhookEvent("issue_comment", payload)
	if !ok {
		t.Fatal("expected event to be mapped")
	}
	if ev.Type != "github.comment.created" {
		t.Errorf("type = %s, want github.comment.created", ev.Type)
	}
	if ev.Payload["body"] != "Looks good!" {
		t.Errorf("body = %v, want Looks good!", ev.Payload["body"])
	}
}

func TestMapWebhookEvent_UnsupportedEventType(t *testing.T) {
	_, ok := MapWebhookEvent("deployment", []byte(`{}`))
	if ok {
		t.Error("expected unsupported event type to return false")
	}
}

func TestMapWebhookEvent_UnsupportedAction(t *testing.T) {
	payload := issueWebhookJSON("transferred", "myorg", "myrepo", 1, "Test", "alice")

	_, ok := MapWebhookEvent("issues", payload)
	if ok {
		t.Error("expected unsupported action to return false")
	}
}

// --- Test helpers: build minimal GitHub webhook JSON ---

func issueWebhookJSON(action, owner, repo string, number int, title, author string) []byte {
	data, _ := json.Marshal(map[string]any{
		"action": action,
		"issue": map[string]any{
			"number":   number,
			"title":    title,
			"body":     "Issue body",
			"html_url": "https://github.com/" + owner + "/" + repo + "/issues/" + itoa(number),
			"user":     map[string]any{"login": author},
			"labels":   []any{},
		},
		"repository": map[string]any{
			"name":  repo,
			"owner": map[string]any{"login": owner},
		},
	})
	return data
}

func prWebhookJSON(action, owner, repo string, number int, title, author string, merged bool) []byte {
	data, _ := json.Marshal(map[string]any{
		"action": action,
		"pull_request": map[string]any{
			"number":           number,
			"title":            title,
			"body":             "PR body",
			"html_url":         "https://github.com/" + owner + "/" + repo + "/pull/" + itoa(number),
			"user":             map[string]any{"login": author},
			"merged":           merged,
			"merge_commit_sha": "abc123",
			"head":             map[string]any{"ref": "feature-branch"},
			"base":             map[string]any{"ref": "main"},
		},
		"repository": map[string]any{
			"name":  repo,
			"owner": map[string]any{"login": owner},
		},
	})
	return data
}

func pushWebhookJSON(owner, repo, ref, before, after, pusher, commitMsg string) []byte {
	data, _ := json.Marshal(map[string]any{
		"ref":    ref,
		"before": before,
		"after":  after,
		"pusher": map[string]any{"login": pusher, "name": pusher},
		"head_commit": map[string]any{
			"message": commitMsg,
		},
		"commits": []any{
			map[string]any{"message": commitMsg},
		},
		"repository": map[string]any{
			"name":  repo,
			"owner": map[string]any{"login": owner},
		},
	})
	return data
}

func commentWebhookJSON(action, owner, repo string, issueNumber int, commentID int, body, author string) []byte {
	data, _ := json.Marshal(map[string]any{
		"action": action,
		"issue": map[string]any{
			"number": issueNumber,
		},
		"comment": map[string]any{
			"id":       commentID,
			"body":     body,
			"html_url": "https://github.com/" + owner + "/" + repo + "/issues/" + itoa(issueNumber) + "#issuecomment-" + itoa(commentID),
			"user":     map[string]any{"login": author},
		},
		"repository": map[string]any{
			"name":  repo,
			"owner": map[string]any{"login": owner},
		},
	})
	return data
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
