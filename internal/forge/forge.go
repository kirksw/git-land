// Package forge isolates forge-specific pull-request behavior behind the
// Publisher interface so the landing workflow stays forge-agnostic.
package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kirksw/git-land/internal/config"
	"github.com/kirksw/git-land/internal/git"
)

// Check is a single continuous-integration check attached to a pull request.
type Check struct {
	Name  string `json:"name"`
	State string `json:"state"` // success | failure | pending
}

// PullRequest is the forge-visible state of a branch's pull request.
type PullRequest struct {
	URL        string  `json:"url"`
	State      string  `json:"state"` // OPEN | MERGED | CLOSED
	Mergeable  string  `json:"mergeable,omitempty"`
	MergeState string  `json:"mergeState,omitempty"` // CLEAN | BLOCKED | DIRTY | UNKNOWN | BEHIND | HAS_HOOKS
	Checks     []Check `json:"checks,omitempty"`
}

// Settled reports whether the forge has finished computing mergeability and
// no further required work (pending or expected checks, required branch
// updates) is outstanding according to the merge state.
func (p PullRequest) Settled() bool {
	if p.Mergeable == "UNKNOWN" || p.MergeState == "" {
		return false
	}
	switch p.MergeState {
	case "BLOCKED", "UNKNOWN", "BEHIND", "DIRTY":
		return false
	}
	return true
}

// AllChecksPassed reports whether every check succeeded; a pull request with
// no checks counts as passed so repositories without CI can still land.
func (p PullRequest) AllChecksPassed() bool {
	for _, check := range p.Checks {
		if check.State != "success" {
			return false
		}
	}
	return true
}

// HasPendingChecks reports whether any check has not completed yet.
func (p PullRequest) HasPendingChecks() bool {
	for _, check := range p.Checks {
		if check.State == "pending" {
			return true
		}
	}
	return false
}

// Publisher isolates forge-specific pull-request behavior from the Git workflow.
type Publisher interface {
	CreatePullRequest(context.Context, git.State, config.Config) (string, error)
	// PullRequest looks up the open or recently merged pull request for the
	// branch; found is false when no pull request exists.
	PullRequest(context.Context, git.State, config.Config) (PullRequest, bool, error)
	Merge(context.Context, git.State, config.Config) error
}

// UnsupportedPublisher fails explicitly until a forge adapter is configured.
type UnsupportedPublisher struct{}

func (UnsupportedPublisher) CreatePullRequest(context.Context, git.State, config.Config) (string, error) {
	return "", fmt.Errorf("pull-request publication needs a forge adapter; branch was pushed but no pull request was created")
}

func (UnsupportedPublisher) PullRequest(context.Context, git.State, config.Config) (PullRequest, bool, error) {
	return PullRequest{}, false, fmt.Errorf("pull-request inspection needs a forge adapter")
}

func (UnsupportedPublisher) Merge(context.Context, git.State, config.Config) error {
	return fmt.Errorf("merging needs a forge adapter")
}

// GitHubCLIPublisher delegates GitHub-specific behavior to the official gh CLI.
type GitHubCLIPublisher struct{}

func (GitHubCLIPublisher) CreatePullRequest(ctx context.Context, state git.State, cfg config.Config) (string, error) {
	args := []string{"pr", "create", "--base", state.Base, "--head", state.Branch, "--fill"}
	if cfg.PullRequest.Draft {
		args = append(args, "--draft")
	}
	output, err := runGH(ctx, state, args...)
	if err != nil {
		return "", fmt.Errorf("create GitHub pull request: %w", err)
	}
	return strings.TrimSpace(output), nil
}

func (GitHubCLIPublisher) PullRequest(ctx context.Context, state git.State, cfg config.Config) (PullRequest, bool, error) {
	output, err := runGH(ctx, state, "pr", "view", state.Branch, "--json", "url,state,mergeable,mergeStateStatus,statusCheckRollup")
	if err != nil {
		if strings.Contains(strings.ToLower(output), "no pull requests found") {
			return PullRequest{}, false, nil
		}
		return PullRequest{}, false, fmt.Errorf("inspect GitHub pull request: %w", err)
	}
	var view struct {
		URL               string          `json:"url"`
		State             string          `json:"state"`
		Mergeable         string          `json:"mergeable"`
		MergeState        string          `json:"mergeStateStatus"`
		StatusCheckRollup []rollupAttempt `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal([]byte(output), &view); err != nil {
		return PullRequest{}, false, fmt.Errorf("parse GitHub pull request: %w", err)
	}
	pr := PullRequest{URL: view.URL, State: view.State, Mergeable: view.Mergeable, MergeState: view.MergeState}
	for _, entry := range view.StatusCheckRollup {
		name, checkState := entry.check()
		if name == "" {
			continue
		}
		pr.Checks = append(pr.Checks, Check{Name: name, State: checkState})
	}
	return pr, true, nil
}

// rollupAttempt mirrors the union of gh's CheckRun and StatusContext shapes.
type rollupAttempt struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

func (r rollupAttempt) check() (string, string) {
	if r.Name != "" || r.Conclusion != "" || (r.Status != "" && r.Context == "") {
		name := r.Name
		if name == "" {
			name = r.Context
		}
		switch {
		case r.Status != "COMPLETED":
			return name, "pending"
		case r.Conclusion == "SUCCESS":
			return name, "success"
		default:
			return name, "failure"
		}
	}
	if r.Context != "" {
		switch r.State {
		case "SUCCESS":
			return r.Context, "success"
		case "FAILURE", "ERROR":
			return r.Context, "failure"
		default:
			return r.Context, "pending"
		}
	}
	return "", ""
}

func (GitHubCLIPublisher) Merge(ctx context.Context, state git.State, cfg config.Config) error {
	args := []string{"pr", "merge", state.Branch}
	switch cfg.MergeMethod() {
	case "squash":
		args = append(args, "--squash")
	case "merge":
		args = append(args, "--merge")
	case "rebase":
		args = append(args, "--rebase")
	}
	if cfg.Merge.DeleteBranch {
		args = append(args, "--delete-branch")
	}
	if _, err := runGH(ctx, state, args...); err != nil {
		return fmt.Errorf("merge GitHub pull request: %w", err)
	}
	return nil
}

func runGH(ctx context.Context, state git.State, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = state.Root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s", strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
