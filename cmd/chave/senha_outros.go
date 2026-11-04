//go:build !windows

package main

import "strings"

func lerSenhaOculta() string {
	l, _ := entrada.ReadString('\n')
	return strings.TrimRight(l, "\r\n")
}
