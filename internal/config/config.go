package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the repository-owned contract for landing a change.
type Config struct {
	Version     int         `yaml:"version"`
	Base        string      `yaml:"base"`
	Commits     Commits     `yaml:"commits"`
	Validation  Validation  `yaml:"validation"`
	Publish     Publish     `yaml:"publish"`
	PullRequest PullRequest `yaml:"pull_request"`
	Merge       Merge       `yaml:"merge"`
}

type Commits struct {
	Style     string `yaml:"style"`
	Structure string `yaml:"structure"`
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
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := Parse(contents)
	if err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes and validates policy bytes.
func Parse(contents []byte) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(contents, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
