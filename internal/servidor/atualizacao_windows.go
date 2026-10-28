//go:build windows

package servidor

import (
	"os"
	"os/exec"
	"path/filepath"
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

	conteudo := "@echo off\r\n" +
		"ping -n 4 127.0.0.1 >nul\r\n" +
		"schtasks /End /TN \"" + nomeTarefaServidor + "\" >nul 2>&1\r\n" +
		"ping -n 2 127.0.0.1 >nul\r\n" +
		"del /Q \"" + antigo + "\" >nul 2>&1\r\n" +
		"move /Y \"" + exe + "\" \"" + antigo + "\" >nul\r\n" +
		"move /Y \"" + novo + "\" \"" + exe + "\" >nul\r\n" +
		"if not exist \"" + exe + "\" move /Y \"" + antigo + "\" \"" + exe + "\" >nul\r\n" +
		"schtasks /Run /TN \"" + nomeTarefaServidor + "\" >nul 2>&1\r\n" +
		"ping -n 3 127.0.0.1 >nul\r\n" +
		"schtasks /Query /TN \"" + nomeTarefaServidor + "\" >nul 2>&1 || start \"\" \"" + exe + "\"\r\n"

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
