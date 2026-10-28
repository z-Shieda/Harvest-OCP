//go:build windows

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"inventario/internal/modelo"
)

//go:embed coleta.ps1
var scriptColeta string

const nomePasta = "Inventario"

var tarefas = []string{`\Inventario\Agente`}
var tarefasAntigas = []string{`\Inventario\Agente-Logon`, `\Inventario\Agente-Inicializacao`}

func dirDados() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, nomePasta)
}

func dirPrograma() string {
	base := os.Getenv("ProgramFiles")
	if base == "" {
		base = `C:\Program Files`
	}
	return filepath.Join(base, nomePasta)
}

const suportaInstalacao = true

func executandoInstalado() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.EqualFold(filepath.Dir(filepath.Clean(exe)), filepath.Clean(dirPrograma()))
}

func comandoOculto(ctx context.Context, nome string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, nome, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}

func caminhoPowerShell() string {
	raiz := os.Getenv("SystemRoot")
	if raiz == "" {
		raiz = `C:\Windows`
	}
	p := filepath.Join(raiz, `System32\WindowsPowerShell\v1.0\powershell.exe`)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return "powershell.exe"
}

// caminhoScriptColeta grava o script de coleta em disco (na pasta de dados, ou na pasta
// temporária se aquela não puder ser escrita) e devolve o caminho. Executar um .ps1 em
// arquivo evita o -EncodedCommand, que antivírus de comportamento tratam como suspeito.
func caminhoScriptColeta() (string, error) {
	conteudo := []byte(scriptColeta)
	candidatos := []string{filepath.Join(dirDados(), "coleta.ps1"), filepath.Join(os.TempDir(), "inventario-coleta.ps1")}
	var ultimoErro error
	for _, c := range candidatos {
		if atual, err := os.ReadFile(c); err == nil && bytes.Equal(atual, conteudo) {
			return c, nil // já está em disco e igual
		}
		if err := os.MkdirAll(filepath.Dir(c), 0o755); err != nil {
			ultimoErro = err
			continue
		}
		if err := os.WriteFile(c, conteudo, 0o644); err != nil {
			ultimoErro = err
			continue
		}
		return c, nil
	}
	return "", ultimoErro
}

