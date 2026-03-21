# Ernest — Project Guide

## Values

Ernest's values are ranked. When two values conflict, the higher-ranked value wins. This ranking should guide every design decision, code review, and architecture choice.

1. **Resilience** — Work never stops. If a provider is down, fall back. If a stream breaks, show what we have. If config is missing, use sensible defaults. Ernest should be the tool you trust when everything else is broken.

2. **Performance** — Instant startup, streaming-first, no unnecessary allocations. The user should never wait for Ernest. A single static binary with no runtime dependencies. If something can be done at build time, don't do it at runtime.

3. **Terseness** — Every line of code must earn its place. If a feature can't justify itself in a sentence, it doesn't ship. Prefer short, direct implementations over clever abstractions. Three similar lines of code is better than a premature abstraction.

4. **Correctness** — The tool does what it says it does. Tool executions are faithful. Conversation history is accurate. Token counts are real. When something fails, say so clearly — never silently swallow errors or fabricate results.

5. **Portability** — Single binary, runs on Linux and macOS without runtime dependencies. Cross-compilation is a first-class build target. Avoid platform-specific code unless behind a build tag.

6. **Minimalism** — Build what's needed, nothing more. No plugin marketplaces, no config UIs, no extension points for hypothetical futures. Real capabilities (like MCP servers) are welcome when they serve a concrete need. The line is: don't build indirection layers or abstractions until a specific use case forces them.

## Conventions

- **Go 1.22+** with standard library preferred over third-party packages.
- **BubbleTea** (Elm architecture) for all TUI components.
- **No code without a plan.** Non-trivial changes require a plan file in `docs/plans/` before implementation begins.
- **Claude config is the source of truth.** `.claude/` directory drives behavior regardless of which model is active.
- Plans are never deleted — they serve as historical records of decisions made.
