//go:build windows

package accounts

import (
	"os/exec"
	"syscall"
)

func applyHiddenProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
