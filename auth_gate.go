package main

import (
	"fmt"
	"io"

	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
)

// allowUnauthenticated is the explicit escape hatch for launching at a
// remote Switch with no credential. It is set by the --allow-unauthenticated
// flag declared in main.go.

// KeySource describes where the provider API key came from. The names are
// printed verbatim in the launch announcement; they carry no key material.
type KeySource = envconfig.KeySource

const (
	KeySourceFlag   = envconfig.KeySourceFlag
	KeySourceEnv    = envconfig.KeySourceEnv
	KeySourceConfig = envconfig.KeySourceConfig
	KeySourceNone   = envconfig.KeySourceNone
)

// apiKeySource names the source APIKey() resolved from. The key's value is
// never returned, logged, or echoed — only the source's name.
func apiKeySource() KeySource {
	return envconfig.APIKeySource()
}

// announceKeySource writes one line to w naming where the provider API key
// came from. Hard rule: never the value, never a prefix, never a length or a
// hash — the source name only. A key fragment in a terminal is a leaked
// credential.
func announceKeySource(w io.Writer) {
	// Explicit discard: the source line is best-effort stderr output; a
	// write failure must never block a launch. It carries no key material.
	_, _ = fmt.Fprintf(w, "using api key from: %s\n", apiKeySource())
}

// isLoopbackHost reports whether the provider URL points at this machine:
// the loopback addresses and the "localhost"/"*.localhost" names. Launching
// unauthenticated at a loopback endpoint is routine — local development and
// the test stub server both rely on it — so it is never gated.
func isLoopbackHost() bool {
	name := envconfig.Host().Hostname()
	switch name {
	case "localhost", "127.0.0.1", "0.0.0.0", "::1", "[::1]":
		return true
	}
	return len(name) > len(".localhost") && name[len(name)-len(".localhost"):] == ".localhost"
}

// requireCredentialForRemote refuses an unauthenticated request aimed at a
// remote host. It guards a launch and a listing alike, because both ask the
// Switch for something the key selects. A remote switch asked to authenticate
// nothing answers 401, which a harness renders as "Invalid API key" and a
// listing prints as a bad key. Each blames a credential that was never sent.
// The message names all three places a key can come from, and stays neutral
// about which command raised it. Loopback targets are exempt, and
// --allow-unauthenticated overrides the refusal explicitly. It never contains
// a key value.
func requireCredentialForRemote() error {
	if allowUnauthenticated || envconfig.APIKey() != "" || isLoopbackHost() {
		return nil
	}
	return fmt.Errorf(
		"refusing an unauthenticated request to remote host %s: "+
			"give a key with --api-key, in the config file (~/.prizmal/config.json), or via $%s "+
			"(loopback URLs need no key; --allow-unauthenticated overrides this check)",
		envconfig.BaseURL(), envconfig.KeyEnvVar)
}
