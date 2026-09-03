package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kirksw/git-land/internal/config"
	"github.com/kirksw/git-land/internal/forge"
	"github.com/kirksw/git-land/internal/git"
)

// fakePublisher scripts forge behavior without network access; when script
// is non-empty each PullRequest call consumes one response, repeating the
// last.
type fakePublisher struct {
	pr       forge.PullRequest
	found    bool
	script   []fakePRState
	created  int
	merged   int
	mergeErr error
}

type fakePRState struct {
	pr    forge.PullRequest
	found bool
}

func (f *fakePublisher) CreatePullRequest(context.Context, git.State, config.Config) (string, error) {
	f.created++
	return "https://example/pr/1", nil
}

func (f *fakePublisher) PullRequest(context.Context, git.State, config.Config) (forge.PullRequest, bool, error) {
	if len(f.script) > 0 {
		next := f.script[0]
		if len(f.script) > 1 {
			f.script = f.script[1:]
		}
		return next.pr, next.found, nil
	}
	return f.pr, f.found, nil
}

func (f *fakePublisher) Merge(context.Context, git.State, config.Config) error {
	f.merged++
	return f.mergeErr
}

const policy = `version: 1
base: main
publish:
  strategy: pull_request
`

// newRepo creates a repository on branch main pushed to a bare origin and
// writes the landing policy.
func newRepo(t *testing.T, policyYAML string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	write(t, dir, "base.txt", "base\n")
	write(t, dir, "land.yaml", policyYAML)
	git("add", ".")
	git("commit", "-m", "base")
	bare := t.TempDir()
	git("init", "--bare", bare)
	git("remote", "add", "origin", bare)
	git("push", "-q", "-u", "origin", "main")
	return dir
}

func write(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func commitChange(t *testing.T, dir, name string) {
	t.Helper()
	write(t, dir, name, "change\n")
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git("add", ".")
	git("commit", "-m", "add "+name)
}

func branchWithCommit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "checkout", "-b", "feature")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout feature: %s", out)
	}
	commitChange(t, dir, "change.txt")
}

func TestLandBlocksOnDirtyTree(t *testing.T) {
	dir := newRepo(t, policy)
	branchWithCommit(t, dir)
	write(t, dir, "loose.txt", "uncommitted\n")
	publisher := &fakePublisher{}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if report.Phase != "blocked" || report.BlockedOn != "dirty_tree" {
		t.Fatalf("phase/blockedOn = %s/%s, want blocked/dirty_tree", report.Phase, report.BlockedOn)
	}
	if report.Landed || publisher.created != 0 {
		t.Fatalf("dirty tree must not publish")
	}
}

func TestLandBlocksOnIntegrationBase(t *testing.T) {
	dir := newRepo(t, policy)
	publisher := &fakePublisher{}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if report.BlockedOn != "on_base" {
		t.Fatalf("blockedOn = %s, want on_base", report.BlockedOn)
	}
}

func TestLandBlocksBehindBase(t *testing.T) {
	dir := newRepo(t, policy)
	branchWithCommit(t, dir)
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git("checkout", "-q", "main")
	commitChange(t, dir, "advance.txt")
	git("push", "-q", "origin", "main")
	git("checkout", "-q", "feature")
	report, err := (Service{Publisher: &fakePublisher{}}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if report.BlockedOn != "behind_base" {
		t.Fatalf("blockedOn = %s, want behind_base", report.BlockedOn)
	}
}

func TestLandCreatesPullRequest(t *testing.T) {
	dir := newRepo(t, policy)
	branchWithCommit(t, dir)
	publisher := &fakePublisher{}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if !report.Published || publisher.created != 1 {
		t.Fatalf("expected branch push and pull-request creation")
	}
	if report.Phase != "published" || report.BlockedOn != "ci_pending" {
		t.Fatalf("phase/blockedOn = %s/%s, want published/ci_pending", report.Phase, report.BlockedOn)
	}
	if report.PullRequestURL != "https://example/pr/1" {
		t.Fatalf("pull request url = %s", report.PullRequestURL)
	}
	if report.Landed {
		t.Fatalf("creation must not land")
	}
}

func TestLandReportsPendingChecks(t *testing.T) {
	dir := newRepo(t, policy)
	branchWithCommit(t, dir)
	git := exec.Command("git", "-C", dir, "push", "-q", "-u", "origin", "feature")
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("push feature: %s", out)
	}
	publisher := &fakePublisher{found: true, pr: forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "MERGEABLE", MergeState: "CLEAN",
		Checks: []forge.Check{{Name: "build", State: "pending"}},
	}}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if report.Phase != "ci_pending" || report.BlockedOn != "ci_pending" {
		t.Fatalf("phase/blockedOn = %s/%s, want ci_pending/ci_pending", report.Phase, report.BlockedOn)
	}
	if len(report.Checks) != 1 || report.Checks[0].Name != "build" {
		t.Fatalf("checks = %#v", report.Checks)
	}
	if report.Published || publisher.merged != 0 {
		t.Fatalf("pending checks must not publish or merge")
	}
}

