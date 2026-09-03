package validation

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kirksw/git-land/internal/config"
)

type Result struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Passed  bool   `json:"passed"`
	Output  string `json:"output,omitempty"`
}

func Run(ctx context.Context, dir string, cfg config.Validation) []Result {
	checks := []struct{ name, command string }{
		{"pre-commit", commandForPreCommit(cfg.PreCommit)},
		{"format", cfg.Format}, {"lint", cfg.Lint}, {"test", cfg.Test}, {"build", cfg.Build},
	}
	results := make([]Result, 0, len(checks))
	for _, check := range checks {
		if check.command == "" {
			continue
		}
		cmd := exec.CommandContext(ctx, "sh", "-c", check.command)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		results = append(results, Result{Name: check.name, Command: check.command, Passed: err == nil, Output: strings.TrimSpace(string(output))})
		if err != nil {
			break
		}
	}
	return results
}

func AllPassed(results []Result) bool {
	for _, result := range results {
		if !result.Passed {
			return false
		}
	}
	return true
}

func commandForPreCommit(enabled bool) string {
	if !enabled {
		return ""
	}
	return "pre-commit run --all-files"
}

func FailedMessage(results []Result) error {
	for _, result := range results {
		if !result.Passed {
			return fmt.Errorf("%s failed: %s", result.Name, result.Command)
		}
	}
	return nil
}
