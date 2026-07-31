package config

import (
	"runtime"
	"strings"
	"testing"
)

func TestParseAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
model:
  provider: openai
  name: gpt-5
tools:
  - source: ./tools/echo
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if cfg.Version != CurrentVersion {
		t.Errorf("Version = %q, want %q", cfg.Version, CurrentVersion)
	}
	if cfg.Agent.Name != "goat-agent" {
		t.Errorf("Agent.Name = %q, want goat-agent", cfg.Agent.Name)
	}
	if cfg.Model.APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("Model.APIKeyEnv = %q, want OPENAI_API_KEY", cfg.Model.APIKeyEnv)
	}
	if cfg.Tools[0].Name != "echo" {
		t.Errorf("Tools[0].Name = %q, want echo", cfg.Tools[0].Name)
	}
	if cfg.Build.GOOS != runtime.GOOS || cfg.Build.GOARCH != runtime.GOARCH {
		t.Errorf("target = %s/%s, want %s/%s", cfg.Build.GOOS, cfg.Build.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	_, err := Parse([]byte(`
model:
  provider: openai
  name: gpt-5
  unknown: true
tools:
  - source: ./echo
`))
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("Parse() error = %v, want unknown field error", err)
	}
}

func TestParseRejectsDuplicateToolNames(t *testing.T) {
	_, err := Parse([]byte(`
model:
  provider: openai
  name: gpt-5
tools:
  - name: echo
    source: ./one
  - name: echo
    source: ./two
`))
	if err == nil || !strings.Contains(err.Error(), `duplicate tool name "echo"`) {
		t.Fatalf("Parse() error = %v, want duplicate tool error", err)
	}
}

func TestParseRejectsMultipleDocuments(t *testing.T) {
	_, err := Parse([]byte(`
model: {provider: openai, name: gpt-5}
tools: [{source: ./echo}]
---
model: {provider: openai, name: other}
`))
	if err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("Parse() error = %v, want multiple document error", err)
	}
}
