//go:build !windows

package accounts

import "os/exec"

func applyHiddenProcess(cmd *exec.Cmd) {}
