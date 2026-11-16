//go:build !windows

package main

import "os"

func usuariosLogados() []string {
	if u := os.Getenv("USER"); u != "" {
		return []string{u}
	}
	return nil
}
