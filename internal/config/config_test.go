package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	valid := Config{Version: 1, Base: "main", Publish: Publish{Strategy: "pull_request"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for _, test := range []Config{
		{Version: 2, Base: "main", Publish: Publish{Strategy: "pull_request"}},
		{Version: 1, Publish: Publish{Strategy: "pull_request"}},
		{Version: 1, Base: "main", Publish: Publish{Strategy: "queue"}},
		{Version: 1, Base: "main", Publish: Publish{Strategy: "pull_request"}, Merge: Merge{Mode: "robot"}},
		{Version: 1, Base: "main", Publish: Publish{Strategy: "pull_request"}, Merge: Merge{Method: "bullet"}},
	} {
		if err := test.Validate(); err == nil {
			t.Fatalf("expected configuration failure for %#v", test)
		}
	}
}

func TestMergeDefaults(t *testing.T) {
	cfg := Config{Version: 1, Base: "main", Publish: Publish{Strategy: "pull_request"}}
	if cfg.MergeMode() != "human" {
		t.Fatalf("default merge mode = %q, want human", cfg.MergeMode())
	}
	if cfg.MergeMethod() != "squash" {
		t.Fatalf("default merge method = %q, want squash", cfg.MergeMethod())
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaulted config: %v", err)
	}
}

func TestTemplateParses(t *testing.T) {
	cases := []struct {
		name      string
		base      string
		lint      string
		test      string
		mergeMode string
	}{
		{name: "go defaults", base: "main", lint: "go vet ./...", test: "go test ./...", mergeMode: "human"},
		{name: "empty strings fall back", base: "", lint: "", test: "", mergeMode: ""},
		{name: "auto mode", base: "trunk", lint: "npm run lint", test: "npm test", mergeMode: "auto"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse(Template(tc.base, tc.lint, tc.test, tc.mergeMode))
			if err != nil {
				t.Fatalf("template must parse: %v", err)
			}
			if cfg.Base != firstNonEmpty(tc.base, "main") {
				t.Errorf("base = %q, want %q", cfg.Base, firstNonEmpty(tc.base, "main"))
			}
			if cfg.MergeMode() != firstNonEmpty(tc.mergeMode, "human") {
				t.Errorf("merge mode = %q", cfg.MergeMode())
			}
			if cfg.Validation.Lint != tc.lint && tc.lint != "" {
				t.Errorf("lint = %q, want %q", cfg.Validation.Lint, tc.lint)
			}
		})
	}
}

func TestTemplateCommentedValidationOmitsKeys(t *testing.T) {
	cfg, err := Parse(Template("main", "", "", ""))
	if err != nil {
		t.Fatalf("template must parse: %v", err)
	}
	if cfg.Validation.Lint != "" || cfg.Validation.Test != "" {
		t.Errorf("commented-out validation must stay unset, got lint=%q test=%q", cfg.Validation.Lint, cfg.Validation.Test)
	}
}

func TestLoadMissingFileHintsAtInit(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "land.yaml"))
	if err == nil || !strings.Contains(err.Error(), "land init") {
		t.Fatalf("missing policy must hint at land init, got: %v", err)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
