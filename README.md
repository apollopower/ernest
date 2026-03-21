# Ernest

A terse, fast TUI for AI-assisted coding with multi-provider resilience and Claude config compatibility.

## What it does

Ernest is a terminal-based AI coding assistant. It connects to LLM providers (Anthropic, OpenAI, Gemini), streams responses in real time, and uses your existing `.claude/` configuration as the source of truth for system prompts, rules, and tool permissions.

Key traits:

- **Single binary, instant startup.** Built in Go with no runtime dependencies.
- **Multi-provider fallback.** Claude is preferred, but when it's down, work continues via other providers.
- **Claude config compatible.** Your `.claude/CLAUDE.md`, `settings.json`, and `rules/` directory drive behavior regardless of which model is active.
- **Vim-style TUI navigation.** `j`/`k` to scroll, `gg`/`G` to jump, `i` to focus input, `Esc` to navigate, `:q` to quit.

## Install

Download the binary for your platform from releases, or build from source:

```
make build          # native build to dist/ernest
make build-all      # cross-compile for linux, macOS ARM, macOS Intel
```

Put the binary in your `$PATH` and run `ernest` from any project directory.

## Configuration

Ernest reads its own config from `~/.config/ernest/config.yaml`:

```yaml
providers:
  - name: anthropic
    api_key_env: ANTHROPIC_API_KEY
    model: claude-opus-4-6
    priority: 1

  - name: openai
    api_key_env: OPENAI_API_KEY
    model: gpt-4.1
    priority: 2

cooldown_seconds: 30
max_context_tokens: 180000
```

If no config file exists, Ernest defaults to Anthropic with `claude-opus-4-6`.

## Claude config

Ernest reads the same `.claude/` directory structure that Claude Code uses:

- `~/.claude/CLAUDE.md` — user-global instructions
- `.claude/CLAUDE.md` — project instructions
- `.claude/settings.json` — tool permissions
- `.claude/rules/*.md` — project rules
- `CLAUDE.md` at repo root — legacy location

All of these are assembled into the system prompt sent to whichever provider is active.

## Values

Ernest's design decisions are guided by a ranked set of values. When two conflict, the higher-ranked value wins.

1. **Resilience** — work never stops, even when providers go down
2. **Performance** — instant startup, streaming-first, single binary
3. **Terseness** — every line of code earns its place
4. **Correctness** — the tool does what it says it does
5. **Portability** — runs on Linux, macOS, and Windows without runtime dependencies
6. **Minimalism** — build what's needed, nothing more

## License

[MIT](LICENSE)
