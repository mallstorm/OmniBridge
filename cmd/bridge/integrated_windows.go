//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

func integratedDesktopSupported() bool { return true }

func launchIntegratedDesktop(root, page string) (int, error) {
	script := filepath.Join(root, "scripts", "desktop.ps1")
	args := []string{"-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", script, "-Root", root, "-Page", page}
	logPath := filepath.Join(root, "logs", "ui.log")
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	cmd := exec.Command("powershell.exe", args...)
	cmd.Dir = root
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	err := cmd.Run()
	if logFile != nil {
		_ = logFile.Close()
	}
	if err != nil {
		if details := desktopErrorDetails(logPath); details != "" {
			err = fmt.Errorf("%w\n\n%s", err, details)
		}
	}
	if cmd.ProcessState == nil {
		return -1, err
	}
	return cmd.ProcessState.ExitCode(), err
}

func desktopErrorDetails(path string) string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return ""
	}
	const limit = 6000
	if len(b) > limit {
		b = b[len(b)-limit:]
	}
	return strings.TrimSpace(string(b))
}

func showStartupError(title, text string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	const mbOK = 0x00000000
	const mbIconError = 0x00000010
	_, _, _ = messageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), uintptr(mbOK|mbIconError))
}
