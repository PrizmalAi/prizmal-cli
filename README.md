# Prizmal CLI

One command to launch a supported coding-agent harness — Claude Code, Codex, OpenCode, Pi, Cline — configured to talk to the **Prizmal Switch**.

```
prizmal claude
prizmal codex
prizmal opencode
prizmal pi
prizmal cline
```

## What it does

`prizmal` launches any coding-agent harness against the Prizmal Switch. Per harness it knows how to:

- **discover** the harness binary (PATH, `~/.local/bin`, npm/uv/bun locations) and **auto-install** it when missing,
- **inject your provider** via the mechanism each harness supports:
  - env vars (`ANTHROPIC_BASE_URL`, `OPENAI_API_KEY`, …),
  - config-file surgery with backup + `--restore` (Codex `config.toml` profiles, Pi `models.json`, Cline, …); these files set the endpoint and model, and the key goes on the environment,
  - inline config JSON (OpenCode),
- **launch** the harness as a child process with your model selected.

## Install

With Homebrew:

```bash
brew install PrizmalAi/tap/prizmal
```

With Go:

```bash
go install github.com/PrizmalAi/prizmal-cli/cmd/prizmal@latest
```

Or build from source:

```bash
git clone https://github.com/PrizmalAi/prizmal-cli
cd prizmal-cli
go build ./cmd/prizmal
```

