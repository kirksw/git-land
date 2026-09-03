package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kirksw/git-land/internal/config"
	"github.com/kirksw/git-land/internal/forge"
	gitrepo "github.com/kirksw/git-land/internal/git"
	"github.com/kirksw/git-land/internal/validation"
)

type Service struct{ Publisher forge.Publisher }

// Wait configures in-process polling on ci_pending; the zero value disables
// waiting so callers receive the first observation immediately.
type Wait struct {
	Enabled  bool
	Timeout  time.Duration
	Interval time.Duration
	// Progress receives each poll observation while waiting.
	Progress func(elapsed time.Duration, pr forge.PullRequest)
}

const (
	defaultWaitTimeout = 30 * time.Minute
	defaultWaitPoll    = 15 * time.Second
	// emptyCheckGrace is how many consecutive check-less observations a
	// settled pull request may show before land concludes the repository has
	// no CI to wait for.
	emptyCheckGrace = 3
)

// Report is the machine-readable landing state; phase and blockedOn form the
// contract that drives agent loops.
type Report struct {
	ConfigPath     string              `json:"configPath"`
	State          gitrepo.State       `json:"state"`
	Validation     []validation.Result `json:"validation,omitempty"`
	Phase          string              `json:"phase"`
	BlockedOn      string              `json:"blockedOn,omitempty"`
	Hint           string              `json:"hint,omitempty"`
	Published      bool                `json:"published"`
	PullRequestURL string              `json:"pullRequestUrl,omitempty"`
	MergeState     string              `json:"mergeState,omitempty"`
	Checks         []forge.Check       `json:"checks,omitempty"`
	Landed         bool                `json:"landed"`
}

func (s Service) Status(dir, configPath string) (Report, error) {
	cfg, repo, path, err := load(dir, configPath)
	if err != nil {
		return Report{}, err
	}
	state, err := repo.Inspect(cfg.Base)
	if err != nil {
		return Report{}, err
	}
	return Report{ConfigPath: path, State: state}, nil
}

func (s Service) Validate(ctx context.Context, dir, configPath string) (Report, error) {
	report, err := s.Status(dir, configPath)
	if err != nil {
		return report, err
	}
	cfg, err := config.Load(report.ConfigPath)
	if err != nil {
		return report, err
	}
	report.Validation = validation.Run(ctx, report.State.Root, cfg.Validation)
	if !validation.AllPassed(report.Validation) {
		return report, validation.FailedMessage(report.Validation)
	}
	return report, nil
}

// Submit validates and publishes without inspecting continuous integration;
// Land is the full resumable pipeline.
func (s Service) Submit(ctx context.Context, dir, configPath string, dryRun bool) (Report, error) {
	cfg, repo, path, err := load(dir, configPath)
	if err != nil {
		return Report{}, err
	}
	if !dryRun && (cfg.Publish.Strategy != "direct_push" && cfg.Publish.Strategy != "pull_request") {
		return Report{}, fmt.Errorf("unsupported publication strategy")
	}
	if !dryRun {
		if err := repo.Fetch(); err != nil {
			return Report{}, fmt.Errorf("synchronize remote: %w", err)
		}
	}
	report, err := s.Validate(ctx, dir, path)
	if err != nil {
		return report, err
	}
	if blockedOn, hint := gate(report.State, cfg); blockedOn != "" {
		return report, fmt.Errorf("%s", hint)
	}
	if dryRun {
		return report, nil
	}
	if err := repo.Push(report.State.Branch); err != nil {
		return report, fmt.Errorf("push branch: %w", err)
	}
	report.Published = true
	if cfg.Publish.Strategy == "pull_request" {
		url, err := s.publisher().CreatePullRequest(ctx, report.State, cfg)
		if err != nil {
			return report, err
		}
		report.PullRequestURL = url
	}
	return report, nil
}