func TestLandReportsFailingChecks(t *testing.T) {
	dir := newRepo(t, policy)
	branchWithCommit(t, dir)
	git := exec.Command("git", "-C", dir, "push", "-q", "-u", "origin", "feature")
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("push feature: %s", out)
	}
	publisher := &fakePublisher{found: true, pr: forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "MERGEABLE", MergeState: "CLEAN",
		Checks: []forge.Check{{Name: "lint", State: "failure"}},
	}}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if report.Phase != "ci_failed" || !strings.Contains(report.Hint, "lint") {
		t.Fatalf("phase/hint = %s/%s, want ci_failed mentioning lint", report.Phase, report.Hint)
	}
}

func TestLandStopsForHumanMerge(t *testing.T) {
	dir := newRepo(t, policy)
	branchWithCommit(t, dir)
	git := exec.Command("git", "-C", dir, "push", "-q", "-u", "origin", "feature")
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("push feature: %s", out)
	}
	publisher := &fakePublisher{found: true, pr: forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "MERGEABLE", MergeState: "CLEAN",
		Checks: []forge.Check{{Name: "build", State: "success"}},
	}}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if report.Phase != "ready_for_merge" || report.BlockedOn != "human_merge" {
		t.Fatalf("phase/blockedOn = %s/%s, want ready_for_merge/human_merge", report.Phase, report.BlockedOn)
	}
	if report.Landed || publisher.merged != 0 {
		t.Fatalf("human merge policy must not merge")
	}
}

func TestLandAutoMergesWhenGreen(t *testing.T) {
	dir := newRepo(t, policy+"\nmerge:\n  mode: auto\n")
	branchWithCommit(t, dir)
	git := exec.Command("git", "-C", dir, "push", "-q", "-u", "origin", "feature")
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("push feature: %s", out)
	}
	publisher := &fakePublisher{found: true, pr: forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "MERGEABLE", MergeState: "CLEAN",
		Checks: []forge.Check{{Name: "build", State: "success"}},
	}}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if !report.Landed || report.Phase != "merged" || publisher.merged != 1 {
		t.Fatalf("auto merge expected: phase=%s landed=%v merged=%d", report.Phase, report.Landed, publisher.merged)
	}
}

func TestLandReportsMergedPullRequest(t *testing.T) {
	dir := newRepo(t, policy)
	branchWithCommit(t, dir)
	publisher := &fakePublisher{found: true, pr: forge.PullRequest{URL: "https://example/pr/1", State: "MERGED"}}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if !report.Landed || report.Phase != "merged" {
		t.Fatalf("phase/landed = %s/%v, want merged/true", report.Phase, report.Landed)
	}
	if report.Published || publisher.merged != 0 {
		t.Fatalf("already-merged pull request must not push or merge")
	}
}

func TestLandDirectPush(t *testing.T) {
	dir := newRepo(t, "version: 1\nbase: main\npublish:\n  strategy: direct_push\n")
	branchWithCommit(t, dir)
	publisher := &fakePublisher{}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if !report.Landed || report.Phase != "landed" || !report.Published {
		t.Fatalf("direct push expected: phase=%s landed=%v published=%v", report.Phase, report.Landed, report.Published)
	}
	if publisher.created != 0 {
		t.Fatalf("direct push must not create pull requests")
	}
}

func TestVerifyWithoutPullRequest(t *testing.T) {
	dir := newRepo(t, policy)
	branchWithCommit(t, dir)
	publisher := &fakePublisher{}
	report, err := (Service{Publisher: publisher}).Verify(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.BlockedOn != "no_pull_request" {
		t.Fatalf("blockedOn = %s, want no_pull_request", report.BlockedOn)
	}
	if publisher.created != 0 {
		t.Fatalf("verify must not create pull requests")
	}
}

func TestVerifyReadyForAutoMerge(t *testing.T) {
	dir := newRepo(t, policy+"\nmerge:\n  mode: auto\n")
	branchWithCommit(t, dir)
	publisher := &fakePublisher{found: true, pr: forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "MERGEABLE", MergeState: "CLEAN",
		Checks: []forge.Check{{Name: "build", State: "success"}},
	}}
	report, err := (Service{Publisher: publisher}).Verify(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Phase != "ready_for_merge" || report.BlockedOn != "auto_merge" {
		t.Fatalf("phase/blockedOn = %s/%s, want ready_for_merge/auto_merge", report.Phase, report.BlockedOn)
	}
	if publisher.merged != 0 {
		t.Fatalf("verify must not merge")
	}
}

func TestLandBlocksOnMergeConflicts(t *testing.T) {
	dir := newRepo(t, policy)
	branchWithCommit(t, dir)
	git := exec.Command("git", "-C", dir, "push", "-q", "-u", "origin", "feature")
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("push feature: %s", out)
	}
	publisher := &fakePublisher{found: true, pr: forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "CONFLICTING",
		Checks: []forge.Check{{Name: "build", State: "success"}},
	}}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if report.BlockedOn != "merge_conflicts" {
		t.Fatalf("blockedOn = %s, want merge_conflicts", report.BlockedOn)
	}
}