On first run, `prizmal` prompts for your API key (see [Credentials](#credentials)).

## Credentials

`prizmal` stores its Prizmal Switch API key in a single config file at `~/.prizmal/config.json`, created on first run and written with `0600` permissions (`0700` on the directory). On first launch it prompts for the key. Type a key and `prizmal` writes it to the file. Leave it empty and the next run prompts again. To run without the prompt, set the key with `--api-key` or `$PRIZMAL_SWITCH_KEY`, or write the file yourself.

The key is stored as **plain text by default**. This matches how the downstream coding harnesses store their own credentials, and `prizmal` deliberately does not run a local proxy or an OS keyring, so there is no extra infrastructure. If you prefer not to keep a secret on disk, the `api_key` field is extensible to two other forms. It is one of:

**Plain string** (default)
```json
{"version": 1, "base_url": "https://api.prizmal.ai", "api_key": "sk-plain-text"}
```

**Environment variable**
```json
{"version": 1, "base_url": "https://api.prizmal.ai", "api_key": {"env": "PRIZMAL_API_KEY"}}
```

**Shell out** — run a command and use its trimmed stdout as the key (e.g. 1Password, `age`, `pass`)
```json
{"version": 1, "base_url": "https://api.prizmal.ai", "api_key": {"command": "op read op://Vault/item/field"}}
```

### Precedence

The API key resolves in this order: `--api-key` flag, then `$PRIZMAL_SWITCH_KEY`, then the config file's `api_key` (resolved per its form). The URL follows the same order: `--url`, then `$PRIZMAL_SWITCH_URL`, then the config file's `base_url`. An exported variable is the more specific choice, so it takes precedence over the file. When the flag or the variable supplies the key, `prizmal` doesn't resolve the file's `api_key`, and a `command` form doesn't run. A launch announces the source it used on stderr:

```
using api key from: --api-key
using api key from: $PRIZMAL_SWITCH_KEY
using api key from: config file (~/.prizmal/config.json)
```

The announcement prints the source only, never the key's value, a prefix of it, its length, or a hash.

`--list` skips the announcement. Its output is the answer, and the line only gets in the way.

### Model discovery failures

Before a launch, `prizmal` asks the Switch for the tenant's models at `GET /v1/models`. When that request fails, `prizmal` prints one line on stderr with the endpoint, the key source, and the HTTP status:

```
warning: could not read models from https://api.prizmal.ai with api key from $PRIZMAL_SWITCH_KEY: model catalog request returned 401 Unauthorized
```

A launch that already has its model continues after the warning, without the extra picker rows and capabilities the list provides. `--list` and `--pick` stop with the same line as an error, because they can't answer without the list. A `401` means the key doesn't belong to this host: check the announced source first.

## Supported integrations

| Integration | Harness | Aliases | Key wiring |
|---|---|---|---|
| `claude` | Claude Code | | `ANTHROPIC_AUTH_TOKEN` (`ANTHROPIC_API_KEY` is emptied) |
| `codex` | OpenAI Codex CLI | | `OPENAI_API_KEY` + `model_providers.<profile>` TOML |
| `opencode` | OpenCode | | `provider.prizmal.options.apiKey` |
| `cline` | Cline | | `OPENAI_API_KEY`, which cline reads when `providers.openai-compatible.settings` has no `apiKey` |
| `pi` | Pi coding agent | | `PRIZMAL_SWITCH_KEY`, which `"apiKey": "$PRIZMAL_SWITCH_KEY"` in `~/.pi/agent/models.json` refers to (pi 0.77.0 or later) |

## Usage

```
prizmal [flags] [INTEGRATION] [-- EXTRA_ARGS...]

Flags:
      --allow-unauthenticated  allow an unauthenticated launch against a remote Switch (debugging only)
  -k, --api-key string         provider API key (or $PRIZMAL_SWITCH_KEY)
  -l, --list                   print the models this switch key's tenant serves, and exit
  -m, --model string           model to launch with (defaults to the saved default_model)
      --pick                   choose a model from this switch key's tenant, and save it as the default
      --persist                write the integration configuration without launching (persistent until --restore)
      --restore                restore the integration's original configuration and exit
      --subagent-model string  model for subagents, which default to the session model
  -u, --url string             provider base URL (or $PRIZMAL_SWITCH_URL)
  -v, --version                print the version
  -y, --yes                    auto-approve confirmation prompts
```

Examples:

```bash
# Launch Claude Code against the Prizmal Switch
prizmal claude

# Choose a model from the menu, and save the choice as the default
prizmal --pick claude

# prizmal's own flags come before the integration name
prizmal --model gpt-oss:20b claude

# Claude Code, passing a harness flag straight through without a separator
prizmal claude --resume <session-id>

# Codex, with the old `--` separator still accepted
prizmal codex -- --sandbox workspace-write

# Persist configuration only (no launch)
prizmal --persist cline

# Undo config changes made by a previous launch
prizmal --restore codex

# See which models your switch key can route to. The Switch resolves the list
# from the key alone, so it holds your tenant's models and no other tenant's.
prizmal --list

# Same for Pi: removes the prizmal provider (and any key an older prizmal
# wrote into it) from ~/.pi/agent/models.json and puts back the default provider and model you
# had before the first launch. Cline's --restore does the same for its
# provider selection and mode pointers. On a machine prizmal never
# configured, --restore reports that there is nothing to restore.
prizmal --restore pi
```

`prizmal` never writes your key to disk for any of the five harnesses. It sets
the key on the launched process's environment, and the config files it writes
refer to that variable:

- Codex's generated profile sets `env_key = "OPENAI_API_KEY"`.
- Pi's `models.json` sets `"apiKey": "$PRIZMAL_SWITCH_KEY"`, which pi 0.77.0
  and later read from the environment. A launch updates an older pi first.
- Cline's provider entry leaves out `apiKey`, and cline then reads
  `OPENAI_API_KEY`.

When the harness exits, the key goes with its process. A plain launch also
removes a key that an older `prizmal` wrote into these files. To run a
configured harness yourself, without `prizmal`, export the variable its config
refers to.

Pass extra arguments for the harness after the integration name. prizmal's
own flags come before the name (see the Usage block above) and every
argument after it is handed to the harness unchanged, so `claude --resume
<session-id>` and any other harness flag need no separator. The `--`
separator is still accepted and consumed, and everything after it passes
through the same way.

A launch always runs a real model. It takes the model from `--model`, from the
default an earlier `--pick` saved, or from the interactive picker when neither
supplies one. When a launch has no model and no terminal to choose on, it stops
and asks you to pass `--model`.

Backups of any modified config files are kept in `~/.prizmal/backup/`.
Each launch, `--persist` and `--restore` deletes any backup there that
has a Switch key in it, and prints the path of each file it deletes. A
backup counts as key-bearing when one object in it has an `apiKey` or
`api_key` next to a `baseUrl` or `base_url` on a `prizmal.ai` host or on
the configured `--url` host. That rule matches a rotated key, and a key
from another tenant, as well as the current one. A key for any other
endpoint is yours, and the backup that has it stays.

## Run Claude Code without the Prizmal CLI

You can start Claude Code against the Prizmal Switch with environment variables alone, without installing the Prizmal CLI. Export your switch key as `PRIZMAL_SWITCH_KEY`, then run:

```bash
ANTHROPIC_BASE_URL=https://api.prizmal.ai \
ANTHROPIC_AUTH_TOKEN="$PRIZMAL_SWITCH_KEY" \
ANTHROPIC_API_KEY= \
ENABLE_TOOL_SEARCH=true \
ENABLE_CLAUDEAI_MCP_SERVERS=false \
CLAUDE_CODE_ATTRIBUTION_HEADER=0 \
DISABLE_ERROR_REPORTING=1 \
DISABLE_FEEDBACK_COMMAND=1 \
CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1 \
claude
```

The first two variables connect Claude Code to the Switch. The others match what `prizmal claude` sets:

| Variable | Purpose |
|---|---|
| `ANTHROPIC_BASE_URL=` | The Switch URL. Leave off `/v1`, because Claude Code adds it. |
| `ANTHROPIC_AUTH_TOKEN=` | Your switch key, sent as `Authorization: Bearer`. |
| `ANTHROPIC_API_KEY=` | Empty, so an Anthropic key exported in your shell stays out of Switch requests. Keep your switch key out of this variable too, because Claude Code asks for approval before the first turn when it finds a key there. |
| `ENABLE_TOOL_SEARCH=true` | Claude Code turns tool search off for any host other than Anthropic's. Without it, each request sends the full tool list. |
| `ENABLE_CLAUDEAI_MCP_SERVERS=false` | Hides the startup warning about claude.ai connectors, which need a claude.ai login that this launch doesn't use. |
| `CLAUDE_CODE_ATTRIBUTION_HEADER=0`, `DISABLE_ERROR_REPORTING=1`, `DISABLE_FEEDBACK_COMMAND=1`, `CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1` | Turn off the attribution header, error reports to Anthropic, `/feedback`, and the feedback survey. |

Claude Code flags work as usual, so `claude --resume <session-id>` goes after `claude` on the last line.

### Choose a model

Pass the model with `--model` and append `[1m]`, so Claude Code budgets the 1M-token window the Switch serves. Without the suffix, Claude Code compacts a session at 200k tokens. For the name, use one of your tenant's router configs, or a tier alias that routes to the config your tenant assigned to that tier: `claude-tier-opus`, `claude-tier-sonnet`, `claude-tier-haiku` or `claude-tier-fable`.

```bash
claude --model 'claude-tier-haiku[1m]'
```

Use the alias for the haiku tier, and avoid `--model haiku`. Claude Code reads `haiku` as Claude Haiku 4.5, and it turns auto mode off for every model released before Claude Opus 4.6, Haiku 4.5 among them.

To get the tier rows in `/model` with the window and model profile `prizmal claude` gives them, save these settings as `prizmal-claude.json`:

```json
{
  "modelPicker": {
    "replaceBuiltInOptions": true,
    "options": [
      {"model": "claude-tier-opus[1m]", "behavesAs": "claude-opus-5", "description": "Opus tier"},
      {"model": "claude-tier-sonnet[1m]", "behavesAs": "claude-sonnet-5", "description": "Sonnet tier"},
      {"model": "claude-tier-haiku[1m]", "behavesAs": "claude-sonnet-5", "description": "Haiku tier"},
      {"model": "claude-tier-fable[1m]", "behavesAs": "claude-fable-5-1", "description": "Fable tier"}
    ]
  }
}
```

Then add `--settings prizmal-claude.json` to the command. A row's `behavesAs` sets the prompt profile and effort defaults Claude Code applies, and its `description` is the row's text. The haiku row behaves as Sonnet 5 so that auto mode stays on. The Switch routes each request by the row's own model name.

`prizmal claude` adds a few things this command leaves out:

- The `/model` menu lists every model your switch key routes to. Without the CLI, it lists the rows in your settings file, or Claude Code's built-in list.
- It removes model variables such as `ANTHROPIC_MODEL` that your shell exports, so they can't change the model a launch runs.
- It installs Claude Code when the `claude` binary is missing.

## Building

Requires Go 1.25+.

```bash
go build ./cmd/prizmal
go test ./...
```

`prizmal --version` reports where the binary came from: the git tag on a release build (stamped by goreleaser), the pseudo-version of a `go install …@version` build, or the short commit id of a build from a git checkout.

## License

MIT licensed — see [LICENSE](LICENSE). Copyright (c) Ollama; Copyright (c) Prizmal Inc.
