//go:build !windows

package main

import (
	"errors"
	"fmt"
)

func runMaintenance(root, mode, pkg string) error {
	return errors.New("OmniBridge Setup is Windows-only")
}
func showError(title, text string) { fmt.Printf("%s: %s\n", title, text) }
