//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// anyDeskInstalado: existe um serviço AnyDesk (inclusive de cliente personalizado, "AnyDesk-xxxx")
// ou o executável na pasta de programas.
func anyDeskInstalado() bool {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, registry.ENUMERATE_SUB_KEYS); err == nil {
		nomes, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, n := range nomes {
			if strings.HasPrefix(strings.ToLower(n), "anydesk") {
				return true
			}
		}
	}
	for _, d := range []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles")} {
		if d != "" && existe(filepath.Join(d, "AnyDesk", "AnyDesk.exe")) {
			return true
		}
	}
	return false
}
