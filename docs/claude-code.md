# Claude Code

`prizmal claude` launches Claude Code against the Prizmal Switch without touching a configuration file. Everything it needs rides the command line and the child environment, and it all goes with the process when Claude Code exits.

## Provider wiring

| What | Value |
|---|---|
| Switch URL | `ANTHROPIC_BASE_URL` |
| Credential | `ANTHROPIC_AUTH_TOKEN`, sent as `Authorization: Bearer` |
| Model, tier remaps, `/model` rows | the inline `--settings` JSON |
| Device login | an `apiKeyHelper` in the same settings JSON |

`ANTHROPIC_API_KEY` is emptied so that an Anthropic key exported in your shell stays out of Switch requests. The launch also sets `ENABLE_TOOL_SEARCH=true`, hides the claude.ai connector warning, and turns off the attribution header and error reporting.

A launch with no model from `--model` or a saved default, and no terminal to ask on, stops with an error that lists the names `--list` prints. `claude -p` is such a run even at a terminal: it prints its answer and exits, so the launch skips the picker and fails rather than starting on a guessed model.

A launch states the model twice, in the inline settings JSON and as the `--model` flag, and the flag takes precedence. Both spell the model the way its own `/model` row does, with `[1m]` when its tier has a 1M window. prizmal consumes a `--model` you type after the integration name, because the model decision belongs to prizmal for Claude Code. Forwarding that value let Claude Code's flag take precedence over the settings prizmal wrote, which is how a launch lost its 1M window and warned that the model was unknown.

Subagents run the session model by default. `--subagent-model` gives them a different one; there is no settings-JSON equivalent, so that variable is the only channel.

The Switch refuses a model name that is not one of your tenant's models or an alias, so every name Claude Code can send has to be one of those. The launch refuses `default` too, because it is not a model name. The launch refuses a `--model`, a saved default or a `--subagent-model` that your tenant does not route, and it lists only the tier aliases your tenant holds. Claude Code also turns a tier word (`/model sonnet`, a built-in agent's model, the small model behind background calls) into its own model id. When your tenant holds the tier, `modelOverrides` maps those ids to the alias, and when it does not, the launch sets `ANTHROPIC_DEFAULT_SONNET_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL` or `ANTHROPIC_DEFAULT_FABLE_MODEL` to the launch model, so the word resolves to a name your tenant routes. `ANTHROPIC_DEFAULT_OPUS_MODEL` is always the launch model, unless the launch model is itself a held tier word, which resolves to that tier's alias.

## Device login

Claude Code can refresh a device token during a session, so a `prizmal claude` launch runs in device mode on a machine signed in with a device key. The launch adds an `apiKeyHelper` to its inline settings: a command that runs `prizmal auth token`, which prints a fresh device token on stdout. Claude Code re-runs it on the interval `CLAUDE_CODE_API_KEY_HELPER_TTL_MS` sets, four minutes, so a background refresh always starts with at least six minutes left on the token in hand. That margin covers a laptop sleep.

In device mode `ANTHROPIC_AUTH_TOKEN` is left unset, and a value exported in your shell is filtered out of the child environment. Claude Code treats that variable as a fixed credential it never refreshes and builds the bearer header from it before consulting the helper, so leaving it set would pin the session to the launch-time token.

## Extra arguments

The `harnesses.claude.args` string in `~/.prizmal/config.json` adds arguments to every `prizmal claude` launch. For example, `"--allow-dangerously-skip-permissions"` puts bypass mode in the Shift+Tab cycle without starting in it. `prizmal` adds a configured flag only when the launch doesn't already carry it, spelled `--flag` or `--flag=value`. Claude Code runs the rest unchanged, including a `-p` you configure, which makes every launch headless. Outside `prizmal`, pass the same arguments to `claude` yourself.

## Restore

The launch doesn't write a config file, so there is nothing for `prizmal --restore claude` to undo.

## Run Claude Code without the Prizmal CLI

You can start Claude Code against the Prizmal Switch with environment variables alone. Export your switch key as `PRIZMAL_SWITCH_KEY`, then run:

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

