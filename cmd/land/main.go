package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"github.com/kirksw/git-land/internal/app"
	"github.com/kirksw/git-land/internal/config"
	"github.com/kirksw/git-land/internal/forge"
	"github.com/spf13/cobra"
)

// version is overridden at build time via
// -ldflags "-X main.version=v0.1" so pinned releases carry their tag and
// nightlies carry a date-and-revision stamp.
var version string

// resolvedVersion prefers the injected version, then module version, then
// the source revision recorded by VCS build stamping.
func resolvedVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && len(setting.Value) >= 7 {
				return "dev-" + setting.Value[:7]
			}
		}
	}
	return "unknown"
}

type options struct {
	config      string
	json        bool
	noWait      bool
	waitTimeout time.Duration
}

func main() {
	var opts options
	root := &cobra.Command{
		Use:     "land",
		Version: resolvedVersion(),
		Short:   "Land repository changes according to land.yaml",
		Long:    "Land advances the current branch toward landed as far as policy allows: synchronize, validate, publish, verify. Rerun until phase reports landed or ready_for_merge.",
		RunE:    func(cmd *cobra.Command, args []string) error { return land(cmd, opts) },
	}
	root.PersistentFlags().StringVar(&opts.config, "config", "", "path to landing policy (default: ./land.yaml)")
	root.PersistentFlags().BoolVar(&opts.json, "json", false, "write structured JSON output")
	root.Flags().BoolVar(&opts.noWait, "no-wait", false, "report the first continuous-integration observation instead of polling until checks settle")
	root.Flags().DurationVar(&opts.waitTimeout, "wait-timeout", 30*time.Minute, "maximum time to poll continuous integration before reporting ci_pending")
	root.AddCommand(&cobra.Command{Use: "status", Short: "Inspect the current landing state", RunE: func(cmd *cobra.Command, args []string) error {
		report, err := (app.Service{}).Status(".", opts.config)
		writeIfAvailable(opts.json, report)
		return err
	}})
	root.AddCommand(&cobra.Command{Use: "validate", Short: "Run repository validation", RunE: func(cmd *cobra.Command, args []string) error {
		report, err := (app.Service{}).Validate(context.Background(), ".", opts.config)
		writeIfAvailable(opts.json, report)
		return err
	}})
	root.AddCommand(&cobra.Command{Use: "verify", Short: "Report pull-request and CI status without mutating anything", RunE: func(cmd *cobra.Command, args []string) error {
		report, err := (app.Service{}).Verify(context.Background(), ".", opts.config)
		writeIfAvailable(opts.json, report)
		return err
	}})
	var dryRun bool
	submitCmd := &cobra.Command{Use: "submit", Short: "Validate and publish the current branch", RunE: func(cmd *cobra.Command, args []string) error { return submit(cmd, opts, dryRun) }}
	submitCmd.Flags().BoolVar(&dryRun, "dry-run", false, "validate and show eligibility without publishing")
	root.AddCommand(submitCmd)
	root.AddCommand(initCmd(&opts))
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func initCmd(opts *options) *cobra.Command {
	var (
		base  string
		lint  string
		test  string
		merge string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter land.yaml for this repository",
		Long:  "init detects sensible defaults (integration base, Go validation commands) and writes a commented land.yaml. Review it, commit it, then run land.",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := opts.config
			if path == "" {
				path = "land.yaml"
			}
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists; review it or rerun with --force to overwrite", path)
			}
			if base == "" {
				base = detectBase()
			}
			if lint == "" && test == "" {
				if _, err := os.Stat("go.mod"); err == nil {
					lint, test = "go vet ./...", "go test ./..."
				}
			}
			if merge == "" {
				merge = "human"
			}
			if merge != "human" && merge != "auto" {
				return fmt.Errorf("unsupported merge mode %q (human or auto)", merge)
			}
			contents := config.Template(base, lint, test, merge)
			if err := os.WriteFile(path, contents, 0o644); err != nil {
				return err
			}
			writeInit(opts.json, path, base, lint, test, merge)
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "integration base branch (default: detected)")
	cmd.Flags().StringVar(&lint, "lint", "", "lint command recorded in land.yaml (default: go vet for Go modules)")
	cmd.Flags().StringVar(&test, "test", "", "test command recorded in land.yaml (default: go test for Go modules)")
	cmd.Flags().StringVar(&merge, "merge", "human", "merge policy: human or auto")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing land.yaml")
	return cmd
}

