# Cline

`prizmal cline` launches Cline against the Prizmal Switch. Cline keeps its provider settings in its data directory, so the launch writes a prizmal provider entry there and puts the key on the child environment. `prizmal cline` starts a per-session loopback relay, so Cline's `/model` picker can list the names your key routes and a device login can use a short-lived credential.

The launch uses the same data directory as Cline: `--data-dir DIR`, then `CLINE_DATA_DIR`, then `--config DIR` as `DIR/data`, then `CLINE_DIR` as `$CLINE_DIR/data`, then `~/.cline/data`. Use `--data-dir` or `CLINE_DATA_DIR` to keep two worktrees from sharing provider settings:

```bash
CLINE_DATA_DIR="$PWD/.cline-data" prizmal --model your-model cline "Reply with OK"
prizmal --model your-model cline --data-dir "$PWD/.cline-data" "Reply with OK"
```

## Provider wiring

| What | Where |
|---|---|
| Provider, endpoint, model | `<data-dir>/settings/providers.json`, written with a backup |
| Provider selection and mode | `<data-dir>/globalState.json` |
| Credential | `OPENAI_API_KEY`, which Cline reads when the provider's `settings` don't set `apiKey` |

The provider entry uses Cline's openai-compatible provider and leaves out `apiKey`, so Cline reads the key from `OPENAI_API_KEY` in its environment. The launch replaces the `settings` map rather than mutating it, which drops any `apiKey` an older prizmal wrote there.

The entry also sets `contextWindow` to 1,000,000, the window the Switch rules for every model. Without it Cline assumes 128,000 tokens for an `openai-compatible` model and compacts at 90 percent of that, long before the route's window is used. A manual setup adds `"contextWindow": 1000000` to `settings` for the same effect.

When you pass `--data-dir` or `--config` to Cline, the launch leaves the flag in place and writes the same two files into the directory Cline reads.

## Search

`prizmal cline` installs a local Cline plugin named `prizmal_search`. It adds one tool, `prizmal_search__search`, which sends the query to the Switch's `/v1/responses` with its `web_search` tool and returns a summary with source URLs. Cline's title and summary requests do not carry session tools, so the model can call search only from the chat it is working on.

The plugin reads `PRIZMAL_CLINE_SEARCH_URL`, `PRIZMAL_CLINE_SEARCH_MODEL`, and `OPENAI_API_KEY` from the environment when the tool runs. The first two name the endpoint and model. The key stays in Cline's process environment and is never written into the plugin or its settings. A launch uses the loopback URL and per-launch placeholder.

The plugin stays installed after Cline exits, as every persisted launch edit does. Running the launch again replaces Prizmal's plugin. It lives under Cline's configuration root (`--config`, `CLINE_DIR`, then `~/.cline`), independently of the data directory, so unrelated global plugins remain available. Launches with different data directories share the one plugin safely because its endpoint, model, and key come from each Cline process's environment; a cross-process lock serializes replacement. A plugin the user already installed, including another plugin named `prizmal_search`, stays untouched. `prizmal --restore cline` removes only Prizmal's copy.

Cline also offers its own `fetch_web_content` tool in the `openai-compatible` sessions configured here. Cline executes it in the client to read a URL; it does not search the web or call the Switch's `/responses` endpoint. Cline's hosted `web_search` tool does not apply to this provider. Both native names remain distinct from `prizmal_search__search`.

## Model

`prizmal cline` takes the model from `--model`, from the default an earlier `--pick` or the picker's `ctrl+s` saved, or from the picker. A launch with a prompt after the integration name runs that task and exits, and a launch on a pipe has no terminal, so neither opens the picker: with no model named they stop with an error that lists the names `prizmal --list` prints. `--tui` asks for the interactive interface, where the picker still opens.

## Loopback relay

`prizmal cline` always starts a relay on an ephemeral `127.0.0.1` port, with a switch key or a device login, and without `--relay`. It points Cline's provider and mode base URLs at `http://127.0.0.1:<port>/v1` and sets `OPENAI_API_KEY` to a random placeholder. The relay accepts only that placeholder. It swaps in the launch's switch key, or a device token fetched for each request, and forwards the request to the Switch.

Cline's `/model` picker sends `GET /v1/models` with no `Authorization` header, because Cline attaches one only from a key stored in its provider settings, and the launch keeps the key off disk. For that one request the relay skips the placeholder check and forwards it with the real credential, so the picker lists the names your key routes. Choosing one switches the live session's model. The next launch still starts on the model `--model` or the picker chose.

If the Switch answers 401, the relay refreshes the device token once and replays the buffered request. This lets a session continue after its first ten-minute token expires. A request on any other path without the placeholder is answered locally with 401 and never reaches the Switch.

The relay is process-local and lasts only while Cline runs. Before Cline starts, the launch changes the endpoint fields in `providers.json` and `globalState.json`; after Cline exits, it writes the direct Switch URL back into those fields. The active provider, model, and act and plan mode selections stay in place. This temporary endpoint change keeps a later bare `cline` from using a dead loopback port. It is separate from `--restore`, which removes Prizmal's persisted provider configuration.

## Restore

```bash
prizmal --restore cline
```

Cline's restore removes the prizmal provider entry and puts back the provider selection and mode pointers you had before the first launch. It relocates only through `CLINE_DATA_DIR` or `CLINE_DIR`. To restore a tree a launch chose with `--data-dir`, set `CLINE_DATA_DIR` to that path before running `prizmal --restore cline`. On a machine prizmal never configured, `--restore` reports that there is nothing to restore.

## Run Cline without the Prizmal CLI

Cline's active provider is `providers[lastUsedProvider]`, and it only accepts its own provider ids, so the prizmal settings go under the `openai-compatible` key. Set that entry in `<data-dir>/settings/providers.json`, point `lastUsedProvider` at it, export your switch key as `PRIZMAL_SWITCH_KEY`, and launch Cline with `OPENAI_API_KEY` set:

```json
{
  "version": 1,
  "lastUsedProvider": "openai-compatible",
  "providers": {
    "openai-compatible": {
      "settings": {
        "provider": "openai-compatible",
        "model": "your-model",
        "baseUrl": "https://api.prizmal.ai/v1",
        "contextWindow": 1000000
      },
      "tokenSource": "manual"
    }
  }
}
```

```bash
export PRIZMAL_SWITCH_KEY=sk-...
OPENAI_API_KEY="$PRIZMAL_SWITCH_KEY" cline
```

The entry leaves out `apiKey`, so Cline reads the key from `OPENAI_API_KEY` and the file doesn't contain your switch key.

`prizmal cline` writes this entry and the selection for you, installs the search plugin, and installs Cline when the `cline` binary is missing. The plugin's generated source and installation are launch-specific; a manual setup can install its own Cline plugin for `prizmal_search__search` and point it at the same `/v1/responses` request.
