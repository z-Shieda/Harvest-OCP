//go:build !windows

package main

import (
	"context"
	"os/user"
)

// Fora do Windows (testes de desenvolvimento): "-servico" roda a escuta em primeiro plano.
func rodarServico() {
	escutarEventos(context.Background())
}

func instalarServico(exe string) error { return nil }
func removerServico()                  {}
func pararServico()                    {}

func contaAtual() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}

func podeSeAtualizar() (bool, string) {
	return false, "teste fora do Windows (desenvolvimento)"
}
