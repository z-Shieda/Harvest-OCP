//go:build windows

package main

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

const nomeTarefa = `\Inventario\Servidor`

func executar(nome string, args ...string) error {
	cmd := exec.Command(nome, args...)
	var out []byte
	var err error
	if nome == "powershell.exe" {
		out, err = cmd.Output() // stderr em CLIXML é descartado; a mensagem vem do script
	} else {
		out, err = cmd.CombinedOutput()
	}
	if err != nil {
		return fmt.Errorf("%s: %v %s", nome, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func codificar(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		b[i*2], b[i*2+1] = byte(v), byte(v>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// ehAdministrador: "net session" só funciona em um prompt elevado.
func ehAdministrador() bool {
	return exec.Command("net", "session").Run() == nil
}

var errNaoAdmin = fmt.Errorf("este comando precisa de um Prompt de Comando aberto como administrador (botão direito > Executar como administrador)")

func instalar(exe string, porta int, banco, cert, chave string) error {
	if !ehAdministrador() {
		return errNaoAdmin
	}
	args := fmt.Sprintf(`-porta %d -banco "%s"`, porta, banco)
	if cert != "" {
		args += fmt.Sprintf(` -cert "%s" -chave "%s"`, cert, chave)
	}
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	// sem limite de tempo de execução, reinicia sozinho em caso de falha, roda também na bateria
	script := `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$acao = New-ScheduledTaskAction -Execute ` + q(exe) + ` -Argument ` + q(args) + ` -WorkingDirectory ` + q(filepath.Dir(exe)) + `
$gat = New-ScheduledTaskTrigger -AtStartup
$cfg = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -MultipleInstances IgnoreNew
$quem = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
try {
  Register-ScheduledTask -TaskName 'Servidor' -TaskPath '\Inventario\' -Action $acao -Trigger $gat -Settings $cfg -Principal $quem -Description 'Servidor do inventario de maquinas' -Force | Out-Null
} catch { Write-Output $_.Exception.Message; exit 1 }`
	if err := executar("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", codificar(script)); err != nil {
		return err
	}
	_ = executar("netsh", "advfirewall", "firewall", "delete", "rule", "name=InventarioServidor")
	if err := executar("netsh", "advfirewall", "firewall", "add", "rule", "name=InventarioServidor", "dir=in", "action=allow",
		"protocol=TCP", fmt.Sprintf("localport=%d", porta)); err != nil {
		return err
	}
	// inicia agora, sem precisar reiniciar
	return executar("schtasks.exe", "/Run", "/TN", nomeTarefa)
}

func desinstalar(porta int) error {
	if !ehAdministrador() {
		return errNaoAdmin
	}
	_ = executar("schtasks.exe", "/End", "/TN", nomeTarefa)
	_ = executar("netsh", "advfirewall", "firewall", "delete", "rule", "name=InventarioServidor")
	if exec.Command("schtasks.exe", "/Query", "/TN", nomeTarefa).Run() != nil {
		return nil // nada instalado
	}
	return executar("schtasks.exe", "/Delete", "/F", "/TN", nomeTarefa)
}
