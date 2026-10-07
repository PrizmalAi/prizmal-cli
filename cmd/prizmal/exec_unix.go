//go:build !windows

package main

import "syscall"

func execProcess(path string, args, env []string) error {
	return syscall.Exec(path, append([]string{path}, args...), env)
}