Pass the model with `--model` and append `[1m]`, so Claude Code budgets the 1M-token window the Switch serves. Without the suffix, Claude Code compacts a session at 200k tokens. For the name, use one of your tenant's models, or a tier alias for the model your tenant assigned to that tier: `frontier`, `core`, `flash` or `premier`. Claude Code's `/model` labels those rows Opus, Sonnet, Haiku and Fable. A tenant that still holds the older `claude-tier-opus`, `claude-tier-sonnet`, `claude-tier-haiku` and `claude-tier-fable` aliases keeps them routing, and its rows keep their old labels.

```bash
claude --model 'flash[1m]'
```

`--model opus` runs the model behind `frontier`. Claude Code turns the word into its own Opus id, and the launch maps that id to `frontier` through `modelOverrides`; without the CLI, the Switch maps the Opus id to `frontier` itself. A name copied off a legacy `/model` row works too, because `tier-opus` is another spelling of `claude-tier-opus`. A tier alias routes only when your tenant assigned a model to that tier. Without that, the Switch refuses the name, and a launch refuses a tier word for a tier your tenant does not hold.

Prefer the alias for the haiku tier over `--model haiku`. A held haiku tier routes `haiku` too. Claude Code reads the word as Claude Haiku 4.5, the Switch maps that id to your `flash` config, and the session starts without auto mode, because Claude Code turns auto mode off for every model released before Claude Opus 4.6.

To get the tier rows in `/model` with the window and model profile `prizmal claude` gives them, save these settings as `prizmal-claude.json`:

```json
{
  "modelPicker": {
    "replaceBuiltInOptions": true,
    "options": [
      {"label": "Opus", "model": "frontier[1m]", "behavesAs": "claude-opus-5", "description": "Opus tier"},
      {"label": "Sonnet", "model": "core[1m]", "behavesAs": "claude-sonnet-5", "description": "Sonnet tier"},
      {"label": "Haiku", "model": "flash[1m]", "behavesAs": "claude-sonnet-5", "description": "Haiku tier"},
      {"label": "Fable", "model": "premier[1m]", "behavesAs": "claude-fable-5-1", "description": "Fable tier"}
    ]
  }
}
```

Include a row only for a tier your tenant holds. Then add `--settings prizmal-claude.json` to the command. A row's `behavesAs` sets the prompt profile and effort defaults Claude Code applies, and its `description` is the row's text. The haiku row behaves as Sonnet 5 so that auto mode stays on. The Switch routes each request by the row's own model name.

### Auto-compaction

A model name `prizmal claude` spells, with its `[1m]` suffix and your tenant's own names, is one Claude Code's model catalogue does not carry. For such a name Claude Code skips auto-compaction on its own: it waits for the endpoint to refuse the request, and a session stalls at the provider's limit. So the launch sets `CLAUDE_CODE_AUTO_COMPACT_WINDOW=1000000`, which tells Claude Code the window and turns its threshold check on. Claude Code then compacts a session at 967000 counted tokens, which is the window less its 20000-token output budget and a 13000-token margin.

To reproduce this without the CLI, add the variable to the command:

```bash
CLAUDE_CODE_AUTO_COMPACT_WINDOW=1000000 claude --model 'claude-tier-haiku[1m]'
```

Set a smaller value to compact sooner. `/autocompact` does not change a window the variable sets.

An HTTP 400 `invalid_request_error` whose message reads `prompt is too long: N tokens > M maximum` also makes Claude Code compact and retry, whatever the window's source. That is the second guard for a conversation that grows past the window between two checks.

`prizmal claude` adds a few things this command leaves out:

- The `/model` menu lists every model your switch key routes to. Without the CLI, it lists the rows in your settings file, or Claude Code's built-in list.
- It sets `ANTHROPIC_DEFAULT_SONNET_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL` and `ANTHROPIC_DEFAULT_FABLE_MODEL` to your model for each tier your tenant does not hold, so a tier word never sends a name the Switch refuses.
- It removes model variables such as `ANTHROPIC_MODEL` that your shell exports, so they can't change the model a launch runs. It keeps a `CLAUDE_CODE_AUTO_COMPACT_WINDOW` you export when the value is a positive number under 1000000, and sets 1000000 otherwise.
- It installs Claude Code when the `claude` binary is missing.
