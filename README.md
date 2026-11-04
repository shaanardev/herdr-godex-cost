# Herdr Codex Cost

A Go port of the Herdr Codex cost plugin. It estimates API-equivalent dollar
costs for a pane's Codex session and descendant sessions, then publishes the
formatted result as the pane's `cost` token.

The Python watchdog in your home directory estimates subscription credits.
This plugin uses the dollar-cost behavior from the upstream plugin. It does not mix TUI credit totals with
API dollar estimates. Model rates are a local table in `internal/codex/rates.go`.

Requires Go 1.27 and Herdr on Linux or macOS.

```sh
go build -o bin/herdr-godex-cost ./cmd/herdr-godex-cost
herdr plugin link "$PWD"
```

The manifest initializes existing panes at startup and exposes a focused-pane
refresh action and an agent-status event hook. The event starts one worker per working Codex pane. It refreshes immediately,
waits five seconds after each working pass, and exits after refreshing the first
idle snapshot. Each pass fetches the pane once.
Workers use an OS file lock that releases automatically when the process exits.
Background worker errors go to `watch-*.log` in the state directory.
No separate service is needed. Add `$cost` to your Herdr sidebar configuration.

For development, run a single refresh or watch a pane in the foreground:

```sh
bin/herdr-godex-cost refresh --pane PANE_ID
bin/herdr-godex-cost watch --pane PANE_ID --interval 5s
```

`CODEX_HOME` selects the Codex data directory. `HERDR_BIN_PATH` selects the Herdr
executable. `HERDR_PLUGIN_STATE_DIR` selects the cache directory. The equivalent
command flags are `--codex-home` and `--state-dir`. Plugin actions read the pane
from `HERDR_PLUGIN_CONTEXT_JSON`; events read `HERDR_PLUGIN_EVENT_JSON`.
An explicit `--pane` overrides plugin context. Omitting the command runs
`refresh`. Run `--help` or `watch --help` for generated CLI help. Invalid
arguments exit with code 2; operational failures exit with code 1. Errors are
reported once by the entry point, and signal cancellation exits cleanly.

Session resolution tries the reported session ID, visible session IDs, and
terminal-title/working-directory evidence. Ambiguous matches are rejected.
All usable `state_*.sqlite` databases contribute sessions and spawn edges; rollout
metadata provides a fallback when databases are unavailable. Visible IDs are
verified against the pane working directory. Title fallback matches the exact
indexed thread name in `terminal_title_stripped` (`name | directory basename`).

Estimates display as `$1.23`, `~$1.23` for partial results, or
`$1.23(+$0.45)` when an auto-review estimate is included. `...` means no usage;
`session?` means the session could not be resolved. Existing values survive
brief restarts until new usage appears. Cache failures are reported but do not
prevent publishing. Incomplete estimates are retried rather than cached.

The code stays in four places:

- `cmd/herdr-godex-cost`: flags, signals, event workers.
- `internal/codex`: session files/database, JSONL parsing, pricing.
- `internal/herder`: Herdr subprocess commands and JSON responses.
- `internal/refresh`: resolution, descendant aggregation, caching, refresh loop.

The Herdr client acts as an adapter. Other operations use ordinary functions
and concrete structs; no additional design-pattern framework is needed.

```sh
go test ./...
go vet ./...
```

Tests cover accounting validation, model changes, resets, mixed cache-write
lifetimes, long-context pricing, malformed records, filesystem-only session
discovery, ambiguous matches, escaping symlinks, caching, and a refresh against
a fake Herdr executable. Live Herdr integration still needs a manual smoke test.

Intentional differences from upstream are the `~` partial-result marker and
stricter rejection of ambiguous visible IDs rather than choosing the last ID
shown. The port does not install or remove legacy Codex hooks. Linux process-start
matching, subscription-credit overrides, and TUI credit scraping from the
standalone watchdog are not included; this plugin estimates API-equivalent USD.

The fixture tests run without Python or network access. Built-binary integration
tests exercise startup/event worker spawning, lock arbitration, final publication,
and shutdown against a fake Herdr executable. Live Herdr still needs a manual
smoke test.
