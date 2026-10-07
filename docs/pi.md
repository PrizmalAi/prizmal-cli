# Pi coding agent

`prizmal pi` launches Pi against the Prizmal Switch. Pi has no environment channel for its endpoint and model, so the launch writes a provider entry into Pi's own config: `~/.pi/agent/models.json` carries the endpoint and the model list, `~/.pi/agent/settings.json` names the prizmal provider as the default, and the key goes on the child environment.

## Provider wiring

| What | Where |
|---|---|
| Endpoint, model list, window, output budget | the `prizmal` provider in `~/.pi/agent/models.json`, written with a backup |
| Default provider and model | `~/.pi/agent/settings.json` |
| Credential | `PRIZMAL_SWITCH_KEY`, which the entry's `"apiKey": "$PRIZMAL_SWITCH_KEY"` refers to |

The `"$PRIZMAL_SWITCH_KEY"` value is a reference, not the key, so the file doesn't contain your switch key. Pi 0.77.0 and later read the variable from their own environment, and a launch updates an older Pi first.

Each model entry states a `contextWindow` and a `maxTokens`. Pi budgets the session and compacts from the declared window, and it sends the declared output budget as the request's maximum. The Switch's catalog doesn't report a window, so the launch states one rather than leaving Pi's 128000 default, which would compact a session at a fraction of the 1M window the Switch serves. Set `HARNESS_CONTEXT_LENGTH` to override the window for a model whose real one differs. The output budget is the whole window, the most any route allows, and the Switch caps each request to the limit of the route that serves it.

## Device login

Pi can refresh a device token during a session, so a `prizmal pi` launch runs in device mode on a machine signed in with a device key.

Device mode has no key to export. Pi reads a provider's `apiKey` fresh for every request when the value is a command preceded by `!`. The launch writes a small Pi extension into a temporary directory and hands Pi its path with `--extension`. The directory goes when Pi exits. The extension registers the same provider with `!prizmal auth token` as its `apiKey`, so Pi runs that helper for each credential it needs and gets a fresh device token rather than a launch-time one that expires mid-session. The extension and its config take the credential from that command, so neither file contains a key, and the directory is private to the session.

The command path is the running `prizmal` binary, quoted, so Pi finds it whether or not `prizmal` is on your `PATH`.

Pi's `--extension` adds to the extensions Pi discovers rather than replacing them. Your own extensions, from `~/.pi/agent/extensions`, a project's `.pi/extensions`, or an installed Pi package, load on a device-login launch exactly as they do on any other.

## Restore

```bash
prizmal --restore pi
```

Pi's restore removes the prizmal provider from `~/.pi/agent/models.json`, including any key an older prizmal wrote into it, and puts back the default provider and model you had before the first launch. Providers and settings prizmal doesn't manage are left as they were. On a machine prizmal never configured, `--restore` reports that there is nothing to restore.

## Run Pi without the Prizmal CLI

Pi reads its providers from `~/.pi/agent/models.json`. With a switch key, export it as `PRIZMAL_SWITCH_KEY` and add the prizmal provider to that file:

```json
{
  "providers": {
    "prizmal": {
      "baseUrl": "https://api.prizmal.ai/v1",
      "api": "openai-completions",
      "apiKey": "$PRIZMAL_SWITCH_KEY",
      "models": [
        { "id": "your-model", "contextWindow": 1000000, "maxTokens": 1000000 }
      ]
    }
  }
}
```

Then launch Pi on that provider:

```bash
pi --provider prizmal --model your-model
```

The `"$PRIZMAL_SWITCH_KEY"` value is a reference rather than the key, so this file doesn't contain your switch key.

The `contextWindow` and `maxTokens` on each model matter. Pi budgets the session and compacts from the declared window, and it sends the declared output budget as the request's maximum. The Switch's catalog doesn't report a window, so a launch states one instead of leaving Pi's 128000 default, which would compact a session at a fraction of the 1M window the Switch serves. Set `HARNESS_CONTEXT_LENGTH` to override the window for a model whose real one differs. Setting `maxTokens` to the window requests the most a route allows, and the Switch caps each request to that route's limit.

With a device login instead, there is no key to export. Pi reads a provider's `apiKey` fresh for each request when the value is a command preceded by `!`, so point it at the CLI's device-token helper:

```json
{
  "providers": {
    "prizmal": {
      "baseUrl": "https://api.prizmal.ai/v1",
      "api": "openai-completions",
      "apiKey": "!prizmal auth token",
      "models": [
        { "id": "your-model", "contextWindow": 1000000, "maxTokens": 1000000 }
      ]
    }
  }
}
```

`prizmal pi` does this for the session without touching your config: it writes an extension into a temporary directory and hands Pi its path with `--extension`. The directory goes when Pi exits. The extension registers the same provider with that command as its `apiKey`. The command path is the running `prizmal` binary, quoted, so Pi finds it whether or not `prizmal` is on your `PATH`.

`prizmal pi` adds a few things these files leave out:

- It declares each model's window and output budget, as the examples above do.
- The extension's provider is the only prizmal provider a device launch loads. A switch-key launch uses your `models.json` entry instead.
- It updates an older Pi that reads a provider's key only once, rather than for each request.
- It installs Pi when the `pi` binary is missing.
