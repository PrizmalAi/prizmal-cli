# Cline

`prizmal cline` launches Cline against the Prizmal Switch. Cline keeps its provider settings in `~/.cline/data/settings/providers.json`, so the launch writes a prizmal provider entry there and puts the key on the child environment.

## Provider wiring

| What | Where |
|---|---|
| Provider, endpoint, model | `~/.cline/data/settings/providers.json`, written with a backup |
| Provider selection and mode | `~/.cline/data/globalState.json` |
| Credential | `OPENAI_API_KEY`, which Cline reads when the provider's `settings` don't set `apiKey` |

The provider entry uses Cline's openai-compatible provider and leaves out `apiKey`, so Cline reads the key from `OPENAI_API_KEY` in its environment. The launch replaces the `settings` map rather than mutating it, which drops any `apiKey` an older prizmal wrote there.

## Device login

Cline can't refresh a device token during a session: it reads `OPENAI_API_KEY` once, and a device token expires ten minutes after issue. So a `prizmal cline` launch on a machine signed in with a device key ignores device login and runs on the switch key from the config file, the environment, or `--api-key`. With no switch key, prizmal asks for one, as any unauthenticated launch does.

## Restore

```bash
prizmal --restore cline
```

Cline's restore removes the prizmal provider entry and puts back the provider selection and mode pointers you had before the first launch. On a machine prizmal never configured, `--restore` reports that there is nothing to restore.

## Run Cline without the Prizmal CLI

Cline's active provider is `providers[lastUsedProvider]`, and it only accepts its own provider ids, so the prizmal settings go under the `openai-compatible` key. Set that entry, point `lastUsedProvider` at it, export your switch key as `PRIZMAL_SWITCH_KEY`, and launch Cline with `OPENAI_API_KEY` set:

```json
{
  "version": 1,
  "lastUsedProvider": "openai-compatible",
  "providers": {
    "openai-compatible": {
      "settings": {
        "provider": "openai-compatible",
        "model": "your-model",
        "baseUrl": "https://api.prizmal.ai/v1"
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

`prizmal cline` writes this entry and the selection for you, and installs Cline when the `cline` binary is missing.
