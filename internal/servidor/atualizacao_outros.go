//go:build !windows

package servidor

import (
	"os"
	"syscall"
)

// reiniciarComNovo substitui o binário atual e re-executa o processo (Linux/macOS).
func reiniciarComNovo(novo string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	antigo := exe + ".antigo"
	_ = os.Remove(antigo)
	if err := os.Rename(exe, antigo); err != nil {
		return err
	}
	if err := os.Rename(novo, exe); err != nil {
		_ = os.Rename(antigo, exe) // desfaz
		return err
	}
	_ = os.Chmod(exe, 0o755)
	// substitui a imagem do processo pelo novo binário, mantendo os mesmos argumentos.
	return syscall.Exec(exe, os.Args, os.Environ())
}

// reiniciarServidor re-executa o próprio processo com os mesmos argumentos (Linux/macOS).
func reiniciarServidor() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
