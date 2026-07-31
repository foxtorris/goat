// Package config defines the goatc build and runtime configuration.
package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// CurrentVersion is the YAML schema version understood by goatc.
const CurrentVersion = "v1"

// Config is the complete goatc build and runtime configuration.
type Config struct {
	Version string  `yaml:"version"`
	Agent   Agent   `yaml:"agent"`
	Model   Model   `yaml:"model"`
	Context Context `yaml:"context,omitempty"`
	Tools   []Tool  `yaml:"tools"`
	Build   Build   `yaml:"build,omitempty"`
	TUI     TUI     `yaml:"tui,omitempty"`
}

// Agent configures the generated agent loop.
type Agent struct {
	Name                string   `yaml:"name"`
	ModelMaxTokensK     int      `yaml:"model_max_tokens_k,omitempty"`
	MaxSteps            int      `yaml:"max_steps,omitempty"`
	EnablePlanning      bool     `yaml:"enable_planning,omitempty"`
	ParallelTools       int      `yaml:"parallel_tools,omitempty"`
	Compress            bool     `yaml:"compress,omitempty"`
	SpecialRequirements []string `yaml:"special_requirements,omitempty"`
}

// Model configures the generated agent's model provider.
type Model struct {
	Provider        string `yaml:"provider"`
	Name            string `yaml:"name"`
	APIKeyEnv       string `yaml:"api_key_env"`
	BaseURL         string `yaml:"base_url,omitempty"`
	MaxOutputTokens int    `yaml:"max_output_tokens,omitempty"`
}

// Context configures conversation persistence.
type Context struct {
	Backend string `yaml:"backend,omitempty"`
	Path    string `yaml:"path,omitempty"`
}

// Tool describes one Go plugin build unit.
type Tool struct {
	Name   string `yaml:"name,omitempty"`
	Source string `yaml:"source"`
}

// Build configures the output artifact.
type Build struct {
	Output string   `yaml:"output,omitempty"`
	GOOS   string   `yaml:"goos,omitempty"`
	GOARCH string   `yaml:"goarch,omitempty"`
	Tags   []string `yaml:"tags,omitempty"`
}

// TUI configures the generated terminal interface.
type TUI struct {
	Welcome string `yaml:"welcome,omitempty"`
}

// Load reads and validates a YAML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data)
}

// Parse decodes and validates a YAML configuration.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode config: multiple YAML documents are not allowed")
		}
		return nil, fmt.Errorf("decode config: %w", err)
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) setDefaults() {
	if c.Version == "" {
		c.Version = CurrentVersion
	}
	if c.Agent.Name == "" {
		c.Agent.Name = "goat-agent"
	}
	if c.Agent.ModelMaxTokensK <= 0 {
		c.Agent.ModelMaxTokensK = 128
	}
	if c.Agent.MaxSteps <= 0 {
		c.Agent.MaxSteps = 8
	}
	if c.Model.APIKeyEnv == "" {
		switch strings.ToLower(c.Model.Provider) {
		case "openai":
			c.Model.APIKeyEnv = "OPENAI_API_KEY"
		case "claude", "anthropic":
			c.Model.APIKeyEnv = "ANTHROPIC_API_KEY"
		case "gemini":
			c.Model.APIKeyEnv = "GEMINI_API_KEY"
		}
	}
	if c.Context.Backend == "" {
		c.Context.Backend = "file"
	}
	if c.Build.Output == "" {
		c.Build.Output = c.Agent.Name
	}
	if c.Build.GOOS == "" {
		c.Build.GOOS = runtime.GOOS
	}
	if c.Build.GOARCH == "" {
		c.Build.GOARCH = runtime.GOARCH
	}
}

func (c *Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported config version %q", c.Version)
	}
	if strings.TrimSpace(c.Agent.Name) == "" {
		return fmt.Errorf("agent.name is required")
	}
	if c.Model.Name == "" {
		return fmt.Errorf("model.name is required")
	}
	switch strings.ToLower(c.Model.Provider) {
	case "openai", "claude", "anthropic", "gemini":
	default:
		return fmt.Errorf("unsupported model.provider %q", c.Model.Provider)
	}
	if c.Model.APIKeyEnv == "" {
		return fmt.Errorf("model.api_key_env is required")
	}
	if c.Model.MaxOutputTokens < 0 {
		return fmt.Errorf("model.max_output_tokens cannot be negative")
	}
	if len(c.Tools) == 0 {
		return fmt.Errorf("at least one tool is required")
	}
	seen := make(map[string]struct{}, len(c.Tools))
	for i := range c.Tools {
		tool := &c.Tools[i]
		if tool.Source == "" {
			return fmt.Errorf("tools[%d].source is required", i)
		}
		if tool.Name == "" {
			tool.Name = filepath.Base(filepath.Clean(tool.Source))
		}
		if !validFileName(tool.Name) {
			return fmt.Errorf("tools[%d].name %q is not a valid file name", i, tool.Name)
		}
		if _, ok := seen[tool.Name]; ok {
			return fmt.Errorf("duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = struct{}{}
	}
	if c.Agent.ParallelTools < 0 {
		return fmt.Errorf("agent.parallel_tools cannot be negative")
	}
	switch strings.ToLower(c.Context.Backend) {
	case "ram", "file", "sqlite":
	default:
		return fmt.Errorf("unsupported context.backend %q", c.Context.Backend)
	}
	switch runtime.GOOS {
	case "darwin", "freebsd", "linux":
	default:
		return fmt.Errorf("Go plugins are not supported on %s", runtime.GOOS)
	}
	if c.Build.GOOS != runtime.GOOS || c.Build.GOARCH != runtime.GOARCH {
		return fmt.Errorf("Go plugins must be built natively: target is %s/%s, host is %s/%s", c.Build.GOOS, c.Build.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	return nil
}

func validFileName(name string) bool {
	if name == "." || name == ".." || strings.TrimSpace(name) == "" {
		return false
	}
	return !strings.ContainsAny(name, `/\\\x00`)
}
