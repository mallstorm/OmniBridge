package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		showError("OmniBridge Setup", err.Error())
		os.Exit(1)
	}
	root := filepath.Dir(exe)
	mode := "install"
	pkg := ""
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--install", "install":
			mode = "install"
		case "--repair", "repair":
			mode = "repair"
		case "--backup", "backup":
			mode = "backup"
		case "--restore", "restore":
			mode = "restore"
		case "--update", "update":
			mode = "update"
		case "--uninstall", "uninstall":
			mode = "uninstall"
		case "--verify", "verify":
			mode = "verify"
		default:
			if pkg == "" {
				pkg = os.Args[i]
			}
		}
	}
	if err := runMaintenance(root, mode, pkg); err != nil {
		showError("OmniBridge Setup", fmt.Sprintf("%s failed: %v", mode, err))
		os.Exit(1)
	}
}