// Land advances the current branch toward landed as far as policy allows
// and reports the resulting phase; it never performs authoring mutations.
// With Wait.Enabled it polls continuous integration until checks settle, the
// pull request merges or closes, or the timeout elapses. Merge of the remote
// pull request happens only when merge.mode is auto and all checks have
// passed.
func (s Service) Land(ctx context.Context, dir, configPath string, wait Wait) (Report, error) {
	cfg, repo, path, err := load(dir, configPath)
	if err != nil {
		return Report{}, err
	}
	if err := repo.Fetch(); err != nil {
		return Report{}, fmt.Errorf("synchronize remote: %w", err)
	}
	state, err := repo.Inspect(cfg.Base)
	if err != nil {
		return Report{}, err
	}
	report := Report{ConfigPath: path, State: state}
	if blockedOn, hint := gate(state, cfg); blockedOn != "" {
		report.Phase, report.BlockedOn, report.Hint = "blocked", blockedOn, hint
		return report, nil
	}
	report.Validation = validation.Run(ctx, state.Root, cfg.Validation)
	if !validation.AllPassed(report.Validation) {
		report.Phase, report.BlockedOn = "blocked", "validation"
		report.Hint = validation.FailedMessage(report.Validation).Error()
		return report, nil
	}
	report.Phase = "validated"
	if cfg.Publish.Strategy == "direct_push" {
		if err := repo.Push(state.Branch); err != nil {
			return report, fmt.Errorf("push branch: %w", err)
		}
		report.Published, report.Phase, report.Landed = true, "landed", true
		return report, nil
	}
	publisher := s.publisher()
	pr, found, err := publisher.PullRequest(ctx, state, cfg)
	if err != nil {
		return report, err
	}
	if found {
		report.PullRequestURL = pr.URL
		switch pr.State {
		case "MERGED":
			report.Phase, report.Landed = "merged", true
			return report, nil
		case "CLOSED":
			report.Phase, report.BlockedOn = "blocked", "pull_request_closed"
			report.Hint = "the pull request for this branch is closed; open a new branch or reopen it on the forge"
			return report, nil
		}
	}
	if state.Behind > 0 {
		report.Phase, report.BlockedOn = "blocked", "branch_behind_remote"
		report.Hint = fmt.Sprintf("branch is behind origin/%s; reconcile the remote branch before publishing", state.Branch)
		return report, nil
	}
	if state.Ahead > 0 || !found {
		if err := repo.Push(state.Branch); err != nil {
			return report, fmt.Errorf("push branch: %w", err)
		}
		report.Published = true
	}
	if !found {
		url, err := publisher.CreatePullRequest(ctx, state, cfg)
		if err != nil {
			return report, err
		}
		report.PullRequestURL = url
		report.Phase, report.BlockedOn = "published", "ci_pending"
		report.Hint = "pull request created; continuous integration will start; run land again to verify"
		if !wait.Enabled {
			return report, nil
		}
	} else if report.Published {
		report.Phase, report.BlockedOn = "published", "ci_pending"
		report.Hint = "new commits pushed; continuous integration will restart; run land again to verify"
		if !wait.Enabled {
			return report, nil
		}
	} else {
		report.Checks, report.MergeState = pr.Checks, pr.MergeState
		phase, blockedOn, hint := evaluate(pr)
		if phase != "ci_pending" || !wait.Enabled {
			return s.settle(ctx, report, state, cfg, publisher, pr, phase, blockedOn, hint)
		}
	}
	pr, resolved, err := s.awaitCI(ctx, state, cfg, publisher, wait)
	if err != nil {
		return report, err
	}
	if pr.State == "MERGED" {
		report.Phase, report.Landed = "merged", true
		return report, nil
	}
	if pr.State == "CLOSED" {
		report.Phase, report.BlockedOn = "blocked", "pull_request_closed"
		report.Hint = "the pull request for this branch closed while waiting; open a new branch or reopen it on the forge"
		return report, nil
	}
	report.PullRequestURL, report.Checks, report.MergeState = pr.URL, pr.Checks, pr.MergeState
	phase, blockedOn, hint := evaluate(pr)
	if !resolved {
		hint = fmt.Sprintf("timed out after %s waiting for continuous integration; run land again", wait.timeout())
	}
	return s.settle(ctx, report, state, cfg, publisher, pr, phase, blockedOn, hint)
}

// settle records the evaluated phase and applies merge policy when the pull
// request is ready.
func (s Service) settle(ctx context.Context, report Report, state gitrepo.State, cfg config.Config, publisher forge.Publisher, pr forge.PullRequest, phase, blockedOn, hint string) (Report, error) {
	report.Phase, report.BlockedOn, report.Hint = phase, blockedOn, hint
	if phase != "ready_for_merge" {
		return report, nil
	}
	if cfg.MergeMode() == "human" {
		report.BlockedOn, report.Hint = "human_merge", "all checks passed; a human must merge "+pr.URL
		return report, nil
	}
	if err := publisher.Merge(ctx, state, cfg); err != nil {
		return report, err
	}
	report.Phase, report.Landed = "merged", true
	return report, nil
}

