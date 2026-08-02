package main

import (
	"testing"
)

// TestLoadConfigRightsAreOptIn pins down that every right defaults to off and
// is granted only by the exact string "true".
func TestLoadConfigRightsAreOptIn(t *testing.T) {
	vars := []struct {
		env string
		get func(Config) bool
	}{
		{"TG_ALLOW_SEND", func(c Config) bool { return c.AllowSend }},
		{"TG_ALLOW_PROFILE_EDIT", func(c Config) bool { return c.AllowProfileEdit }},
		{"TG_ALLOW_INLINE_MEDIA", func(c Config) bool { return c.AllowInlineMedia }},
	}
	values := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"true", true},
		{"false", false},
		{"1", false},
		{"TRUE", false},
	}

	for _, v := range vars {
		for _, val := range values {
			t.Run(v.env+"="+val.value, func(t *testing.T) {
				t.Setenv("APP_ID", "1")
				t.Setenv("APP_HASH", "hash")
				for _, other := range vars {
					t.Setenv(other.env, "")
				}
				t.Setenv(v.env, val.value)

				cfg, err := LoadConfig()
				if err != nil {
					t.Fatalf("LoadConfig: %v", err)
				}
				if got := v.get(cfg); got != val.want {
					t.Errorf("%s=%q granted=%v, want %v", v.env, val.value, got, val.want)
				}
			})
		}
	}
}
