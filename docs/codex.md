# OpenAI Codex CLI

`prizmal codex` launches Codex against the Prizmal Switch. The launch passes the provider as `-c` overrides, writes a model catalog to a temporary file for the length of the session, and puts the key on the child environment. It writes nothing to `~/.codex`.

`prizmal codex` needs Codex 0.160.0 or newer and stops with an error on an older release. The launch uses `--no-daemon`, command-backed provider auth, `codex debug models --bundled` and the `supports_search_tool`, `apply_patch_tool_type` and `agents.default_subagent_model` settings, and earlier releases do not know them.

## Provider wiring

| What | Where |
|---|---|
| Model, provider, endpoint | `-c` overrides and `-m` on the command line |
| Model metadata, context window | a `model.json` in a temporary directory, named by `-c model_catalog_json=...` and removed when Codex exits |
| Credential | `model_providers.prizmal.auth` runs `prizmal auth token` in device mode. Otherwise `OPENAI_API_KEY`, which `-c model_providers.prizmal.env_key` names |

The launch passes `--no-daemon` and the provider settings as `-c` overrides. Nothing app-visible in `~/.codex/config.toml` changes, and `CODEX_HOME` makes no difference to the launch. It states the model with `-m`, so Codex runs the model prizmal resolved rather than one of its own.

Codex's `/model` lists the same rows as the prizmal picker, in the same order and with the same labels and descriptions: a tier alias is a row, and the model folded into it is not a second one. Each catalog entry sets `display_name` to the row's label and `description` to its text. A model you launch with `-m` that no row shows still gets an entry, because Codex reads it for the model's window and prompt.

Each catalog entry sets `supports_search_tool`, which makes Codex defer its less-used tools behind a `tool_search` tool, as `ENABLE_TOOL_SEARCH=true` does for Claude Code. A manual setup that writes its own catalog sets the same field on each entry. It needs a Switch that supports Codex's tool search.

Codex derives auto-compaction from the context window the catalog declares, 90% of it unless the entry sets `auto_compact_token_limit`, which prizmal doesn't emit. Every model the Switch serves has a 1M window, so the entry declares 1,000,000. Declaring a smaller window makes Codex discard conversation history the Switch would have accepted. `HARNESS_CONTEXT_LENGTH` overrides it for an operator who knows better.

## Subagent model

Subagents run the session model by default. `--subagent-model` gives them a different one. The launch passes it to Codex twice: as `agents.default_subagent_model`, the model a spawned subagent runs when the spawn call names none, and as `review_model`, the model `/review` runs. A spawn call that names its own model still wins. Codex checks the model against the catalog, so the launch lists it in the catalog even when the Switch's model list lacks it.

## Edit tool

Codex sends its freeform `apply_patch` edit tool only for a model whose catalog entry sets `apply_patch_tool_type` to `"freeform"`. The entry the launch writes sets it, so Codex edits files through `apply_patch` instead of shell commands. The Switch translates the tool for models that can't take a freeform tool.

## Device login

Codex refreshes a device token through its command-backed provider auth. On a machine signed in with a device key the launch passes `model_providers.prizmal.auth` overrides naming this binary as the command, so Codex runs `prizmal auth token` for its bearer token instead of reading a fixed key. The overrides carry no credential, and the launch passes no `env_key` beside them: Codex rejects a provider that names both a key and a command.

Codex re-runs the command every four minutes, the interval prizmal sets. A device token expires ten minutes after issue and Codex's own default refresh is five, which would refresh with only five minutes still in hand; four keeps the margin a laptop sleep spends.

Launched without a device key, the launch names `OPENAI_API_KEY` with `env_key` and the launch sets that variable to the switch key, so nothing credential-shaped outlives the launched process. With no switch key, prizmal asks for one, as any unauthenticated launch does.

## Restore

```bash
prizmal --restore codex
```

A launch writes no files, so there is nothing new to restore. Codex's restore removes the profile file and catalog that an older prizmal wrote to `~/.codex`. On a machine prizmal never configured, `--restore` reports that there is nothing to restore.

## Run Codex without the Prizmal CLI

Write a profile file at `~/.codex/prizmal.config.toml`, export your switch key as `PRIZMAL_SWITCH_KEY`, and launch Codex on that profile:

```toml
model_provider = "prizmal"
model = "your-model"

[model_providers.prizmal]
name = "prizmal"
base_url = "https://api.prizmal.ai/v1"
wire_api = "responses"
env_key = "OPENAI_API_KEY"
```

```bash
export PRIZMAL_SWITCH_KEY=sk-...
OPENAI_API_KEY="$PRIZMAL_SWITCH_KEY" codex --no-daemon --profile prizmal -m your-model
```

Codex runs without its shared background server whenever a launch passes `--profile` or `-c`, and it shows "1 warning" in its footer for that. `--no-daemon` states the choice, so the warning goes away.

`env_key` names the variable Codex reads the key from, so the key itself never enters the file.

To give subagents their own model by hand, add `-c agents.default_subagent_model="your-subagent-model"` and `-c review_model="your-subagent-model"` to the `codex` command, and list that model in the catalog file that `model_catalog_json` names. Codex rejects a subagent model its catalog doesn't list.

To get the `apply_patch` edit tool by hand, set `"apply_patch_tool_type": "freeform"` on your model's entry in the catalog file that `model_catalog_json` names.

`prizmal codex` writes no profile. It passes the same provider settings as `-c` overrides and a model catalog from a temporary file, so your `~/.codex` stays as it was. The catalog makes Codex budget the 1M window the Switch serves instead of a default. It also installs Codex when the `codex` binary is missing.
