# OpenAI Codex CLI

`prizmal codex` launches Codex against the Prizmal Switch. Codex takes its provider from a named profile in `~/.codex/config.toml`, so the launch writes that profile and a model catalog beside it, and puts the key on the child environment.

## Provider wiring

| What | Where |
|---|---|
| Profile, model, provider, catalog | `~/.codex/prizmal.config.toml`, written with a backup |
| Model metadata, context window | `~/.codex/model.json`, named by the profile's `model_catalog_json` |
| Credential | `OPENAI_API_KEY`, which the profile names with `env_key` |

The launch selects the profile with `--profile prizmal` and also passes the provider settings and the catalog path as `-c` overrides on the command line, so nothing app-visible in `~/.codex/config.toml` changes. It states the model with `-m`, so Codex runs the model prizmal resolved rather than one of its own.

Codex derives auto-compaction from the context window the catalog declares, 90% of it unless the entry sets `auto_compact_token_limit`, which prizmal doesn't emit. Every model the Switch serves has a 1M window, so the entry declares 1,000,000. Declaring a smaller window makes Codex discard conversation history the Switch would have accepted. `HARNESS_CONTEXT_LENGTH` overrides it for an operator who knows better.

## Edit tool

Codex sends its freeform `apply_patch` edit tool only for a model whose catalog entry sets `apply_patch_tool_type` to `"freeform"`. The entry the launch writes sets it, so Codex edits files through `apply_patch` instead of shell commands. The Switch translates the tool for models that can't take a freeform tool.

## Device login

Codex can't refresh a device token during a session: it reads `OPENAI_API_KEY` once, and a device token expires ten minutes after issue. So a `prizmal codex` launch on a machine signed in with a device key ignores device login and runs on the switch key from the config file, the environment, or `--api-key`. With no switch key, prizmal asks for one, as any unauthenticated launch does.

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
OPENAI_API_KEY="$PRIZMAL_SWITCH_KEY" codex --profile prizmal -m your-model
```

`env_key` names the variable Codex reads the key from, so the key itself never enters the file.

To get the `apply_patch` edit tool by hand, set `"apply_patch_tool_type": "freeform"` on your model's entry in the catalog file that `model_catalog_json` names.

`prizmal codex` writes this profile and a model catalog beside it, and passes the provider settings and catalog path as `-c` overrides so your root `config.toml` stays as it was. The catalog makes Codex budget the 1M window the Switch serves instead of a default. It also installs Codex when the `codex` binary is missing.
