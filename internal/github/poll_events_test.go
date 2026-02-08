package github

import (
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"
)

func TestMapPolledIssue_Opened(t *testing.T) {
	now := time.Now()
	lastSync := now.Add(-1 * time.Minute)
	created := gh.Timestamp{Time: now} // after lastSync

	issue := &gh.Issue{
		Number:    gh.Ptr(42),
		Title:     gh.Ptr("New issue"),
		Body:      gh.Ptr("body"),
		State:     gh.Ptr("open"),
		HTMLURL:   gh.Ptr("https://github.com/o/r/issues/42"),
		User:      &gh.User{Login: gh.Ptr("alice")},
		CreatedAt: &created,
		Labels:    []*gh.Label{{Name: gh.Ptr("bug")}},
	}

	ev := MapPolledIssue(issue, "o", "r", lastSync)
	if ev.Type != "github.issue.opened" {
		t.Errorf("got type %q, want github.issue.opened", ev.Type)
	}
	if ev.Payload["number"] != 42 {
		t.Errorf("got number %v", ev.Payload["number"])
	}
	if ev.Payload["polled"] != true {
		t.Error("expected polled=true")
	}
	labels := ev.Payload["labels"].([]string)
	if len(labels) != 1 || labels[0] != "bug" {
		t.Errorf("unexpected labels: %v", labels)
	}
}

func TestMapPolledIssue_Closed(t *testing.T) {
	now := time.Now()
	lastSync := now.Add(-1 * time.Minute)
	created := gh.Timestamp{Time: now.Add(-1 * time.Hour)} // before lastSync

	issue := &gh.Issue{
		Number:    gh.Ptr(10),
		Title:     gh.Ptr("Old issue"),
		Body:      gh.Ptr(""),
		State:     gh.Ptr("closed"),
		HTMLURL:   gh.Ptr("https://github.com/o/r/issues/10"),
		User:      &gh.User{Login: gh.Ptr("bob")},
		CreatedAt: &created,
	}

	ev := MapPolledIssue(issue, "o", "r", lastSync)
	if ev.Type != "github.issue.closed" {
		t.Errorf("got type %q, want github.issue.closed", ev.Type)
	}
}

func TestMapPolledIssue_Updated(t *testing.T) {
	now := time.Now()
	lastSync := now.Add(-1 * time.Minute)
	created := gh.Timestamp{Time: now.Add(-1 * time.Hour)} // before lastSync

	issue := &gh.Issue{
		Number:    gh.Ptr(7),
		Title:     gh.Ptr("Updated issue"),
		Body:      gh.Ptr(""),
		State:     gh.Ptr("open"),
		HTMLURL:   gh.Ptr("https://github.com/o/r/issues/7"),
		User:      &gh.User{Login: gh.Ptr("carol")},
		CreatedAt: &created,
	}

	ev := MapPolledIssue(issue, "o", "r", lastSync)
	if ev.Type != "github.issue.updated" {
		t.Errorf("got type %q, want github.issue.updated", ev.Type)
	}
}

func TestMapPolledPR_Opened(t *testing.T) {
	now := time.Now()
	lastSync := now.Add(-1 * time.Minute)
	created := gh.Timestamp{Time: now}

	pr := &gh.PullRequest{
		Number:    gh.Ptr(5),
		Title:     gh.Ptr("New PR"),
		Body:      gh.Ptr("description"),
		State:     gh.Ptr("open"),
		Merged:    gh.Ptr(false),
		HTMLURL:   gh.Ptr("https://github.com/o/r/pull/5"),
		User:      &gh.User{Login: gh.Ptr("alice")},
		Head:      &gh.PullRequestBranch{Ref: gh.Ptr("feature")},
		Base:      &gh.PullRequestBranch{Ref: gh.Ptr("main")},
		CreatedAt: &created,
	}

	ev := MapPolledPR(pr, "o", "r", lastSync)
	if ev.Type != "github.pr.opened" {
		t.Errorf("got type %q, want github.pr.opened", ev.Type)
	}
	if ev.Payload["head_branch"] != "feature" {
		t.Errorf("got head_branch %v", ev.Payload["head_branch"])
	}
}

