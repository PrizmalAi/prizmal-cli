//go:build windows

package main

import "errors"

func execProcess(string, []string, []string) error {
	return errors.New("exec is not available on Windows")
}
