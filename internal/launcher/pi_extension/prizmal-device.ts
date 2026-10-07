/**
 * Prizmal Switch provider for Pi.
 *
 * A machine signed in with a device key holds no switch key: the CLI mints a
 * short-lived device token from ~/.prizmal/device.key and must mint a new one
 * as the old expires. This extension registers the Switch as a provider whose
 * apiKey is a command, the CLI's device-token helper, which Pi runs whenever it
 * needs the credential. The command prints a fresh token, so a session that
 * outlives one token keeps working without a local proxy or any other
 * long-lived helper.
 *
 * The launch writes this file and hands Pi its path with --extension, along
 * with prizmal-device.json next to it, which carries the endpoint and models
 * for this launch.
 */
import { readFileSync } from "node:fs";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

/**
 * piCommandEnvVar names the environment variable holding the credential
 * command. The launch sets it to the quoted path of the running prizmal
 * binary followed by "auth token", so Pi runs the same binary the operator
 * launched and never needs prizmal on PATH.
 */
const piCommandEnvVar = "PRIZMAL_PI_CREDENTIAL_COMMAND";

/** Shape of the sidecar file the launch writes beside this extension. */
type PrizmalLaunchConfig = {
  providerID: string;
  providerName: string;
  baseURL: string;
  models: PrizmalModel[];
};

type PrizmalModel = {
  id: string;
  contextWindow: number;
  maxTokens: number;
  reasoning?: boolean;
  vision?: boolean;
};

export default function (pi: ExtensionAPI) {
  const configPath = process.env.PRIZMAL_PI_CONFIG;
  if (!configPath) {
    return;
  }
  const config = readLaunchConfig(configPath);
  if (!config) {
    return;
  }
  const command = process.env[piCommandEnvVar];
  if (!command) {
    return;
  }

  pi.registerProvider(config.providerID, {
    name: config.providerName,
    baseUrl: config.baseURL,
    api: "openai-completions",
    // The apiKey is a command, not a fixed key. Pi runs it when it needs the
    // credential and reads a fresh device token from stdout, so a device token
    // that expires mid-session is replaced rather than sent again.
    apiKey: `!${command}`,
    models: config.models.map(toModel),
  });
}

function readLaunchConfig(path: string): PrizmalLaunchConfig | undefined {
  try {
    const parsed = JSON.parse(readFileSync(path, "utf-8")) as Partial<PrizmalLaunchConfig>;
    if (!parsed.providerID || !parsed.baseURL || !Array.isArray(parsed.models)) {
      return undefined;
    }
    return {
      providerID: parsed.providerID,
      providerName: parsed.providerName ?? parsed.providerID,
      baseURL: parsed.baseURL,
      models: parsed.models,
    };
  } catch {
    return undefined;
  }
}

function toModel(model: PrizmalModel) {
  return {
    id: model.id,
    name: model.id,
    input: (model.vision ? ["text", "image"] : ["text"]) as ("text" | "image")[],
    reasoning: model.reasoning === true,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    // The window and the output budget come from the launch's config, so a
    // device launch and a switch-key launch size the same model the same way.
    // The launch states them because the Switch's catalog carries no window.
    contextWindow: model.contextWindow,
    maxTokens: model.maxTokens,
  };
}
