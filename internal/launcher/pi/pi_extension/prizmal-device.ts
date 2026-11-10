/**
 * Prizmal Switch extension for Pi: the device-token provider and the turn
 * charge.
 *
 * Every launch loads this file. A device launch also registers the provider
 * below; a switch-key launch keeps the provider from models.json and gets only
 * the turn charge.
 *
 * The turn charge: Pi prices a turn from per-token rates in models.json, and
 * the Switch's router picks the serving model per turn, so no rate is right.
 * The Switch sends the turn's real charge on the last chunk of the turn. This
 * extension reads it there and sets the message's cost to it, which is the
 * figure Pi's footer sums.
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

/**
 * switchProviderID is the provider id the launch selects. It mirrors
 * piProviderID in the CLI; a test keeps the two equal. The charge is applied to
 * this provider's messages only, so a model the user picks from another
 * provider keeps Pi's own pricing.
 */
const switchProviderID = "prizmal";

/** Dollars in one pico-dollar: the Switch's charged_total_pico is dollars times 10^12. */
const picoPerDollar = 1e12;

/**
 * chargeFromChunk returns the turn charge in US dollars carried by one raw
 * stream chunk, or undefined when the chunk carries none.
 *
 * charged_total_pico wins when present because it is an integer and exact;
 * usage.cost is the same figure as a float. A chunk with neither, as from a
 * tenant the Switch does not bill, leaves Pi's own cost in place.
 */
export function chargeFromChunk(chunk: unknown): number | undefined {
  const usage = (chunk as { usage?: Record<string, unknown> } | null | undefined)?.usage;
  if (!usage || typeof usage !== "object") {
    return undefined;
  }
  const pico = usage.charged_total_pico;
  if (typeof pico === "number" && Number.isFinite(pico) && pico >= 0) {
    return pico / picoPerDollar;
  }
  const cost = usage.cost;
  if (typeof cost === "number" && Number.isFinite(cost) && cost >= 0) {
    return cost;
  }
  return undefined;
}

/**
 * registerTurnCharge sets each Switch message's cost to the charge the Switch
 * sent for it.
 *
 * The Switch sends the charge on the chunk that ends a turn and never on a
 * tool step, so the last chunk of a message is the only one that can carry it.
 * The charge is held from that chunk until the message ends, then dropped, so
 * a later message with no charge never inherits it.
 *
 * One value serves the whole process: Pi's stream event carries no message id,
 * so two assistant streams running at once in one Pi process would share it.
 * Pi runs one assistant stream at a time today.
 */
function registerTurnCharge(pi: ExtensionAPI) {
  let charge: number | undefined;

  pi.on("message_start", (event) => {
    if (event.message.role === "assistant") {
      charge = undefined;
    }
  });

  pi.on("provider_stream_event", (event) => {
    if (event.provider !== switchProviderID) {
      return;
    }
    const seen = chargeFromChunk(event.data);
    if (seen !== undefined) {
      charge = seen;
    }
  });

  pi.on("message_end", (event) => {
    const held = charge;
    charge = undefined;
    const message = event.message;
    if (held === undefined || message.role !== "assistant" || message.provider !== switchProviderID) {
      return;
    }
    return {
      message: {
        ...message,
        usage: { ...message.usage, cost: { ...message.usage.cost, total: held } },
      },
    };
  });
}

export default function (pi: ExtensionAPI) {
  // provider_stream_event arrived in Pi 0.99.0. The launcher accepts a Pi back
  // to 0.77.0, and those releases ignore the unknown event, so the cost stays
  // at Pi's own figure. The guard keeps a release that rejects an unknown event
  // from taking the session down: the charge is cosmetic, the session is not.
  try {
    registerTurnCharge(pi);
  } catch {
    // Leave the cost at Pi's own figure.
  }

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
