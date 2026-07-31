// Package runtime starts an agent assembled by goatc.
package runtime

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/agenticclaude"
	"github.com/cloudwego/eino-ext/components/model/agenticgemini"
	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/components/model"
	"github.com/torrischen/goat/agent/contextmgr"
	filecontext "github.com/torrischen/goat/agent/contextmgr/file"
	"github.com/torrischen/goat/agent/contextmgr/ram"
	sqlitecontext "github.com/torrischen/goat/agent/contextmgr/sqlite"
	"github.com/torrischen/goat/agent/react"
	"github.com/torrischen/goat/goatc/config"
	"github.com/torrischen/goat/goatc/tui"
	"google.golang.org/genai"
)

// Run initializes and launches an agent from generated embedded assets.
func Run(ctx context.Context, assets fs.FS) error {
	data, err := fs.ReadFile(assets, "goatc.yaml")
	if err != nil {
		return fmt.Errorf("read embedded config: %w", err)
	}
	cfg, err := config.Parse(data)
	if err != nil {
		return err
	}
	llm, err := newModel(ctx, cfg.Model)
	if err != nil {
		return err
	}
	manager, err := newContextManager(cfg.Context)
	if err != nil {
		return err
	}
	agent := react.NewAgent(llm, cfg.Agent.ModelMaxTokensK, manager)

	pluginDir, err := extractPlugins(assets)
	if err != nil {
		return err
	}
	defer os.RemoveAll(pluginDir)
	if err := agent.LoadSharedLibPluginTools(ctx, pluginDir); err != nil {
		return fmt.Errorf("load plugins: %w", err)
	}

	return tui.Run(ctx, agent, cfg)
}

func newModel(ctx context.Context, cfg config.Model) (model.AgenticModel, error) {
	apiKey := os.Getenv(cfg.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("environment variable %s is required", cfg.APIKeyEnv)
	}
	maxTokens := cfg.MaxOutputTokens

	switch strings.ToLower(cfg.Provider) {
	case "openai":
		modelConfig := &agenticopenai.ResponsesConfig{
			APIKey:  apiKey,
			BaseURL: cfg.BaseURL,
			Model:   cfg.Name,
		}
		if maxTokens > 0 {
			modelConfig.MaxTokens = &maxTokens
		}
		return agenticopenai.NewResponsesModel(ctx, modelConfig)
	case "claude", "anthropic":
		if maxTokens <= 0 {
			maxTokens = 4096
		}
		return agenticclaude.New(ctx, &agenticclaude.Config{
			APIKey:    apiKey,
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Name,
			MaxTokens: maxTokens,
		})
	case "gemini":
		clientConfig := &genai.ClientConfig{APIKey: apiKey}
		if cfg.BaseURL != "" {
			clientConfig.HTTPOptions.BaseURL = cfg.BaseURL
		}
		client, err := genai.NewClient(ctx, clientConfig)
		if err != nil {
			return nil, fmt.Errorf("create Gemini client: %w", err)
		}
		modelConfig := &agenticgemini.Config{Client: client, Model: cfg.Name}
		if maxTokens > 0 {
			modelConfig.MaxTokens = &maxTokens
		}
		return agenticgemini.New(ctx, modelConfig)
	default:
		return nil, fmt.Errorf("unsupported model provider %q", cfg.Provider)
	}
}

func newContextManager(cfg config.Context) (contextmgr.ContextManager, error) {
	switch strings.ToLower(cfg.Backend) {
	case "ram":
		return ram.NewRAMContextManager(), nil
	case "file":
		return filecontext.NewFileContextManager(cfg.Path), nil
	case "sqlite":
		manager, err := sqlitecontext.NewSQLiteContextManager(cfg.Path)
		if err != nil {
			return nil, fmt.Errorf("create SQLite context manager: %w", err)
		}
		return manager, nil
	default:
		return nil, fmt.Errorf("unsupported context backend %q", cfg.Backend)
	}
}

func extractPlugins(assets fs.FS) (string, error) {
	dir, err := os.MkdirTemp("", "goatc-plugins-")
	if err != nil {
		return "", fmt.Errorf("create plugin cache: %w", err)
	}
	entries, err := fs.ReadDir(assets, "plugins")
	if err != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("read embedded plugins: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(assets, "plugins/"+entry.Name())
		if err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("read plugin %s: %w", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o700); err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("extract plugin %s: %w", entry.Name(), err)
		}
	}
	return dir, nil
}
