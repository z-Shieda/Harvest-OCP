//go:build windows

package servidor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const nomeTarefaServidor = `\Inventario\Servidor`

// reiniciarComNovo troca o executável em uso pelo novo e reinicia o serviço. Um .exe em
// execução não pode ser sobrescrito, mas pode ser renomeado; por isso a troca é feita por
// um .bat separado, que roda depois que este processo encerra. Tenta reiniciar pela tarefa
// agendada e, se ela não existir, inicia o executável diretamente.
func reiniciarComNovo(novo string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	antigo := filepath.Join(filepath.Dir(exe), "inventario-servidor.antigo.exe")
	bat := filepath.Join(filepath.Dir(exe), "atualizar-servidor.bat")

	logf := filepath.Join(filepath.Dir(exe), "atualizar-servidor.log")
	pid := strconv.Itoa(os.Getpid())
	// 1.13.1: espera o processo antigo sair de verdade (até ~60 s) antes de trocar o arquivo e
	// registra cada passo em atualizar-servidor.log — antes, se o .exe ainda estivesse preso
	// (antivírus/Windows), a troca falhava calada e o servidor voltava na versão anterior.
	conteudo := "@echo off\r\n" +
		"echo %date% %time% inicio da atualizacao >> \"" + logf + "\"\r\n" +
		"ping -n 4 127.0.0.1 >nul\r\n" +
		"schtasks /End /TN \"" + nomeTarefaServidor + "\" >nul 2>&1\r\n" +
		"taskkill /F /PID " + pid + " >nul 2>&1\r\n" +
		"del /Q \"" + antigo + "\" >nul 2>&1\r\n" +
		"set /a n=0\r\n" +
		":tenta\r\n" +
		"move /Y \"" + exe + "\" \"" + antigo + "\" >nul 2>&1 && goto movido\r\n" +
		"set /a n+=1\r\n" +
		"if %n% geq 30 goto falhou\r\n" +
		"ping -n 3 127.0.0.1 >nul\r\n" +
		"goto tenta\r\n" +
		":falhou\r\n" +
		"echo %date% %time% ERRO: o executavel continuou em uso (antivirus?); servidor mantido na versao anterior >> \"" + logf + "\"\r\n" +
		"goto iniciar\r\n" +
		":movido\r\n" +
		"move /Y \"" + novo + "\" \"" + exe + "\" >nul 2>&1 && (echo %date% %time% executavel trocado >> \"" + logf + "\") || (move /Y \"" + antigo + "\" \"" + exe + "\" >nul & echo %date% %time% ERRO ao colocar o novo; anterior restaurado >> \"" + logf + "\")\r\n" +
		":iniciar\r\n" +
		"schtasks /Run /TN \"" + nomeTarefaServidor + "\" >nul 2>&1\r\n" +
		"ping -n 3 127.0.0.1 >nul\r\n" +
		"schtasks /Query /TN \"" + nomeTarefaServidor + "\" >nul 2>&1 || start \"\" \"" + exe + "\"\r\n" +
		"echo %date% %time% servidor reiniciado >> \"" + logf + "\"\r\n"

	if err := os.WriteFile(bat, []byte(conteudo), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("cmd.exe", "/C", bat)
	// DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP: segue vivo depois que o servidor encerra.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0) // libera o arquivo para o .bat poder trocá-lo
	return nil
}

// reiniciarServidor encerra este processo e o inicia de novo pela tarefa agendada (usado depois de
// importar um banco). Se não houver tarefa, inicia o executável direto.
func reiniciarServidor() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	bat := filepath.Join(filepath.Dir(exe), "reiniciar-servidor.bat")
	conteudo := "@echo off\r\n" +
		"ping -n 4 127.0.0.1 >nul\r\n" +
		"schtasks /End /TN \"" + nomeTarefaServidor + "\" >nul 2>&1\r\n" +
		"taskkill /F /PID " + strconv.Itoa(os.Getpid()) + " >nul 2>&1\r\n" +
		"ping -n 3 127.0.0.1 >nul\r\n" +
		"schtasks /Run /TN \"" + nomeTarefaServidor + "\" >nul 2>&1 || start \"\" \"" + exe + "\" " + argumentosAtuais() + "\r\n"
	if err := os.WriteFile(bat, []byte(conteudo), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("cmd.exe", "/C", bat)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
	if err := cmd.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}

func argumentosAtuais() string {
	var p []string
	for _, a := range os.Args[1:] {
		p = append(p, `"`+a+`"`)
	}
	return strings.Join(p, " ")
}
