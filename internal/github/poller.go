package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// RepoRef is a parsed owner/repo reference.
type RepoRef struct {
	Owner string
	Repo  string
}

// Poller periodically queries the GitHub REST API for updated issues, PRs, and comments.
type Poller struct {
	client       GitHubClient
	interval     time.Duration
	repos        []RepoRef
	onEvent      func(protocol.Event)
	logger       zerolog.Logger
	lastSyncTime time.Time
}

// NewPoller creates a GitHub API poller.
func NewPoller(client GitHubClient, interval time.Duration, repos []RepoRef, onEvent func(protocol.Event), logger zerolog.Logger) *Poller {
	return &Poller{
		client:       client,
		interval:     interval,
		repos:        repos,
		onEvent:      onEvent,
		logger:       logger.With().Str("component", "poller").Logger(),
		lastSyncTime: time.Now().Add(-5 * time.Minute),
	}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) error {
	p.poll(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *Poller) poll(ctx context.Context) {
	since := p.lastSyncTime
	now := time.Now()
	var failed bool

	for _, repo := range p.repos {
		if err := p.pollIssues(ctx, repo, since); err != nil {
			p.logger.Error().Err(err).Str("repo", repo.Owner+"/"+repo.Repo).Msg("poll issues failed")
			failed = true
		}
		if err := p.pollPRs(ctx, repo, since); err != nil {
			p.logger.Error().Err(err).Str("repo", repo.Owner+"/"+repo.Repo).Msg("poll PRs failed")
			failed = true
		}
		if err := p.pollComments(ctx, repo, since); err != nil {
			p.logger.Error().Err(err).Str("repo", repo.Owner+"/"+repo.Repo).Msg("poll comments failed")
			failed = true
		}
	}

	if !failed {
		p.lastSyncTime = now
	}

	p.logger.Debug().Int("repos", len(p.repos)).Bool("failed", failed).Msg("poll complete")
}

func (p *Poller) pollIssues(ctx context.Context, repo RepoRef, since time.Time) error {
	issues, err := p.client.ListIssuesSince(ctx, repo.Owner, repo.Repo, since)
	if err != nil {
		return err
	}

	for _, issue := range issues {
		// GitHub Issues API returns PRs as issues; skip them.
		if issue.PullRequestLinks != nil {
			continue
		}
		ev := MapPolledIssue(issue, repo.Owner, repo.Repo, since)
		p.onEvent(ev)
		p.logger.Debug().
			Str("type", ev.Type).
			Int("number", issue.GetNumber()).
			Msg("issue event")
	}
	return nil
}

func (p *Poller) pollPRs(ctx context.Context, repo RepoRef, since time.Time) error {
	prs, err := p.client.ListPRsSince(ctx, repo.Owner, repo.Repo, since)
	if err != nil {
		return err
	}

	for _, pr := range prs {
		ev := MapPolledPR(pr, repo.Owner, repo.Repo, since)
		p.onEvent(ev)
		p.logger.Debug().
			Str("type", ev.Type).
			Int("number", pr.GetNumber()).
			Msg("PR event")
	}
	return nil
}

func (p *Poller) pollComments(ctx context.Context, repo RepoRef, since time.Time) error {
	comments, err := p.client.ListCommentsSince(ctx, repo.Owner, repo.Repo, since)
	if err != nil {
		return err
	}

	for _, comment := range comments {
		ev := MapPolledComment(comment, repo.Owner, repo.Repo)
		p.onEvent(ev)
	}
	return nil
}

// ParseRepos parses a slice of "owner/repo" strings into RepoRef values.
func ParseRepos(repos []string) ([]RepoRef, error) {
	result := make([]RepoRef, 0, len(repos))
	for _, r := range repos {
		parts := strings.SplitN(r, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid repo format %q, expected owner/repo", r)
		}
		result = append(result, RepoRef{Owner: parts[0], Repo: parts[1]})
	}
	return result, nil
}
