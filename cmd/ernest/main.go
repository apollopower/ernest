package main

import (
	"ernest/internal/agent"
	"ernest/internal/config"
	"ernest/internal/provider"
	"ernest/internal/tools"
	"ernest/internal/tui"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load config: %v\n", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	claudeCfg, err := config.LoadClaudeConfig(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to load claude config: %v\n", err)
		claudeCfg = &config.ClaudeConfig{}
	}

	// Build providers from config
	var providers []provider.Provider
	for _, pc := range cfg.Providers {
		apiKey := pc.ResolveAPIKey()
		switch pc.Name {
		case "anthropic":
			providers = append(providers, provider.NewAnthropic(apiKey, pc.Model))
		}
	}

	if len(providers) == 0 {
		// Fallback: create default Anthropic provider
		defaultCfg := config.DefaultConfig().Providers[0]
		providers = append(providers, provider.NewAnthropic(
			defaultCfg.ResolveAPIKey(), defaultCfg.Model,
		))
	}

	// Redirect log output to a file so it doesn't corrupt the TUI alt screen
	logPath := filepath.Join(os.TempDir(), "ernest.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	// Register all tools. Write tools (write_file, str_replace, bash) require
	// confirmation via the TUI dialog before execution.
	registry := tools.NewRegistry(
		&tools.ReadFileTool{},
		&tools.WriteFileTool{},
		&tools.StrReplaceTool{},
		&tools.BashTool{},
		&tools.GlobTool{},
		&tools.GrepTool{},
	)

	cooldown := time.Duration(cfg.CooldownSeconds) * time.Second
	router := provider.NewRouter(providers, cooldown)
	a := agent.New(router, registry, claudeCfg)

	app := tui.NewAppModel(cfg, claudeCfg, a)
	p := tea.NewProgram(app, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
