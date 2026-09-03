package config

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the repository-owned contract for landing a change.
type Config struct {
	Version     int         `yaml:"version"`
	Base        string      `yaml:"base"`
	Validation  Validation  `yaml:"validation"`
	Publish     Publish     `yaml:"publish"`
	PullRequest PullRequest `yaml:"pull_request"`
	Merge       Merge       `yaml:"merge"`
}

type Validation struct {
	PreCommit bool   `yaml:"pre_commit"`
	Format    string `yaml:"format"`
	Lint      string `yaml:"lint"`
	Test      string `yaml:"test"`
	Build     string `yaml:"build"`
}

type Publish struct {
	Strategy string `yaml:"strategy"`
}

type PullRequest struct {
	Draft bool `yaml:"draft"`
}

type Merge struct {
	Mode         string `yaml:"mode"`
	Method       string `yaml:"method"`
	DeleteBranch bool   `yaml:"delete_branch"`
}

func Load(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf("no landing policy at %s; run `land init` to create one", path)
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := Parse(contents)
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// Template renders a new, commented land.yaml for a repository.
// Empty lint or test omits that command; empty base defaults to "main".
func Template(base, lint, test, mergeMode string) []byte {
	if base == "" {
		base = "main"
	}
	if mergeMode == "" {
		mergeMode = "human"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "version: 1\nbase: %s\n\n", base)
	if lint == "" && test == "" {
		b.WriteString("# Commands run before publishing; each must pass.\n")
		b.WriteString("# validation:\n")
		b.WriteString("#   lint: go vet ./...\n")
		b.WriteString("#   test: go test ./...\n\n")
	} else {
		b.WriteString("# Commands run before publishing; each must pass.\nvalidation:\n")
		if lint != "" {
			fmt.Fprintf(&b, "  lint: %s\n", lint)
		}
		if test != "" {
			fmt.Fprintf(&b, "  test: %s\n", test)
		}
		b.WriteString("\n")
	}
	b.WriteString("publish:\n  strategy: pull_request\n\nmerge:\n")
	b.WriteString("  # human: land stops at ready_for_merge and a human merges.\n")
	b.WriteString("  # auto: land merges once every check passes (then set method and\n")
	b.WriteString("  # delete_branch below; they apply only to auto).\n")
	fmt.Fprintf(&b, "  mode: %s\n", mergeMode)
	if mergeMode == "auto" {
		b.WriteString("  # squash (default) | merge | rebase.\n")
		b.WriteString("  method: squash\n")
		b.WriteString("  # Delete the branch locally and remotely after an auto-merge.\n")
		b.WriteString("  delete_branch: true\n")
	}
	return []byte(b.String())
}

// Parse decodes and validates policy bytes.
// Unknown keys are rejected so typos and stale fields fail loudly instead
// of silently configuring nothing, and contradictory combinations are
// rejected so every parsed policy means what it says.
func Parse(contents []byte) (Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(contents, &raw); err != nil {
		return Config{}, err
	}
	if err := checkCombinations(cfg, raw); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// mergeKeys reports the merge keys the policy explicitly sets.
func mergeKeys(raw map[string]any) []string {
	section, ok := raw["merge"].(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(section))
	for key := range section {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// checkCombinations rejects settings that parse and type-check but have no
// effect: merge policy applies only to pull_request publication, and the
// auto-merge knobs apply only when merge.mode is auto.
func checkCombinations(cfg Config, raw map[string]any) error {
	merge := mergeKeys(raw)
	if cfg.Publish.Strategy == "direct_push" {
		if len(merge) > 0 {
			return fmt.Errorf("publish.strategy direct_push cannot be combined with merge settings (merge.%s): merge policy applies only to pull_request publication", strings.Join(merge, ", merge."))
		}
		return nil
	}
	var ineffective []string
	for _, key := range merge {
		if key == "method" || key == "delete_branch" {
			ineffective = append(ineffective, key)
		}
	}
	if cfg.MergeMode() != "auto" && len(ineffective) > 0 {
		return fmt.Errorf("merge.%s applies only when merge.mode is auto; remove it or set merge.mode: auto", strings.Join(ineffective, ", merge."))
	}
	return nil
}

func (c Config) Validate() error {
	if c.Version != 1 {
		return fmt.Errorf("unsupported land.yaml version %d (expected 1)", c.Version)
	}
	if c.Base == "" {
		return fmt.Errorf("land.yaml requires base")
	}
	switch c.Publish.Strategy {
	case "direct_push", "pull_request":
	default:
		return fmt.Errorf("unsupported publish.strategy %q", c.Publish.Strategy)
	}
	switch c.MergeMode() {
	case "human", "auto":
	default:
		return fmt.Errorf("unsupported merge.mode %q", c.Merge.Mode)
	}
	switch c.MergeMethod() {
	case "squash", "merge", "rebase":
	default:
		return fmt.Errorf("unsupported merge.method %q", c.Merge.Method)
	}
	return nil
}

// MergeMode reports who may merge: "human" (default) or "auto".
func (c Config) MergeMode() string {
	if c.Merge.Mode == "" {
		return "human"
	}
	return c.Merge.Mode
}

// MergeMethod reports the auto-merge method; "squash" is the default.
func (c Config) MergeMethod() string {
	if c.Merge.Method == "" {
		return "squash"
	}
	return c.Merge.Method
}
