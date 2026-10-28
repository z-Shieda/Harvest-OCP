//go:build !windows

package main

// Implementação para outros sistemas, usada apenas para testes de desenvolvimento.

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"inventario/internal/modelo"
)

func dirDados() string { return "dados-agente" }

const suportaInstalacao = false

func executandoInstalado() bool { return false }

func coletar() (*modelo.Relatorio, error) {
	h, _ := os.Hostname()
	rel := &modelo.Relatorio{
		Hostname:      strings.ToUpper(h),
		SOName:        runtime.GOOS,
		SOArquitetura: runtime.GOARCH,
		CPUThreads:    runtime.NumCPU(),
		UsuarioAtual:  os.Getenv("USER"),
		UltimoLogon:   os.Getenv("USER"),
		HWUUID:        os.Getenv("INV_HWUUID"),
	}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if len(i.HardwareAddr) == 0 {
			continue
		}
		r := modelo.Rede{Nome: i.Name, MAC: strings.ToUpper(i.HardwareAddr.String()), Fisica: true, Conectada: i.Flags&net.FlagUp != 0}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				r.IPv4 = append(r.IPv4, ipn.IP.String())
			}
		}
		rel.Redes = append(rel.Redes, r)
	}
	return rel, nil
}

func instalar(Config) error { return errNaoSuportado }
func desinstalar() error    { return errNaoSuportado }

func mostrarMensagem(titulo, texto string, erro bool) { fmt.Printf("[%s] %s\n", titulo, texto) }

func abrirArquivo(caminho string) { _ = exec.Command("xdg-open", caminho).Start() }