func coletar() (*modelo.Relatorio, error) {
	script, err := caminhoScriptColeta()
	if err != nil {
		return nil, fmt.Errorf("não foi possível gravar o script de coleta: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := comandoOculto(ctx, caminhoPowerShell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	var saida, erro bytes.Buffer
	cmd.Stdout = &saida
	cmd.Stderr = &erro
	if err := cmd.Run(); err != nil && saida.Len() == 0 {
		return nil, fmt.Errorf("powershell: %v %s", err, strings.TrimSpace(erro.String()))
	}
	dados := bytes.TrimSpace(saida.Bytes())
	dados = bytes.TrimPrefix(dados, []byte{0xEF, 0xBB, 0xBF})
	if i := bytes.IndexByte(dados, '{'); i > 0 {
		dados = dados[i:]
	}
	var rel modelo.Relatorio
	if err := json.Unmarshal(dados, &rel); err != nil {
		return nil, fmt.Errorf("resposta inválida da coleta: %v", err)
	}
	if rel.Hostname == "" {
		rel.Hostname, _ = os.Hostname()
	}
	return &rel, nil
}

func copiarArquivo(origem, destino string) error {
	in, err := os.Open(origem)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := destino + ".novo"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	out.Close()
	_ = os.Remove(destino + ".antigo")
	if _, err := os.Stat(destino); err == nil {
		// um executável em uso não pode ser sobrescrito, mas pode ser renomeado
		if err := os.Rename(destino, destino+".antigo"); err != nil {
			return err
		}
	}
	return os.Rename(tmp, destino)
}

func schtasks(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := comandoOculto(ctx, "schtasks.exe", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

// A tarefa é criada com schtasks.exe a partir de um arquivo XML (um utilitário nativo do
// Windows), em vez de pelo módulo ScheduledTasks do PowerShell. Tarefa única com três
// gatilhos: a cada N horas, no logon de qualquer usuário e na inicialização. Roda como
// SYSTEM (S-1-5-18), também na bateria, e executa assim que possível se perder um horário.
func montarXMLTarefa(exe string, horas int) string {
	inicio := time.Now().Add(2 * time.Minute).Format("2006-01-02T15:04:05")
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Envia o inventario desta maquina ao servidor</Description>
  </RegistrationInfo>
  <Triggers>
    <TimeTrigger>
      <StartBoundary>` + inicio + `</StartBoundary>
      <Enabled>true</Enabled>
      <Repetition>
        <Interval>PT` + fmt.Sprint(horas) + `H</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
    </TimeTrigger>
    <LogonTrigger>
      <Delay>PT1M</Delay>
      <Enabled>true</Enabled>
    </LogonTrigger>
    <BootTrigger>
      <Delay>PT3M</Delay>
      <Enabled>true</Enabled>
    </BootTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>S-1-5-18</UserId>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT15M</ExecutionTimeLimit>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + xmlEscape(exe) + `</Command>
      <Arguments>-silencioso</Arguments>
    </Exec>
  </Actions>
</Task>
`
}

// protegerPasta restringe a pasta a SYSTEM e Administradores (controle total) e deixa os
// usuários comuns apenas com leitura/execução. Impede que um usuário sem privilégio troque o
// coleta.ps1 ou o config.json — que são executados/lidos pela tarefa que roda como SYSTEM.
// Usa os SIDs conhecidos (não os nomes dos grupos, que mudam conforme o idioma do Windows).
func protegerPasta(dir string) error {
	return schtasksSilencioso("icacls.exe", dir, "/inheritance:r",
		"/grant:r", "*S-1-5-18:(OI)(CI)F", // SYSTEM
		"/grant:r", "*S-1-5-32-544:(OI)(CI)F", // Administradores
		"/grant:r", "*S-1-5-32-545:(OI)(CI)RX") // Usuários: ler/executar
}

// schtasksSilencioso roda um utilitário do Windows sem janela e devolve o erro com a saída.
func schtasksSilencioso(nome string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := comandoOculto(ctx, nome, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v %s", nome, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func instalar(cfg Config) error {
	if err := os.MkdirAll(dirPrograma(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(dirDados(), 0o755); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	destino := filepath.Join(dirPrograma(), "inventario-agente.exe")
	if !strings.EqualFold(filepath.Clean(exe), filepath.Clean(destino)) {
		if err := copiarArquivo(exe, destino); err != nil {
			return err
		}
	}
	// remove tarefas de versões anteriores
	for _, t := range tarefasAntigas {
		_ = schtasks("/Delete", "/F", "/TN", t)
	}
	// trava as permissões das pastas (segurança contra escalonamento local): só SYSTEM e
	// administradores escrevem; usuários comuns só leem. Um erro aqui não impede instalar.
	if err := protegerPasta(dirPrograma()); err != nil {
		registrar("aviso: não foi possível proteger " + dirPrograma() + ": " + err.Error())
	}
	if err := protegerPasta(dirDados()); err != nil {
		registrar("aviso: não foi possível proteger " + dirDados() + ": " + err.Error())
	}
	// grava o XML da tarefa (em UTF-16 LE com BOM, como o schtasks espera) e registra-a
	xmlPath := filepath.Join(dirDados(), "tarefa.xml")
	if err := os.WriteFile(xmlPath, utf16LE(montarXMLTarefa(destino, cfg.Intervalo)), 0o644); err != nil {
		return err
	}
	return schtasks("/Create", "/F", "/TN", tarefas[0], "/XML", xmlPath)
}

// utf16LE converte um texto para UTF-16 little-endian com BOM, codificação que o
// schtasks.exe exige no arquivo XML passado em /XML.
func utf16LE(s string) []byte {
	out := []byte{0xFF, 0xFE} // BOM
	for _, r := range s {
		if r > 0xFFFF {
			r = '?'
		}
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

func desinstalar() error {
	for _, t := range append(tarefas, tarefasAntigas...) {
		_ = schtasks("/Delete", "/F", "/TN", t)
	}
	_ = os.RemoveAll(dirDados())
	exe, _ := os.Executable()
	if strings.HasPrefix(strings.ToLower(filepath.Clean(exe)), strings.ToLower(dirPrograma())) {
		// o executável em uso será removido alguns segundos depois de encerrar
		cmd := comandoOculto(context.Background(), "cmd.exe", "/C", "ping 127.0.0.1 -n 4 >nul & rmdir /S /Q \""+dirPrograma()+"\"")
		return cmd.Start()
	}
	return os.RemoveAll(dirPrograma())
}

func mostrarMensagem(titulo, texto string, erro bool) {
	user32 := syscall.NewLazyDLL("user32.dll")
	box := user32.NewProc("MessageBoxW")
	t, _ := syscall.UTF16PtrFromString(titulo)
	m, _ := syscall.UTF16PtrFromString(texto)
	var flags uintptr = 0x40 // MB_ICONINFORMATION
	if erro {
		flags = 0x10 // MB_ICONERROR
	}
	box.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), flags|0x40000) // MB_TOPMOST
}

func abrirArquivo(caminho string) {
	_ = comandoOculto(context.Background(), "rundll32.exe", "url.dll,FileProtocolHandler", caminho).Start()
}
