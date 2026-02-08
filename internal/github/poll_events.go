package github

import (
	"fmt"
	"time"

	gh "github.com/google/go-github/v68/github"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// MapPolledIssue converts a GitHub Issue from the REST API into a sekia Event.
// Issues with CreatedAt after lastSyncTime are treated as newly opened;
// closed issues map to github.issue.closed; everything else is github.issue.updated.
func MapPolledIssue(issue *gh.Issue, owner, repo string, lastSyncTime time.Time) protocol.Event {
	var sekiaType string

	if issue.GetCreatedAt().Time.After(lastSyncTime) {
		sekiaType = "github.issue.opened"
	} else if issue.GetState() == "closed" {
		sekiaType = "github.issue.closed"
	} else {
		sekiaType = "github.issue.updated"
	}

	p := map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": issue.GetNumber(),
		"title":  issue.GetTitle(),
		"body":   issue.GetBody(),
		"author": issue.GetUser().GetLogin(),
		"url":    issue.GetHTMLURL(),
		"polled": true,
	}

	labels := make([]string, 0, len(issue.Labels))
	for _, l := range issue.Labels {
		labels = append(labels, l.GetName())
	}
	p["labels"] = labels

	return protocol.NewEvent(sekiaType, "github", p)
}

// MapPolledPR converts a GitHub PullRequest from the REST API into a sekia Event.
// PRs with CreatedAt after lastSyncTime are treated as newly opened;
// merged PRs map to github.pr.merged; closed PRs to github.pr.closed;
// everything else is github.pr.updated.
func MapPolledPR(pr *gh.PullRequest, owner, repo string, lastSyncTime time.Time) protocol.Event {
	var sekiaType string

	if pr.GetCreatedAt().Time.After(lastSyncTime) {
		sekiaType = "github.pr.opened"
	} else if pr.GetMerged() {
		sekiaType = "github.pr.merged"
	} else if pr.GetState() == "closed" {
		sekiaType = "github.pr.closed"
	} else {
		sekiaType = "github.pr.updated"
	}

	p := map[string]any{
		"owner":       owner,
		"repo":        repo,
		"number":      pr.GetNumber(),
		"title":       pr.GetTitle(),
		"body":        pr.GetBody(),
		"author":      pr.GetUser().GetLogin(),
		"head_branch": pr.GetHead().GetRef(),
		"base_branch": pr.GetBase().GetRef(),
		"url":         pr.GetHTMLURL(),
		"polled":      true,
	}

	if pr.GetMerged() {
		p["merge_commit"] = pr.GetMergeCommitSHA()
	}

	return protocol.NewEvent(sekiaType, "github", p)
}

// MapPolledComment converts a GitHub IssueComment from the REST API into a sekia Event.
func MapPolledComment(comment *gh.IssueComment, owner, repo string) protocol.Event {
	p := map[string]any{
		"owner":      owner,
		"repo":       repo,
		"comment_id": fmt.Sprintf("%d", comment.GetID()),
		"body":       comment.GetBody(),
		"author":     comment.GetUser().GetLogin(),
		"url":        comment.GetHTMLURL(),
		"polled":     true,
	}

	return protocol.NewEvent("github.comment.created", "github", p)
}
