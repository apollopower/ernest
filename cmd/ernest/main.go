package main

import (
	"context"
	"ernest/internal/agent"
	"ernest/internal/config"
	"ernest/internal/headless"
	"ernest/internal/provider"
	"ernest/internal/session"
	"ernest/internal/tools"
	"ernest/internal/tui"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// Parse flags
	prompt := flag.String("p", "", "Run a single prompt in headless mode and exit (use - for stdin)")
	headlessMode := flag.Bool("headless", false, "Run in headless conversational mode (stdin/stdout)")
	output := flag.String("output", "text", "Output format: text or json")
	autoApprove := flag.Bool("auto-approve", false, "Skip all tool confirmation dialogs (headless only)")
	resumeID := flag.String("resume", "", "Resume a session by ID")
	flag.Parse()

	// Validate flags
	isHeadless := *prompt != "" || *headlessMode
	if *prompt != "" && *headlessMode {
		fmt.Fprintf(os.Stderr, "error: -p and --headless are mutually exclusive\n")
		os.Exit(1)
	}
	if *autoApprove && !isHeadless {
		fmt.Fprintf(os.Stderr, "error: --auto-approve requires -p or --headless\n")
		os.Exit(1)
	}
	if *output == "json" && !isHeadless {
		fmt.Fprintf(os.Stderr, "error: --output json requires -p or --headless\n")
		os.Exit(1)
	}
	if *output != "text" && *output != "json" {
		fmt.Fprintf(os.Stderr, "error: --output must be 'text' or 'json'\n")
		os.Exit(1)
	}

	// Load config
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

	// Build providers
	var providers []provider.Provider
	for _, pc := range cfg.Providers {
		apiKey := pc.ResolveAPIKey()
		switch pc.Name {
		case "anthropic":
			providers = append(providers, provider.NewAnthropic(apiKey, pc.Model))
		}
	}
	if len(providers) == 0 {
		defaultCfg := config.DefaultConfig().Providers[0]
		providers = append(providers, provider.NewAnthropic(
			defaultCfg.ResolveAPIKey(), defaultCfg.Model,
		))
	}

	// Redirect log output to a file so it doesn't corrupt TUI/headless output
	logPath := filepath.Join(os.TempDir(), "ernest.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err == nil {
		log.SetOutput(logFile)
		defer logFile.Close()
	}

	// Register tools
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
	a := agent.New(router, registry, claudeCfg, cfg.MaxContextTokens, *autoApprove)

	// Session: resume or create new
	var sess *session.Session
	if *resumeID != "" {
		sess, err = session.LoadByID(*resumeID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		a.LoadSession(sess.Messages)
	} else {
		sess = session.New(cwd)
	}

	if isHeadless {
		runHeadless(a, sess, *prompt, headless.OutputFormat(*output))
	} else {
		runTUI(cfg, claudeCfg, a, sess)
	}
}

func runHeadless(a *agent.Agent, sess *session.Session, prompt string, format headless.OutputFormat) {
	// Signal handling for clean shutdown
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	runner := headless.NewRunner(a, sess, format, os.Stdout)
	defer runner.SaveSession()

	if prompt != "" {
		// One-shot mode
		promptText := prompt
		if prompt == "-" {
			// Read all stdin as the prompt
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
				os.Exit(1)
			}
			promptText = string(data)
		}
		if err := runner.RunPrompt(ctx, promptText); err != nil {
			os.Exit(1)
		}
		if ctx.Err() != nil {
			os.Exit(1) // interrupted by signal
		}
	} else {
		// Conversational mode
		if err := runner.RunConversation(ctx, os.Stdin); err != nil {
			if err != context.Canceled {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
	}
}

func runTUI(cfg config.Config, claudeCfg *config.ClaudeConfig, a *agent.Agent, sess *session.Session) {
	app := tui.NewAppModel(cfg, claudeCfg, a, sess)
	p := tea.NewProgram(app, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Auto-save session on exit
	sess.SetMessages(a.History())
	if err := sess.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to save session: %v\n", err)
	}
}
