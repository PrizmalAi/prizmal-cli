# OpenCode

`prizmal opencode` launches OpenCode against the Prizmal Switch. OpenCode takes a whole config as an inline value, so the launch passes one in an environment variable rather than editing a file.

## Provider wiring

| What | Where |
|---|---|
| Provider, endpoint, key, models, selected model | the inline config in `OPENCODE_CONFIG_CONTENT` |
| Model picker state | `~/.local/state/opencode/model.json` |

The inline config declares the `prizmal` provider (`@ai-sdk/openai-compatible`), its endpoint, an `apiKey`, the model list, and `"model": "prizmal/<your-model>"`. It also turns off OpenCode's built-in websearch tool, which posts to OpenCode's own hosted backends rather than the Switch.

OpenCode is the one harness whose key is not on the child environment: the key is a field in the inline config, which lives only in the variable for the life of the process and is never written to a file.

## Device login

OpenCode can't refresh a device token during a session, and a device token expires ten minutes after issue. So a `prizmal opencode` launch on a machine signed in with a device key ignores device login and runs on the switch key from the config file, the environment, or `--api-key`. With no switch key, prizmal asks for one, as any unauthenticated launch does.

## Restore

OpenCode has nothing to restore: the launch doesn't write a provider config, and the model picker state it touches is refreshed on the next launch.

## Run OpenCode without the Prizmal CLI

OpenCode reads its config from a file at `~/.config/opencode/opencode.json`, or from `OPENCODE_CONFIG_CONTENT` for one launch. Point it at the Switch with a provider block:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "prizmal": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Prizmal",
      "options": {
        "baseURL": "https://api.prizmal.ai/v1",
        "apiKey": "sk-..."
      },
      "models": {
        "your-model": { "name": "your-model" }
      }
    }
  },
  "model": "prizmal/your-model"
}
```

Then launch OpenCode:

```bash
opencode
```

A config file holds the key in plain text. To keep it off disk, export the whole config for one launch instead, and substitute your key with `$PRIZMAL_SWITCH_KEY` in the shell:

```bash
export PRIZMAL_SWITCH_KEY=sk-...
OPENCODE_CONFIG_CONTENT="$(jq -c --arg k "$PRIZMAL_SWITCH_KEY" '.provider.prizmal.options.apiKey = $k' opencode.json)" opencode
```

`prizmal opencode` sets this up for the session. It also points the model picker at the launch's model and installs OpenCode when its binary is missing.
