# Herdr Codex Cost

See estimated usage for each Codex pane in Herdr, including its nested
subagents. Choose API-equivalent dollar cost or Standard-speed credit estimates
calculated from local Codex session data. Dollar cost is the default.

## Quick start

You need:

- Herdr 0.9.5 or newer on Linux or macOS.
- Go 1.27 or newer on your `PATH`; Herdr builds the plugin during installation.
- Local Codex session data, normally in `~/.codex`.

Check your installed versions:

```sh
herdr --version
go version
```

If you need Go, use the [official downloads](https://go.dev/dl/) and
[installation guide](https://go.dev/doc/install).

Install the plugin:

```sh
herdr plugin install shaanardev/herdr-godex-cost
```

### Show costs in the sidebar

Herdr displays custom pane metadata only when the sidebar layout includes its
named token. Add `"$cost"` to the desired row in your Herdr config, normally
`~/.config/herdr/config.toml` (or `$XDG_CONFIG_HOME/herdr/config.toml`).
If you already have a Codex layout under `rows_by_agent`, add the token there.
Otherwise, this example keeps the standard information and adds a price row
for Codex panes:

```toml
[ui.sidebar.agents.rows_by_agent]
codex = [
  ["state_icon", "machine", "workspace", "tab"],
  ["agent"],
  ["$cost"],
]
```

Merge this into an existing table rather than declaring the table or `codex`
key twice. A per-agent layout replaces the shared rows, so preserve any other
fields you want to see. If `$cost` is already in the applicable layout, no
further configuration is needed.

Apply the change:

```sh
herdr server reload-config
```

The plugin publishes the selected estimate through the `cost` metadata token. Agent names, titles, and
status labels remain under Herdr's control. The price occupies its own row in
the example above. This follows Herdr's documented
[custom sidebar token configuration](https://herdr.dev/tui/docs/configuration/#sidebar-row-layouts).

Refreshes begin when Herdr reports a Codex status change. Panes already present
are initialized at the next Herdr server startup. For an existing idle pane,
focus it and invoke the refresh action to see an estimate immediately:

```sh
herdr plugin action invoke sarora.herdr-godex-cost.refresh
```

## Switch between dollars and credits

Once `$cost` is in your sidebar layout, switch all Codex panes using either
plugin action:

```sh
herdr plugin action invoke sarora.herdr-godex-cost.show-credits
herdr plugin action invoke sarora.herdr-godex-cost.show-cost
```

Both modes use the existing `$cost` sidebar field, so switching does not need a
Herdr config edit. The selection is saved in the plugin state directory and
read on every worker pass. Each action also refreshes existing Codex panes.

Credit mode displays values such as `~75.00 credits`. It uses published
[Standard credit rates](https://learn.chatgpt.com/docs/pricing#token-rates),
checked November 5, 2026, without API cache-write premiums. Rates are defined
in [credits.go](internal/codex/credits.go). Unsupported models, including
unpriced auto-review usage, are excluded and the result is marked `(partial)`.
Credit estimates do not include Fast/Ultrafast multipliers or account-specific
adjustments and do not measure your remaining subscription allowance.

For manual use, select the mode with the same state directory as your worker:

```sh
bin/herdr-godex-cost display --mode credit --state-dir /path/to/plugin/state
bin/herdr-godex-cost display --mode cost --state-dir /path/to/plugin/state
```

## Reading the estimate

| Display | Meaning |
| --- | --- |
| `$8.30` | Combined estimate from the available, priced usage. |
| `~$8.30` | Partial estimate; some data is missing, invalid, or unpriced. |
| `…` | No usage counters are available yet. |
| `session?` | The pane could not be matched to a Codex session. |

The total includes the parent session, nested subagents, and estimated
auto-review usage. Inherited fork counters are excluded from child costs.
Prices come from the local [model-rate table](internal/codex/rates.go), with
separate estimates for auto-review in [estimate.go](internal/codex/estimate.go).
Historical usage is valued using those rates, rather than a historical price
lookup.

Dollar mode estimates API-equivalent USD; credit mode estimates Standard-speed
credits. Neither is a billing record. Unknown models and incomplete logs can
produce partial totals.

While a pane is working, the plugin refreshes immediately and then waits five
seconds between passes. It makes a final pass when the pane stops working.
Unchanged complete dollar estimates reuse a local cache; incomplete dollar
estimates are retried.
The previous value is retained during a brief restart until new usage appears.

## Troubleshooting

- **Nothing appears:** check that the applicable sidebar layout contains
  `$cost`, reload the config, and confirm Herdr recognizes the pane as Codex.
  Then invoke the refresh action above.
- **`session?`:** check that the session data is available locally. The plugin
  uses reported session IDs, visible terminal text, title/directory evidence,
  and local process start times. The process fallback requires one top-level
  rollout in the same directory within one minute of the Codex process
  starting; it rejects ambiguous matches. Each session lookup also has a
  one-minute execution timeout, bounded by any shorter caller deadline. Resumed older sessions may still
  require Herdr’s Codex integration to report their session ID.
- **Partial totals:** inspect worker logs named `watch-*.log` in the plugin state
  directory. Unsupported model rates or damaged session data may be the cause.

Use [GitHub issues](https://github.com/shaanardev/herdr-godex-cost/issues) for bug
reports and questions. Include your Herdr and Go versions, the displayed value,
and relevant errors. Remove private session content before sharing logs.

## Development

From a local checkout:

```sh
go build -o bin/herdr-godex-cost ./cmd/herdr-godex-cost
herdr plugin link "$PWD"
go test ./...
go vet ./...
```

Run a single refresh or watch a pane:

```sh
bin/herdr-godex-cost refresh --pane PANE_ID
bin/herdr-godex-cost watch --pane PANE_ID --interval 5s
```

| Environment variable | Purpose |
| --- | --- |
| `CODEX_HOME` | Codex data directory; defaults to `~/.codex`. |
| `HERDR_BIN_PATH` | Herdr executable; defaults to `herdr`. |
| `HERDR_PLUGIN_STATE_DIR` | Cache, worker lock, and log directory supplied by Herdr. |

Manual runs default to a per-user state directory under the system temporary
directory. `--codex-home` and `--state-dir` override the corresponding directory
settings. Run `--help` for command options.

The [manifest](herdr-plugin.toml) defines the build, startup hook, refresh action,
and status-change handler. Tests exercise pricing, session discovery, caching,
and worker behavior with a fake Herdr executable. A live Herdr smoke test is
still needed to verify rendering and installation end to end.

Bug reports and pull requests are welcome. For behavior changes, describe the
problem and how you verified the fix.

## Acknowledgments and licensing

This Go implementation was developed with reference to
[Coolsik/herdr-codex-cost](https://github.com/Coolsik/herdr-codex-cost).
The [pricing test fixture](internal/codex/testdata/pricing.json) records its
source revision. This README describes this implementation in original wording.

This repository does not currently include a `LICENSE` file. The referenced
project also has no declared license as of November 5, 2026. Attribution alone
does not establish permission to redistribute adapted material; provenance and
reuse permissions need to be resolved before applying a project-wide open-source
license.
