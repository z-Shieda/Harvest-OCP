package main

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// lerSenhaOculta: desliga o eco do console enquanto a senha é digitada.
func lerSenhaOculta() string {
	h := windows.Handle(os.Stdin.Fd())
	var modo uint32
	if windows.GetConsoleMode(h, &modo) == nil {
		_ = windows.SetConsoleMode(h, modo&^windows.ENABLE_ECHO_INPUT)
		defer windows.SetConsoleMode(h, modo)
	}
	l, _ := entrada.ReadString('\n')
	return strings.TrimRight(l, "\r\n")
}

// o console do Windows abre em CP850: troca para UTF-8 para os acentos e símbolos saírem certos
func init() {
	_ = windows.SetConsoleOutputCP(65001)
	_ = windows.SetConsoleCP(65001)
}
