# OpenAI Codex CLI

`prizmal codex` launches Codex against the Prizmal Switch. Codex takes its provider from a named profile in `~/.codex/config.toml`, so the launch writes that profile and a model catalog beside it, and puts the key on the child environment.

## Provider wiring

| What | Where |
|---|---|
| Profile, model, provider, catalog | `~/.codex/prizmal.config.toml`, written with a backup |
| Model metadata, context window | `~/.codex/model.json`, named by the profile's `model_catalog_json` |
| Credential | `model_providers.<profile>.auth` runs `prizmal auth token` in device mode; otherwise `OPENAI_API_KEY`, which the profile names with `env_key` |

The launch selects the profile with `--profile prizmal` and `--no-daemon`, and also passes the provider settings and the catalog path as `-c` overrides on the command line, so nothing app-visible in `~/.codex/config.toml` changes. It states the model with `-m`, so Codex runs the model prizmal resolved rather than one of its own.

Codex derives auto-compaction from the context window the catalog declares, 90% of it unless the entry sets `auto_compact_token_limit`, which prizmal doesn't emit. Every model the Switch serves has a 1M window, so the entry declares 1,000,000. Declaring a smaller window makes Codex discard conversation history the Switch would have accepted. `HARNESS_CONTEXT_LENGTH` overrides it for an operator who knows better.

## What the launch changes

Codex's own analytics and feedback upload are off, set with `-c analytics.enabled=false` and `-c feedback.enabled=false` so nothing new is written to your config. The launch also drops four variables inherited from your shell, because each one would send the session somewhere other than the Switch or sign it in as someone else: `OPENAI_BASE_URL`, `CODEX_API_KEY`, `CODEX_ACCESS_TOKEN` and `CODEX_HOME`.

By hand, pass the same two settings and unset those four variables:

```bash
env -u OPENAI_BASE_URL -u CODEX_API_KEY -u CODEX_ACCESS_TOKEN -u CODEX_HOME \
  OPENAI_API_KEY="$PRIZMAL_SWITCH_KEY" codex --profile prizmal \
  -c analytics.enabled=false -c feedback.enabled=false
```

## Device login

Codex refreshes a device token through its command-backed provider auth. On a machine signed in with a device key the launch writes a `[model_providers.<profile>.auth]` table naming this binary as the command, so Codex runs `prizmal auth token` for its bearer token instead of reading a fixed key. The table carries no credential, and the launch writes no `env_key` beside it: Codex rejects a provider that names both a key and a command.

Codex re-runs the command on the refresh interval prizmal sets, four minutes, so a token that expires ten minutes after issue is replaced with time in hand.

Launched without a device key, the profile names `OPENAI_API_KEY` with `env_key` and the launch sets that variable to the switch key, so nothing credential-shaped outlives the launched process. With no switch key, prizmal asks for one, as any unauthenticated launch does.

## Restore

```bash
prizmal --restore codex
```

Codex's restore removes the profile file and catalog the launch wrote and puts back any root config it touched. On a machine prizmal never configured, `--restore` reports that there is nothing to restore.

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

`prizmal codex` writes this profile and a model catalog beside it, and passes the provider settings and catalog path as `-c` overrides so your root `config.toml` stays as it was. The catalog makes Codex budget the 1M window the Switch serves instead of a default. It also installs Codex when the `codex` binary is missing.

Give Codex its system prompt by hand too: build the catalog with `codex debug models --bundled`, and copy the `base_instructions` and `model_messages` of the model you launched into its entry. A catalog entry without them sends Codex a session with no description of its tools, its sandbox or its editing rules.

For Codex's `/model` picker to list your whole tenant, point `model_catalog_json` at a catalog with one entry per model from `GET /v1/models`. Each entry needs `slug`, `display_name`, `description`, `supported_reasoning_levels` and a `priority`, where the lowest priority is the model Codex runs by default. Leave out the `claude-tier-*` aliases: those rows are for Claude Code's picker.
