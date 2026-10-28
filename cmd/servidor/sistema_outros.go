//go:build !windows

package main

import "errors"

func instalar(exe string, porta int, banco, cert, chave string) error {
	return errors.New("no Linux, use um serviço systemd (veja o LEIA-ME)")
}

func desinstalar(porta int) error { return errors.New("não suportado neste sistema") }
