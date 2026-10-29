# Kilo Code

`prizmal kilo` launches the Kilo Code CLI against the Prizmal Switch. Kilo takes a whole config as an inline value, so the launch passes one in an environment variable rather than editing a file.

## Provider wiring

| What | Where |
|---|---|
| Provider, endpoint, placeholder key, models, selected model | the inline config in `KILO_CONFIG_CONTENT` |
| Selected model | `--model prizmal/<your-model>` on Kilo's command line |

The inline config declares the `prizmal` provider (`@ai-sdk/openai-compatible`), its endpoint, an `apiKey`, the model list, `"enabled_providers": ["prizmal"]` so Kilo doesn't list gateway models fetched over the network, and `"model": "prizmal/<your-model>"`. It also turns off Kilo's built-in websearch tool, which posts to hosted backends rather than the Switch.

Each model declares text and image input, which Kilo needs before it sends an image, and sets `limit.context` to 1,000,000, the window the Switch rules for every router config, and `limit.output` to 32,000. Without `limit.context`, Kilo doesn't know the window and compacts a session only after the provider refuses it.

Prizmal doesn't write Kilo state. The model picker's Recent list, the prompt history, your sessions and your settings stay in Kilo's own directories.

## Loopback relay

`prizmal kilo` always starts a relay on an ephemeral `127.0.0.1` port, with a switch key or a device login, and without `--relay`. The inline config points at the relay, and its `apiKey` is a random placeholder. The relay accepts only that placeholder and forwards each request to the Switch with the launch's switch key or a fresh device token. The config never contains a key that expires, so a session that runs past one device token keeps working.

The relay is local to the process and stops when Kilo exits.

## Model

`prizmal kilo` takes the model from `--model`, from the default an earlier `--pick` or the picker's `ctrl+s` saved, or from the picker. `prizmal kilo run` runs one message and exits, and a launch on a pipe has no terminal, so neither opens the picker: with no model named they stop with an error that lists the names `prizmal --list` prints.

## Restore

Kilo has nothing to restore: the launch doesn't write provider config or Kilo state.

## Run Kilo without the prizmal CLI

Kilo reads its config from `~/.config/kilo/kilo.json`, or from `KILO_CONFIG_CONTENT` for one launch. Point it at the Switch with a provider block:

```json
{
  "provider": {
    "prizmal": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Prizmal",
      "options": {
        "baseURL": "https://api.prizmal.ai/v1",
        "apiKey": "sk-..."
      },
      "models": {
        "your-model": {
          "name": "your-model",
          "limit": { "context": 1000000, "output": 32000 },
          "modalities": { "input": ["text", "image"], "output": ["text"] }
        }
      }
    }
  },
  "enabled_providers": ["prizmal"],
  "model": "prizmal/your-model"
}
```

A config file holds the key in plain text. To keep it off disk, export the whole config for one launch, and substitute your key with `$PRIZMAL_SWITCH_KEY` in the shell:

```bash
export PRIZMAL_SWITCH_KEY=sk-...
KILO_CONFIG_CONTENT="$(jq -c --arg k "$PRIZMAL_SWITCH_KEY" '.provider.prizmal.options.apiKey = $k' kilo.json)" kilo
```

Kilo ignores `OPENCODE_CONFIG_CONTENT`. `prizmal kilo` also names the launch's model on the command line and runs the relay. When the `kilo` binary is missing, it installs Kilo with npm.