func TestMapPolledPR_Merged(t *testing.T) {
	now := time.Now()
	lastSync := now.Add(-1 * time.Minute)
	created := gh.Timestamp{Time: now.Add(-1 * time.Hour)}

	pr := &gh.PullRequest{
		Number:         gh.Ptr(3),
		Title:          gh.Ptr("Merged PR"),
		Body:           gh.Ptr(""),
		State:          gh.Ptr("closed"),
		Merged:         gh.Ptr(true),
		MergeCommitSHA: gh.Ptr("abc123"),
		HTMLURL:        gh.Ptr("https://github.com/o/r/pull/3"),
		User:           &gh.User{Login: gh.Ptr("bob")},
		Head:           &gh.PullRequestBranch{Ref: gh.Ptr("fix")},
		Base:           &gh.PullRequestBranch{Ref: gh.Ptr("main")},
		CreatedAt:      &created,
	}

	ev := MapPolledPR(pr, "o", "r", lastSync)
	if ev.Type != "github.pr.merged" {
		t.Errorf("got type %q, want github.pr.merged", ev.Type)
	}
	if ev.Payload["merge_commit"] != "abc123" {
		t.Errorf("got merge_commit %v", ev.Payload["merge_commit"])
	}
}

func TestMapPolledPR_Closed(t *testing.T) {
	now := time.Now()
	lastSync := now.Add(-1 * time.Minute)
	created := gh.Timestamp{Time: now.Add(-1 * time.Hour)}

	pr := &gh.PullRequest{
		Number:    gh.Ptr(4),
		Title:     gh.Ptr("Closed PR"),
		Body:      gh.Ptr(""),
		State:     gh.Ptr("closed"),
		Merged:    gh.Ptr(false),
		HTMLURL:   gh.Ptr("https://github.com/o/r/pull/4"),
		User:      &gh.User{Login: gh.Ptr("carol")},
		Head:      &gh.PullRequestBranch{Ref: gh.Ptr("wip")},
		Base:      &gh.PullRequestBranch{Ref: gh.Ptr("main")},
		CreatedAt: &created,
	}

	ev := MapPolledPR(pr, "o", "r", lastSync)
	if ev.Type != "github.pr.closed" {
		t.Errorf("got type %q, want github.pr.closed", ev.Type)
	}
}

func TestMapPolledPR_Updated(t *testing.T) {
	now := time.Now()
	lastSync := now.Add(-1 * time.Minute)
	created := gh.Timestamp{Time: now.Add(-1 * time.Hour)}

	pr := &gh.PullRequest{
		Number:    gh.Ptr(6),
		Title:     gh.Ptr("Updated PR"),
		Body:      gh.Ptr(""),
		State:     gh.Ptr("open"),
		Merged:    gh.Ptr(false),
		HTMLURL:   gh.Ptr("https://github.com/o/r/pull/6"),
		User:      &gh.User{Login: gh.Ptr("dave")},
		Head:      &gh.PullRequestBranch{Ref: gh.Ptr("dev")},
		Base:      &gh.PullRequestBranch{Ref: gh.Ptr("main")},
		CreatedAt: &created,
	}

	ev := MapPolledPR(pr, "o", "r", lastSync)
	if ev.Type != "github.pr.updated" {
		t.Errorf("got type %q, want github.pr.updated", ev.Type)
	}
}

func TestMapPolledComment(t *testing.T) {
	comment := &gh.IssueComment{
		ID:      gh.Ptr(int64(999)),
		Body:    gh.Ptr("Nice work!"),
		HTMLURL: gh.Ptr("https://github.com/o/r/issues/1#issuecomment-999"),
		User:    &gh.User{Login: gh.Ptr("eve")},
	}

	ev := MapPolledComment(comment, "o", "r")
	if ev.Type != "github.comment.created" {
		t.Errorf("got type %q, want github.comment.created", ev.Type)
	}
	if ev.Payload["comment_id"] != "999" {
		t.Errorf("got comment_id %v", ev.Payload["comment_id"])
	}
	if ev.Payload["author"] != "eve" {
		t.Errorf("got author %v", ev.Payload["author"])
	}
	if ev.Payload["polled"] != true {
		t.Error("expected polled=true")
	}
}
