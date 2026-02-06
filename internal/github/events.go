package github

import (
	"fmt"

	gh "github.com/google/go-github/v68/github"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// MapWebhookEvent converts a GitHub webhook delivery into a sekia Event.
// eventType is the X-GitHub-Event header value. payload is the raw JSON body.
// Returns the event and true, or zero value and false if the event type/action is unsupported.
func MapWebhookEvent(eventType string, payload []byte) (protocol.Event, bool) {
	parsed, err := gh.ParseWebHook(eventType, payload)
	if err != nil {
		return protocol.Event{}, false
	}

	var sekiaType string
	var sekiaPayload map[string]any

	switch e := parsed.(type) {
	case *gh.IssuesEvent:
		action := e.GetAction()
		switch action {
		case "opened", "closed", "reopened", "labeled", "assigned":
			sekiaType = "github.issue." + action
			sekiaPayload = issuePayload(e)
		default:
			return protocol.Event{}, false
		}

	case *gh.PullRequestEvent:
		action := e.GetAction()
		switch action {
		case "opened":
			sekiaType = "github.pr.opened"
			sekiaPayload = prPayload(e)
		case "closed":
			if e.GetPullRequest().GetMerged() {
				sekiaType = "github.pr.merged"
			} else {
				sekiaType = "github.pr.closed"
			}
			sekiaPayload = prPayload(e)
		case "review_requested":
			sekiaType = "github.pr.review_requested"
			sekiaPayload = prPayload(e)
		default:
			return protocol.Event{}, false
		}

	case *gh.PushEvent:
		sekiaType = "github.push"
		sekiaPayload = pushPayload(e)

	case *gh.IssueCommentEvent:
		if e.GetAction() != "created" {
			return protocol.Event{}, false
		}
		sekiaType = "github.comment.created"
		sekiaPayload = commentPayload(e)

	default:
		return protocol.Event{}, false
	}

	return protocol.NewEvent(sekiaType, "github", sekiaPayload), true
}

func issuePayload(e *gh.IssuesEvent) map[string]any {
	issue := e.GetIssue()
	p := map[string]any{
		"owner":  e.GetRepo().GetOwner().GetLogin(),
		"repo":   e.GetRepo().GetName(),
		"number": issue.GetNumber(),
		"title":  issue.GetTitle(),
		"body":   issue.GetBody(),
		"author": issue.GetUser().GetLogin(),
		"url":    issue.GetHTMLURL(),
	}

	labels := make([]string, 0, len(issue.Labels))
	for _, l := range issue.Labels {
		labels = append(labels, l.GetName())
	}
	p["labels"] = labels

	if e.GetAction() == "labeled" && e.GetLabel() != nil {
		p["label"] = e.GetLabel().GetName()
	}
	if e.GetAction() == "assigned" && e.GetAssignee() != nil {
		p["assignee"] = e.GetAssignee().GetLogin()
	}

	return p
}

func prPayload(e *gh.PullRequestEvent) map[string]any {
	pr := e.GetPullRequest()
	p := map[string]any{
		"owner":       e.GetRepo().GetOwner().GetLogin(),
		"repo":        e.GetRepo().GetName(),
		"number":      pr.GetNumber(),
		"title":       pr.GetTitle(),
		"body":        pr.GetBody(),
		"author":      pr.GetUser().GetLogin(),
		"head_branch": pr.GetHead().GetRef(),
		"base_branch": pr.GetBase().GetRef(),
		"url":         pr.GetHTMLURL(),
	}

	if pr.GetMerged() {
		p["merge_commit"] = pr.GetMergeCommitSHA()
	}

	if e.GetAction() == "review_requested" && e.GetRequestedReviewer() != nil {
		p["reviewer"] = e.GetRequestedReviewer().GetLogin()
	}

	return p
}

func pushPayload(e *gh.PushEvent) map[string]any {
	p := map[string]any{
		"owner":         e.GetRepo().GetOwner().GetLogin(),
		"repo":          e.GetRepo().GetName(),
		"ref":           e.GetRef(),
		"before":        e.GetBefore(),
		"after":         e.GetAfter(),
		"commits_count": len(e.Commits),
		"pusher":        e.GetPusher().GetLogin(),
	}

	if e.GetHeadCommit() != nil {
		p["head_commit_message"] = e.GetHeadCommit().GetMessage()
	}

	return p
}

func commentPayload(e *gh.IssueCommentEvent) map[string]any {
	comment := e.GetComment()
	return map[string]any{
		"owner":        e.GetRepo().GetOwner().GetLogin(),
		"repo":         e.GetRepo().GetName(),
		"issue_number": e.GetIssue().GetNumber(),
		"comment_id":   fmt.Sprintf("%d", comment.GetID()),
		"body":         comment.GetBody(),
		"author":       comment.GetUser().GetLogin(),
		"url":          comment.GetHTMLURL(),
	}
}
