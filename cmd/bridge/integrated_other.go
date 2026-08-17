//go:build !windows

package main

func integratedDesktopSupported() bool { return false }

func launchIntegratedDesktop(root, page string) (int, error) { return 0, nil }

func showStartupError(title, text string) {}
