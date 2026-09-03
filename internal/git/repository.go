package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

type Repository struct{ Dir string }

type State struct {
	Root       string   `json:"root"`
	Branch     string   `json:"branch"`
	Base       string   `json:"base"`
	BaseRef    string   `json:"baseRef"`
	Remote     string   `json:"remote"`
	Staged     []string `json:"staged"`
	Unstaged   []string `json:"unstaged"`
	Untracked  []string `json:"untracked"`
	Ahead      int      `json:"ahead"`
	Behind     int      `json:"behind"`
	BaseAhead  int      `json:"baseAhead"`
	BaseBehind int      `json:"baseBehind"`
}

func Open(dir string) (Repository, error) {
	r := Repository{Dir: dir}
	root, err := r.run("rev-parse", "--show-toplevel")
	if err != nil {
		return Repository{}, fmt.Errorf("not a Git worktree: %w", err)
	}
	r.Dir = root
	return r, nil
}

func (r Repository) Inspect(base string) (State, error) {
	root, err := r.run("rev-parse", "--show-toplevel")
	if err != nil {
		return State{}, err
	}
	branch, err := r.run("branch", "--show-current")
	if err != nil {
		return State{}, err
	}
	if branch == "" {
		return State{}, fmt.Errorf("HEAD is detached; check out a branch before landing")
	}
	remote, err := r.run("remote", "get-url", "origin")
	if err != nil {
		return State{}, fmt.Errorf("origin remote is required: %w", err)
	}
	baseRef := "origin/" + base
	if _, err := r.run("rev-parse", "--verify", baseRef); err != nil {
		return State{}, fmt.Errorf("base %s is unavailable; run a fetch or check config: %w", baseRef, err)
	}
	upstream := "origin/" + branch
	ahead, behind := r.counts(upstream, "HEAD")
	baseAhead, baseBehind := r.counts(baseRef, "HEAD")
	staged, unstaged, untracked, err := r.changes()
	if err != nil {
		return State{}, err
	}
	return State{Root: root, Branch: branch, Base: base, BaseRef: baseRef, Remote: remote, Staged: staged, Unstaged: unstaged, Untracked: untracked, Ahead: ahead, Behind: behind, BaseAhead: baseAhead, BaseBehind: baseBehind}, nil
}

func (r Repository) Fetch() error { _, err := r.run("fetch", "--prune", "origin"); return err }
func (r Repository) Push(branch string) error {
	_, err := r.run("push", "--set-upstream", "origin", branch)
	return err
}
func (r Repository) IsAncestor(ancestor, descendant string) (bool, error) {
	_, err := r.run("merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func (r Repository) counts(left, right string) (int, int) {
	out, err := r.run("rev-list", "--left-right", "--count", left+"..."+right)
	if err != nil {
		return 0, 0
	}
	var leftCount, rightCount int
	fmt.Sscanf(out, "%d\t%d", &leftCount, &rightCount)
	return rightCount, leftCount
}

func (r Repository) changes() ([]string, []string, []string, error) {
	out, err := r.run("status", "--porcelain=v1")
	if err != nil {
		return nil, nil, nil, err
	}
	var staged, unstaged, untracked []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		path := line[3:]
		if line[:2] == "??" {
			untracked = append(untracked, path)
			continue
		}
		if line[0] != ' ' {
			staged = append(staged, path)
		}
		if line[1] != ' ' {
			unstaged = append(unstaged, path)
		}
	}
	return staged, unstaged, untracked, nil
}

func (r Repository) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
