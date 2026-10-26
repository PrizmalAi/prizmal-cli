package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/PrizmalAi/prizmal-cli/internal/device"
	"github.com/PrizmalAi/prizmal-cli/internal/envconfig"
	"github.com/PrizmalAi/prizmal-cli/internal/relay"
)

func newLaunchRelay(deviceMode bool, switchKey string, opts ...relay.Option) (*relay.Relay, error) {
	upstream := envconfig.UnversionedBaseURL()
	token, err := launchTokenSource(deviceMode, switchKey)
	if err != nil {
		return nil, err
	}
	r, err := relay.New(upstream, token, opts...)
	if err != nil {
		return nil, fmt.Errorf("start loopback relay: %w", err)
	}
	return r, nil
}

func launchTokenSource(deviceMode bool, switchKey string) (relay.TokenSource, error) {
	if !deviceMode {
		return func(context.Context) (string, error) {
			return switchKey, nil
		}, nil
	}

	key, err := device.LoadKey()
	if err != nil {
		return nil, err
	}
	client := device.NewClient(envconfig.BaseURL())
	var tokenMu sync.Mutex
	return func(ctx context.Context) (string, error) {
		// Parallel harness requests share one cache file. Serialize cache
		// reads, refreshes, and writes inside this launch.
		tokenMu.Lock()
		defer tokenMu.Unlock()

		refresh := relay.IsRefresh(ctx)
		loads := 0
		load := func() *device.CachedToken {
			loads++
			// On a 401 the cached bearer was refused, so the first load skips
			// it. HelperToken can still call load again after a 429 and reuse a
			// token a parallel helper saved.
			if refresh && loads == 1 {
				return nil
			}
			cached, err := device.LoadCachedToken()
			if err != nil {
				return nil
			}
			return cached
		}
		value, cached, err := device.HelperToken(client, key, time.Now(), load, time.Sleep)
		if err != nil {
			return "", err
		}
		if cached != nil {
			if err := cached.Save(); err != nil {
				return "", err
			}
		}
		return value, nil
	}, nil
}