// detectBase prefers the remote default branch, then init.defaultBranch,
// then "main"; init outside a repository still succeeds with that default.
func detectBase() string {
	if out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "origin/HEAD").Output(); err == nil {
		if ref := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/"); ref != "" && ref != "HEAD" {
			return ref
		}
	}
	if out, err := exec.Command("git", "config", "--get", "init.defaultBranch").Output(); err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" {
			return branch
		}
	}
	return "main"
}

func writeInit(asJSON bool, path, base, lint, test, merge string) {
	hint := "review land.yaml (especially merge.mode), commit it, then run land"
	if asJSON {
		fmt.Printf("{\n  \"configPath\": %q,\n  \"base\": %q,\n  \"lint\": %q,\n  \"test\": %q,\n  \"mergeMode\": %q,\n  \"hint\": %q\n}\n", path, base, lint, test, merge, hint)
		return
	}
	fmt.Printf("Wrote %s\n", path)
	fmt.Printf("  base: %s\n", base)
	if lint != "" || test != "" {
		fmt.Printf("  validation: %s | %s\n", lint, test)
	} else {
		fmt.Printf("  validation: none detected; see the commented examples in land.yaml\n")
	}
	fmt.Printf("  merge: %s\n", merge)
	fmt.Printf("Next: %s\n", hint)
}

func land(cmd *cobra.Command, opts options) error {
	report, err := (app.Service{}).Land(context.Background(), ".", opts.config, app.Wait{
		Enabled:  !opts.noWait,
		Timeout:  opts.waitTimeout,
		Interval: 15 * time.Second,
		Progress: progressPrinter(opts.json),
	})
	writeIfAvailable(opts.json, report)
	return err
}

// progressPrinter streams one observation per poll while land waits: to
// stdout in text mode, to stderr in JSON mode so stdout stays a single
// machine-readable report document.
func progressPrinter(asJSON bool) func(time.Duration, forge.PullRequest) {
	out := io.Writer(os.Stdout)
	if asJSON {
		out = os.Stderr
	}
	return func(elapsed time.Duration, pr forge.PullRequest) {
		states := make([]string, 0, len(pr.Checks))
		for _, check := range pr.Checks {
			states = append(states, check.Name+"="+check.State)
		}
		observation := strings.Join(states, " ")
		if observation == "" {
			observation = "no checks reported yet"
		}
		fmt.Fprintf(out, "Waiting for CI (%s): %s\n", elapsed.Truncate(time.Second), observation)
	}
}

func submit(cmd *cobra.Command, opts options, dryRun bool) error {
	report, err := (app.Service{}).Submit(context.Background(), ".", opts.config, dryRun)
	writeIfAvailable(opts.json, report)
	return err
}

func writeIfAvailable(asJSON bool, report app.Report) {
	if report.ConfigPath == "" {
		return
	}
	write(asJSON, report)
}

func write(asJSON bool, report app.Report) {
	if asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(report)
		return
	}
	state := report.State
	if state.Branch == "" {
		return
	}
	fmt.Printf("Repository: %s\nBranch: %s\nBase: %s\n", state.Root, state.Branch, state.BaseRef)
	fmt.Printf("Changes: %d staged, %d unstaged, %d untracked\n", len(state.Staged), len(state.Unstaged), len(state.Untracked))
	fmt.Printf("Divergence: %d ahead / %d behind %s\n", state.BaseAhead, state.BaseBehind, state.BaseRef)
	for _, result := range report.Validation {
		status := "ok"
		if !result.Passed {
			status = "failed"
		}
		fmt.Printf("Validation: %s (%s)\n", result.Name, status)
	}
	if report.Phase != "" {
		fmt.Printf("Phase: %s\n", report.Phase)
	}
	if report.BlockedOn != "" {
		fmt.Printf("Blocked on: %s\n", report.BlockedOn)
	}
	if report.Hint != "" {
		fmt.Printf("Next: %s\n", report.Hint)
	}
	for _, check := range report.Checks {
		fmt.Printf("Check: %s (%s)\n", check.Name, check.State)
	}
	if report.Published {
		fmt.Printf("Published: origin/%s\n", state.Branch)
	}
	if report.PullRequestURL != "" {
		fmt.Printf("Pull request: %s\n", report.PullRequestURL)
	}
	if report.MergeState != "" {
		fmt.Printf("Merge state: %s\n", report.MergeState)
	}
	if report.Landed {
		fmt.Printf("Landed: yes\n")
	}
}
