package main

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	launcher "github.com/PrizmalAi/prizmal-cli/internal/launcher"
)

// ensureDeviceKey returns the device key a login approves, and whether it made
// it. A machine with no key gets a new one. A machine with a key reuses it,
// unless the Switch says that device was revoked: a revoked device never works
// again, so approving the same key would change nothing.
//
// Only DEVICE_REVOKED retires a key. The Switch answers it to the holder of the
// key alone, and every other refusal (an unapproved key, an expired sign-in, an
// unreachable Switch) leaves the key in place for the normal approval, so one
// failed probe can never cost a good enrollment.
//
// On a terminal the operator chooses between a backup and a plain replacement.
// With no terminal nobody can answer, so the key is backed up and out says
// where it went. Neither the prompt nor the messages print key material.
func ensureDeviceKey(client *device.Client, out io.Writer) (*device.Key, bool, *device.Token, error) {
	key, created, err := device.LoadOrCreateKey()
	if err != nil || created {
		return key, created, nil, err
	}
	token, err := client.Refresh(key, time.Now())
	if err == nil {
		// The probe succeeded, so the device is already approved. The caller
		// gets the token and prints no approval link.
		return key, false, token, nil
	}
	if !errors.Is(err, device.ErrDeviceRevoked) {
		return key, false, nil, nil
	}

	_, _ = fmt.Fprintf(out, "\nThis device was revoked.\n")
	backup := true
	if launcher.StdinIsTerminal() {
		backup, err = launcher.ConfirmPromptWithOptions(
			"Back up the revoked key before making a new one?",
			launcher.ConfirmOptions{Default: launcher.ConfirmDefaultYes})
		if err != nil {
			return nil, false, nil, err
		}
	}
	path, err := device.RetireKey(backup, time.Now())
	if err != nil {
		return nil, false, nil, err
	}
	if path != "" {
		_, _ = fmt.Fprintf(out, "Moved the revoked key to %s\n", path)
	} else {
		_, _ = fmt.Fprintf(out, "Replaced the revoked key.\n")
	}
	key, created, err = device.LoadOrCreateKey()
	return key, created, nil, err
}
