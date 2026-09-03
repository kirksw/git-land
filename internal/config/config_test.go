package config

import "testing"

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
