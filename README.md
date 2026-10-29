# Prizmal CLI

One command to launch a supported coding-agent harness — Claude Code, Codex, OpenCode, Pi, Cline, Kilo Code — configured to talk to the **Prizmal Switch**.

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

`go install` writes the binary to `$GOBIN`, or to `$(go env GOPATH)/bin` (`~/go/bin` by default) when `GOBIN` is unset. If your shell then answers `prizmal: command not found`, add that directory to your `PATH`:

```bash
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.bashrc
```

Or build from source:

```bash
git clone https://github.com/PrizmalAi/prizmal-cli
cd prizmal-cli
go build ./cmd/prizmal
```

On first run, `prizmal` offers two ways to sign in: approve this machine in your browser, or paste an API key (see [Credentials](#credentials)).

## Updates

Before a launch, `prizmal` checks once a day for a newer release. In a terminal, a menu offers to upgrade through the tool that installed it, Homebrew or `go install`. From a script, or with `--yes`, `prizmal` prints a warning and continues. A copy built from source or unpacked from an archive prints a warning, then a menu to continue or exit.

To turn the check off, set `"check_updates": false` in `~/.prizmal/config.json`, or export `PRIZMAL_NO_UPDATE_CHECK=1`.

## Harness arguments

To add arguments to every launch of a harness, put them in a `harnesses` section of `~/.prizmal/config.json`, keyed by the name you type after `prizmal`:

```json
{
  "harnesses": {
    "claude": { "args": "--allow-dangerously-skip-permissions" }
  }
}
```

`args` is one string, split like a shell line, so quotes keep a value together. `prizmal` doesn't check the arguments against the harness. A flag you already pass on the command line wins, and `prizmal` doesn't add the configured copy.

## Credentials

A `prizmal` launch can use one of two credentials. You paste a **switch key** in, or this machine enrolls a **device key** once in your browser. The switch key is simplest, since it doesn't expire, while the device key means you don't type a secret on this machine and keeps a long harness session working without a restart.

### Device login

```bash
prizmal login
```

`prizmal login` generates an ed25519 key pair at `~/.prizmal/device.key` (mode `0600`, in the `0700` directory), prints a URL and a fingerprint, opens your browser, and waits. On the consent page you confirm the fingerprint and pick a router config. Then choose Allow. The CLI then polls until your tenant approves the device and stores the first device token in `~/.prizmal/token.json` (mode `0600`). There is no localhost listener, so this works over SSH: open the printed URL on any machine with a browser.

Once your tenant approves a device, a launch uses it automatically. `prizmal claude` runs with an `apiKeyHelper` that refreshes the device token every few minutes, so a session keeps working across a laptop sleep and doesn't need a restart. `prizmal codex` does the same through a command-backed provider `auth`, and runs the helper again after a 401. A sign-in that a tenant's identity provider manages does not expire. Otherwise the device has a deadline, and `prizmal claude` and `prizmal codex` warn you under 24 hours before it, and `prizmal login` again re-approves the same device key.

A device token is a bearer credential that expires 10 minutes after issue, and the Switch binds it to one tenant. The private key never leaves the machine. The next refresh stops once a tenant manager revokes the device or disables the tenant, or sets the member inactive.

Claude Code, Pi, Codex, OpenCode, Cline and Kilo Code can refresh a device token during a session, so all six run in device mode. Each does it through a mechanism its own docs describe, under [Supported integrations](#supported-integrations). Cline and Kilo Code always start the loopback relay, without `--relay`: Cline because its provider configuration takes a fixed key, Kilo Code so its inline config never holds a token that expires.

### Switch key

`prizmal` stores a Prizmal Switch API key in a single config file at `~/.prizmal/config.json`, created on first run and written with `0600` permissions (`0700` on the directory). On first launch it prompts for the key. Type a key and `prizmal` writes it to the file. Leave it empty and the next run prompts again. To run without the prompt, set the key with `--api-key` or `$PRIZMAL_SWITCH_KEY`, or write the file yourself.

The key is stored as **plain text by default**. This matches how the downstream coding harnesses store their own credentials, and a direct switch-key launch uses no local proxy or OS keyring. Passing `--relay`, or starting Cline or Kilo Code, adds a process-local loopback relay for that session. If you prefer not to keep a secret on disk, the `api_key` field is extensible to two other forms. It is one of:

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

An enrolled device key takes precedence over both the config file and `$PRIZMAL_SWITCH_KEY` on the launches device login serves — `claude`, `pi`, `codex`, `opencode`, `cline`, `kilo`, `--list`, and a model pick — since it is the credential your tenant approved for this machine. `--api-key` and `--url` still win for one launch, so you can use a switch key on a device-signed machine.

`prizmal login` prints nothing on stdout. It signs in and exits. `prizmal auth token` is the internal helper command Claude Code, Pi and Codex run for a device token: it prints one token on stdout and nothing else, and you don't run it by hand.

`--list` skips the announcement. Its output is the answer, and the line only gets in the way.

### Model discovery failures

Before a launch, `prizmal` asks the Switch for the tenant's models at `GET /v1/models`. When that request fails, `prizmal` prints one line on stderr with the endpoint, the key source, and the HTTP status:

```
warning: could not read models from https://api.prizmal.ai with api key from $PRIZMAL_SWITCH_KEY: model catalog request returned 401 Unauthorized
```

A launch that already has its model continues after the warning, without the extra picker rows and capabilities the list provides. `--list` and `--pick` stop with the same line as an error, because they can't answer without the list. A `401` means the key doesn't belong to this host: check the announced source first.

## Supported integrations

| Integration | Harness | Key wiring | Docs |
|---|---|---|---|
| `claude` | Claude Code | `ANTHROPIC_AUTH_TOKEN` (`ANTHROPIC_API_KEY` is emptied) | [docs/claude-code.md](docs/claude-code.md) |
| `codex` | OpenAI Codex CLI | `OPENAI_API_KEY` + `model_providers.<profile>` TOML. In device mode, `model_providers.<profile>.auth` runs `prizmal auth token` instead, and no key is set | [docs/codex.md](docs/codex.md) |
| `opencode` | OpenCode | `provider.prizmal.options.apiKey` in the inline config; a device login instead loads a plugin that runs `prizmal auth token` before each request | [docs/opencode.md](docs/opencode.md) |
| `kilo` | Kilo Code | `provider.prizmal.options.apiKey` in the inline config `KILO_CONFIG_CONTENT`, which holds a per-launch placeholder; the launch always starts a loopback relay that holds the credential, so a device login refreshes per request | [docs/kilo.md](docs/kilo.md) |
| `cline` | Cline | `OPENAI_API_KEY`, which Cline reads when `providers.openai-compatible.settings` has no `apiKey`; the launch points its provider at a per-session loopback relay that holds the credential, so the `/model` picker lists your names and a device login refreshes per request. The launch also installs a local `prizmal_search` plugin that adds `prizmal_search__search`. Provider state goes under the data directory that `--data-dir` or `CLINE_DATA_DIR` sets | [docs/cline.md](docs/cline.md) |
| `pi` | Pi coding agent | `PRIZMAL_SWITCH_KEY`, which `"apiKey": "$PRIZMAL_SWITCH_KEY"` in `~/.pi/agent/models.json` refers to (pi 0.77.0 or later); a device login instead loads an extension whose provider runs `prizmal auth token` | [docs/pi.md](docs/pi.md) |

Each doc covers how a launch configures that harness, what its device-login behavior is, how to undo the configuration with `--restore`, and how to run the harness against the Switch without the CLI.

A `codex` launch lists the same rows as the prizmal picker in Codex's `/model`, in the same order and with the Switch's labels and descriptions. Each entry offers the low, medium and high reasoning efforts and declares a 1,000,000-token `context_window`, the window every model the Switch serves has. A model whose `GET /v1/models` id ends in `[1m]` sits under the id without the suffix. `$HARNESS_CONTEXT_LENGTH` overrides the window. For the same list without `prizmal`, write a catalog with one entry per model from `GET /v1/models`, copy the complete example entry in [docs/codex.md](docs/codex.md#catalog-entry), and point `model_catalog_json` at the file. Codex rejects an entry that lacks any of the fields in that example.

A `codex` launch gives Codex its own system prompt. It reads the prompt of the installed Codex's default model with `codex debug models --bundled`, so the prompt always matches the installed version, and it stops with an error when Codex can't print it. To get the same prompt without `prizmal`, point `model_catalog_json` at a catalog whose entry for your model copies `base_instructions` and `model_messages` from that command's output.

## Commands

| Command | What it does |
|---|---|
| `prizmal [flags] INTEGRATION` | Launch an integration (the default command) |
| `prizmal login` | Approve this machine in your browser, or re-approve the device key it already has |
| `prizmal auth token` | Print one device token on stdout (the Claude Code `apiKeyHelper`; internal) |

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
      --relay                  route the launched harness through a per-session loopback relay
      --restore                restore the integration's original configuration and exit
      --subagent-model string  model for subagents, which default to the session model (Claude Code and Codex)
  -u, --url string             provider base URL (or $PRIZMAL_SWITCH_URL)
  -v, --version                print the version
  -y, --yes                    auto-approve confirmation prompts
```

Examples:

```bash
# Launch Claude Code against the Prizmal Switch
prizmal claude

# Choose a model from the menu, and save the choice as the default
# (a pick without --pick runs once; ctrl+s in the menu saves it)
prizmal --pick claude

# prizmal's own flags come before the integration name
prizmal --model gpt-oss:20b claude

# Keep the real credential in a loopback relay while any harness runs
prizmal --relay --model flash codex

# Claude Code, passing a harness flag straight through without a separator
prizmal claude --resume <session-id>

# Codex, with the old `--` separator still accepted
prizmal codex -- --sandbox workspace-write

# Persist configuration only (no launch)
prizmal --persist cline

# Undo config changes made by a previous launch (see the harness's doc)
prizmal --restore codex

# See which models your switch key can route to. The Switch resolves the list
# from the key alone, so it holds your tenant's models and no other tenant's.
# The tier aliases your tenant holds (frontier, core, flash, premier) lead,
# because they route too even though the Switch does not list them; the same
# aliases head the --pick menu.
# `default` is left out: it names the key's bound config, and a launch never
# sends it.
prizmal --list
```

`--relay` starts a loopback HTTP relay for one launch. The harness receives a
random placeholder credential, and the relay replaces it with the launch's real
device token or switch key on every request. It works with Claude Code, Codex,
OpenCode, Pi, Cline and Kilo Code. The relay listens on an ephemeral loopback port, accepts
only that placeholder (Cline's model list request is the one exception), streams responses as they arrive, and refreshes and
replays once after a 401 when the credential source can refresh. It stops when
the harness exits. `--relay` is a launch flag: `--list`, `--restore` and
`--persist` refuse it, and the existing credential gate refuses a remote launch
that has no credential.

The picker shows every name `--list` prints with one exception and one fold. `default` is the placeholder the Switch is retiring, so it is not a row to choose. A model whose `GET /v1/models` entry carries a tier sits under that tier's alias row, and the row names the folded model beside the tier's description. A model that holds several tiers gives a row for each.

Each harness labels the tier rows in its own words, and every row sends the tier's alias. Codex names each tier row after the config that holds it and puts the word in the row's description, as in "Sol-like":

| Alias | Claude Code | Codex |
|---|---|---|
| `frontier` | Opus | Sol |
| `core` | Sonnet | Terra |
| `flash` | Haiku | Luna |
| `premier` | Fable | Astra |

pi, OpenCode and Cline show your router configs by name, with no tier rows. A tenant whose Switch reports no tier gets its router configs by name in every harness.

`prizmal` never writes your key to disk for any of the five harnesses. It sets the key on the launched process's environment, and the config files it writes refer to that variable. Each harness's doc lists the variable. When the harness exits, the key goes with its process, and a plain launch also removes a key that an older `prizmal` wrote into one of those files.

Pass extra arguments for the harness after the integration name. prizmal's
own flags come before the name (see the Usage block above) and every
argument after it is handed to the harness unchanged, so `claude --resume
<session-id>` and any other harness flag need no separator. The `--`
separator is still accepted and consumed, and everything after it passes
through the same way.

A launch always runs a real model. It takes the model from `--model`, from the
default an earlier `--pick` saved, or from the interactive picker when neither
supplies one. A model chosen in the picker during a plain launch runs that
launch only. Press `ctrl+s` in the picker, or pass `--pick`, to save the choice
as the default. The first save creates `~/.prizmal/config.json` if the machine
has none, such as one that signed in with `prizmal login`, and the file holds
the default alone. The device key stays the credential. A saved default skips
the picker on a bare launch. When a launch has no model and no terminal to choose on, it stops,
and the error lists the names `--list` prints. The launch never sends the
Switch's `default` placeholder, which selects the config the key is bound to.

A launch that cannot ask stops. When stdin is not a terminal, or the harness
runs one prompt and exits (`claude -p`, `codex exec`, `opencode run`, `pi
--print`, or a Cline prompt), there is no session for a menu, so the launch
fails with an error that lists the names `--list` prints for your key. Pass one
of them with `--model`, or run `--pick` on a terminal.

`--model` takes a name your switch key routes, or the label `--list` and the
pickers print for it. A label drops the `claude-` prefix from the model's name, so
`tier-opus` and `claude-tier-opus` name the same router config. Claude Code
launches also take its tier words (`sonnet`, `opus`, `haiku`, `fable`, `best`,
`opusplan`, in any case) for a tier your tenant holds, and Codex launches take
Sol, Terra, Luna and Astra, which the launch sends as the tier's alias. The
Switch routes the alias only, so a Codex word typed outside `prizmal` routes
nowhere.

Backups of any modified config files are kept in `~/.prizmal/backup/`.
Each launch, `--persist` and `--restore` deletes any backup there that
has a Switch key in it, and prints the path of each file it deletes. A
backup counts as key-bearing when one object in it has an `apiKey` or
`api_key` next to a `baseUrl` or `base_url` on a `prizmal.ai` host or on
the configured `--url` host. That rule matches a rotated key, and a key
from another tenant, as well as the current one. A key for any other
endpoint is yours, and the backup that has it stays.

## Building

Requires Go 1.25+.

```bash
go build ./cmd/prizmal
go test ./...
```

`prizmal --version` reports where the binary came from: the git tag on a release build (stamped by goreleaser), the pseudo-version of a `go install …@version` build, or the short commit id of a build from a git checkout.

## License

MIT licensed — see [LICENSE](LICENSE). Copyright (c) Ollama; Copyright (c) Prizmal Inc.