func TestLandWaitsThenMerges(t *testing.T) {
	dir := newRepo(t, policy+"\nmerge:\n  mode: auto\n")
	branchWithCommit(t, dir)
	green := forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "MERGEABLE", MergeState: "CLEAN",
		Checks: []forge.Check{{Name: "build", State: "success"}},
	}
	pending := forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "MERGEABLE", MergeState: "BLOCKED",
		Checks: []forge.Check{{Name: "build", State: "pending"}},
	}
	publisher := &fakePublisher{script: []fakePRState{
		{pr: pending, found: true},
		{pr: pending, found: true},
		{pr: green, found: true},
	}}
	var observed int
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{
		Enabled:  true,
		Interval: time.Millisecond,
		Timeout:  5 * time.Second,
		Progress: func(time.Duration, forge.PullRequest) { observed++ },
	})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if !report.Landed || report.Phase != "merged" || publisher.merged != 1 {
		t.Fatalf("waited merge expected: phase=%s landed=%v merged=%d", report.Phase, report.Landed, publisher.merged)
	}
	if observed < 1 {
		t.Fatalf("progress observations = %d, want at least 1", observed)
	}
}

func TestLandWaitTimesOut(t *testing.T) {
	dir := newRepo(t, policy+"\nmerge:\n  mode: auto\n")
	branchWithCommit(t, dir)
	git := exec.Command("git", "-C", dir, "push", "-q", "-u", "origin", "feature")
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("push feature: %s", out)
	}
	pending := forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "MERGEABLE", MergeState: "BLOCKED",
		Checks: []forge.Check{{Name: "build", State: "pending"}},
	}
	publisher := &fakePublisher{found: true, pr: pending}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{
		Enabled:  true,
		Interval: time.Millisecond,
		Timeout:  30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if report.Phase != "ci_pending" || report.Landed || publisher.merged != 0 {
		t.Fatalf("timeout report expected: phase=%s landed=%v merged=%d", report.Phase, report.Landed, publisher.merged)
	}
	if !strings.Contains(report.Hint, "timed out") {
		t.Fatalf("hint = %q, want timeout mention", report.Hint)
	}
}

func TestLandWaitAcceptsChecklessRepository(t *testing.T) {
	dir := newRepo(t, policy) // mode human
	branchWithCommit(t, dir)
	checkless := forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "MERGEABLE", MergeState: "CLEAN",
	}
	publisher := &fakePublisher{script: []fakePRState{{pr: checkless, found: true}}}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{
		Enabled:  true,
		Interval: time.Millisecond,
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if report.Phase != "ready_for_merge" || report.BlockedOn != "human_merge" {
		t.Fatalf("phase/blockedOn = %s/%s, want ready_for_merge/human_merge", report.Phase, report.BlockedOn)
	}
	if report.Landed || publisher.merged != 0 {
		t.Fatalf("checkless repository must not auto-merge under human policy")
	}
}

func TestLandWaitReportsExternalMerge(t *testing.T) {
	dir := newRepo(t, policy+"\nmerge:\n  mode: auto\n")
	branchWithCommit(t, dir)
	pending := forge.PullRequest{
		URL: "https://example/pr/1", State: "OPEN", Mergeable: "MERGEABLE", MergeState: "BLOCKED",
	}
	merged := forge.PullRequest{URL: "https://example/pr/1", State: "MERGED"}
	publisher := &fakePublisher{script: []fakePRState{
		{pr: pending, found: true},
		{pr: merged, found: true},
	}}
	report, err := (Service{Publisher: publisher}).Land(context.Background(), dir, "", Wait{
		Enabled:  true,
		Interval: time.Millisecond,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("land: %v", err)
	}
	if !report.Landed || report.Phase != "merged" {
		t.Fatalf("phase/landed = %s/%v, want merged/true", report.Phase, report.Landed)
	}
	if publisher.merged != 0 {
		t.Fatalf("externally merged pull request must not be merged again")
	}
}