// awaitCI polls the pull request until its checks settle, the pull request
// merges or closes externally, the context is cancelled, or the timeout
// elapses; resolved is false only on timeout.
func (s Service) awaitCI(ctx context.Context, state gitrepo.State, cfg config.Config, publisher forge.Publisher, wait Wait) (forge.PullRequest, bool, error) {
	deadline := time.Now().Add(wait.timeout())
	interval := wait.Interval
	if interval <= 0 {
		interval = defaultWaitPoll
	}
	start := time.Now()
	empty := 0
	for {
		pr, found, err := publisher.PullRequest(ctx, state, cfg)
		if err != nil {
			return pr, false, err
		}
		if !found {
			return pr, false, fmt.Errorf("pull request disappeared while waiting")
		}
		resolved := pr.State != "OPEN" ||
			(pr.Settled() && !pr.HasPendingChecks() && (len(pr.Checks) > 0 || empty+1 >= emptyCheckGrace))
		if resolved {
			return pr, true, nil
		}
		if len(pr.Checks) == 0 {
			empty++
		} else {
			empty = 0
		}
		if wait.Progress != nil {
			wait.Progress(time.Since(start), pr)
		}
		if !time.Now().Before(deadline) {
			return pr, false, nil
		}
		select {
		case <-ctx.Done():
			return pr, false, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (w Wait) timeout() time.Duration {
	if w.Timeout > 0 {
		return w.Timeout
	}
	return defaultWaitTimeout
}

// Verify reports pull-request and continuous-integration status without any
// mutation.
func (s Service) Verify(ctx context.Context, dir, configPath string) (Report, error) {
	cfg, repo, path, err := load(dir, configPath)
	if err != nil {
		return Report{}, err
	}
	if cfg.Publish.Strategy != "pull_request" {
		return Report{}, fmt.Errorf("verify requires pull_request strategy")
	}
	if err := repo.Fetch(); err != nil {
		return Report{}, fmt.Errorf("synchronize remote: %w", err)
	}
	state, err := repo.Inspect(cfg.Base)
	if err != nil {
		return Report{}, err
	}
	report := Report{ConfigPath: path, State: state, Phase: "inspected"}
	pr, found, err := s.publisher().PullRequest(ctx, state, cfg)
	if err != nil {
		return report, err
	}
	if !found {
		report.Phase, report.BlockedOn = "blocked", "no_pull_request"
		report.Hint = "no pull request exists for this branch; run land to publish"
		return report, nil
	}
	report.PullRequestURL, report.Checks, report.MergeState = pr.URL, pr.Checks, pr.MergeState
	if pr.State == "MERGED" {
		report.Phase, report.Landed = "merged", true
		return report, nil
	}
	if pr.State == "CLOSED" {
		report.Phase, report.BlockedOn, report.Hint = "blocked", "pull_request_closed", "the pull request for this branch is closed"
		return report, nil
	}
	phase, blockedOn, hint := evaluate(pr)
	report.Phase, report.BlockedOn, report.Hint = phase, blockedOn, hint
	if phase != "ready_for_merge" {
		return report, nil
	}
	if cfg.MergeMode() == "human" {
		report.BlockedOn, report.Hint = "human_merge", "all checks passed; a human must merge "+pr.URL
		return report, nil
	}
	report.BlockedOn, report.Hint = "auto_merge", "all checks passed; run land to merge"
	return report, nil
}

// gate reports the first authoring precondition that blocks publishing.
func gate(state gitrepo.State, cfg config.Config) (blockedOn, hint string) {
	if len(state.Staged) > 0 || len(state.Unstaged) > 0 || len(state.Untracked) > 0 {
		return "dirty_tree", "working tree is not clean; commit, stash, or remove changes before publishing"
	}
	if state.Branch == cfg.Base {
		return "on_base", fmt.Sprintf("refusing to publish from integration base %q", cfg.Base)
	}
	if state.BaseBehind > 0 {
		return "behind_base", fmt.Sprintf("branch is behind %s by %d commit(s); integrate the base before publishing", state.BaseRef, state.BaseBehind)
	}
	if state.BaseAhead == 0 {
		return "no_commits", fmt.Sprintf("branch contains no commits ahead of %s", state.BaseRef)
	}
	return "", ""
}

// evaluate maps pull-request checks and merge state to a landing phase
// before merge policy applies.
func evaluate(pr forge.PullRequest) (phase, blockedOn, hint string) {
	switch {
	case pr.Mergeable == "CONFLICTING" || pr.MergeState == "DIRTY":
		return "blocked", "merge_conflicts", "the pull request has merge conflicts; integrate the base and push again"
	case pr.MergeState == "BEHIND":
		return "blocked", "behind_base", "the pull request branch must be up to date with the base; integrate the base and push again"
	case !pr.Settled():
		hint := "continuous integration is still running; run land again"
		if pr.MergeState == "BLOCKED" {
			hint = "the forge still expects required work (checks or reviews); merge state is BLOCKED"
		}
		return "ci_pending", "ci_pending", hint
	case pr.HasPendingChecks():
		return "ci_pending", "ci_pending", "continuous integration is still running; run land again"
	case !pr.AllChecksPassed():
		var failing []string
		for _, check := range pr.Checks {
			if check.State != "success" {
				failing = append(failing, check.Name)
			}
		}
		return "ci_failed", "ci_failed", "failing checks: " + strings.Join(failing, ", ")
	default:
		return "ready_for_merge", "", "all checks passed"
	}
}

func (s Service) publisher() forge.Publisher {
	if s.Publisher != nil {
		return s.Publisher
	}
	return forge.GitHubCLIPublisher{}
}

func load(dir, configPath string) (config.Config, gitrepo.Repository, string, error) {
	repo, err := gitrepo.Open(dir)
	if err != nil {
		return config.Config{}, gitrepo.Repository{}, "", err
	}
	path := configPath
	if path == "" {
		path = filepath.Join(repo.Dir, "land.yaml")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(repo.Dir, path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return config.Config{}, gitrepo.Repository{}, "", fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := config.Parse(contents)
	if err != nil {
		return config.Config{}, gitrepo.Repository{}, "", fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, repo, path, nil
}
