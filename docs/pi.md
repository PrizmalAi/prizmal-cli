# Pi coding agent

`prizmal pi` launches Pi against the Prizmal Switch. A launch writes nothing in your Pi configuration: it hands Pi an extension that registers the provider for the session, and points Pi at an agent directory of its own, so `~/.pi/agent/models.json` and `~/.pi/agent/settings.json` are byte for byte what they were before. The key goes on the child environment.

## Provider wiring

| What | Where |
|---|---|
| Endpoint, model list, window, output budget | the `prizmal` provider the launch's extension registers, from a config file in the launch's temporary directory |
| Default provider and model | the launch's own copy of `settings.json`, which goes when Pi exits |
| Credential | `PRIZMAL_SWITCH_KEY`, which the sidecar's `"apiKey": "$PRIZMAL_SWITCH_KEY"` refers to |

The sidecar sends `x-prizmal-client-version` with the CLI's version, so the Switch can tell which CLI build a request came from. The `"$PRIZMAL_SWITCH_KEY"` value is a reference, not the key: the sidecar names the variable and the key rides the child environment, so no file holds it. Pi 0.77.0 and later read a `$NAME` reference from their own environment, and a launch updates an older Pi first.

A launch registers every model the Switch serves, not only the one it runs, because Pi's `/model` lists what its provider holds. Entries of yours under the `prizmal` provider keep their fields and their place in that list, and every other provider is read from your `models.json` unchanged.

## What the launch keeps out of your config

Pi writes to its agent directory on its own: a `/model` switch is persisted as the default, a `/settings` change is saved, and a `/trust` decision is recorded. A launch therefore sets `PI_CODING_AGENT_DIR` to a directory of its own. Every entry of `~/.pi/agent` is linked into it — your `extensions/`, `skills/`, `prompts/`, `themes/`, `auth.json`, `models-store.json`, `trust.json`, `sessions/` — so they load and record exactly as they do from a bare `pi`. Only `settings.json` and `models.json` are copies, and they are the launch's: a `/model` switch inside a launch changes the session, not your files.

Two consequences follow. A setting or a scope you change inside a launch goes with it, the same deal the launch already makes with its extension. And a `pi install` inside a launch is not kept, because packages live in `settings.json`. Run `pi install` in a bare `pi` when you mean to keep it.

## `--persist`

`prizmal --persist pi` is the explicit, permanent form: it writes the provider into `~/.pi/agent/models.json` with a backup, and sets `defaultProvider` and `defaultModel` in `~/.pi/agent/settings.json`. If you run pi with `PI_CODING_AGENT_DIR` set, `--persist` and `--restore` use that directory, the one your bare `pi` reads. The entry names the key as `"apiKey": "$PRIZMAL_SWITCH_KEY"`, so it holds a reference and never the key, and a bare `pi` then runs on it once `PRIZMAL_SWITCH_KEY` is exported. The entry names no other variable: an older `prizmal` wrote `x-prizmal-client-version: "$PRIZMAL_CLIENT_VERSION"` there, and Pi 1.6.0 refuses to send a request whose referenced variable is unset, so a persisted entry no longer carries that header and the launch sends the version itself. `prizmal --restore pi` removes what `--persist` wrote.

A `models.json` an older `prizmal` launch wrote still holds that entry, and a bare `pi` fails on it with `Failed to resolve provider "prizmal" header "x-prizmal-client-version"`. A launch says so and names the command to run: `prizmal --restore pi`.

Each model entry states a `contextWindow` and a `maxTokens`. Pi budgets the session and compacts from the declared window, and it sends the declared output budget as the request's maximum. The Switch's catalog doesn't report a window, so the launch states one rather than leaving Pi's 128000 default, which would compact a session at a fraction of the 1M window the Switch serves. Set `HARNESS_CONTEXT_LENGTH` to override the window for a model whose real one differs. The output budget is the whole window, the most any route allows, and the Switch caps each request to the limit of the route that serves it.

## Model

`prizmal pi` takes the model from `--model`, from the default an earlier `--pick` or the picker's `ctrl+s` saved, or from the picker. `prizmal pi --print` runs one prompt and exits, and a launch on a pipe has no terminal, so neither opens the picker: with no model named they stop with an error that lists the names `prizmal --list` prints.

## Device login

Pi can refresh a device token during a session, so a `prizmal pi` launch runs in device mode on a machine signed in with a device key.

Device mode has no key to export. Pi reads a provider's `apiKey` fresh for every request when the value is a command preceded by `!`. The launch points the extension's `apiKey` at `!prizmal auth token`, so Pi runs that helper for each credential it needs and gets a fresh device token rather than a launch-time one that expires mid-session. The extension, its sidecar and the launch's agent directory hold no key and no token, and the directory is private to the session.

The command path is the running `prizmal` binary, quoted, so Pi finds it whether or not `prizmal` is on your `PATH`.

Pi's `--extension` adds to the extensions Pi discovers rather than replacing them. Your own extensions, from `~/.pi/agent/extensions`, a project's `.pi/extensions`, or an installed Pi package, load on a launch exactly as they do on a bare `pi`.

## Turn cost

Pi's footer shows a turn's cost from the per-token `cost` rates in its provider entry. The Switch picks the serving model for each turn, so no fixed rate is right, and Pi leaves the cost at 0.

Every `prizmal pi` launch loads the extension above, and the extension reads the charge the Switch sends on the last chunk of a turn: `usage.charged_total_pico` (dollars times 10^12), or `usage.cost` in dollars when the first is absent. It sets that message's cost to the charge, so the footer shows what the Switch billed. A turn with no charge, as on a free tenant, keeps Pi's own figure.

This needs Pi 0.99.0 or later, which added the `provider_stream_event` hook the extension reads. On an older Pi (tested on 0.77.0 and 0.87.1) the launch still works and the footer cost stays at 0. To get the same footer without `prizmal`, load an extension that does the same from a `--extension` flag or `~/.pi/agent/extensions`.

## Restore

```bash
prizmal --restore pi
```

Pi's restore removes the prizmal provider from `~/.pi/agent/models.json`, including any key an older prizmal wrote into it, and puts back the default provider and model you had before the first launch. It also drops every `prizmal/*` row from `enabledModels`, the list that decides what a session starts with when it is non-empty: Pi adds a row there when you switch models, and a row left behind keeps Pi pointed at a provider the restore removed. Providers and settings prizmal doesn't manage are left as they were. On a machine prizmal never configured, `--restore` reports that there is nothing to restore.

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

`prizmal pi` does this for the session without touching your config: it writes an extension into a temporary directory and hands Pi its path with `--extension`. The directory goes when Pi exits. The extension registers that provider, with the helper command as its `apiKey` on a device login and the `$PRIZMAL_SWITCH_KEY` reference on a switch key. The command path is the running `prizmal` binary, quoted, so Pi finds it whether or not `prizmal` is on your `PATH`.

`prizmal pi` adds a few things these files leave out:

- It declares each model's window and output budget, as the examples above do.
- It sets `"reasoning": true` on a model the Switch's `GET /v1/models` reports with `reasoning: true`, so `--thinking` takes effect. Add the same field to a manual entry for such a model.
- The extension's provider is the only prizmal provider a launch loads, whatever the credential; your own models under the `prizmal` provider appear in it, and a `/model` switch inside a launch does not outlive the session.
- It loads the extension that shows the Switch's turn charge in Pi's footer, as described under Turn cost.
- It updates an older Pi that reads a provider's key only once, rather than for each request.
- It installs Pi when the `pi` binary is missing.
