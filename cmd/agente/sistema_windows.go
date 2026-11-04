//go:build windows

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// limparInstalacaoAnterior para e remove qualquer agente de versão anterior nesta máquina,
// para a nova substituir sem conflito: encerra e apaga todas as tarefas agendadas conhecidas
// (atual e antigas) e remove sobras de executáveis (.antigo/.novo). O agente é instalado por
// máquina (Program Files + tarefa como SYSTEM), nunca por perfil de usuário.
func limparInstalacaoAnterior() {
	pararServico() // libera o executável para ser substituído
	for _, t := range append(append([]string{}, tarefas...), tarefasAntigas...) {
		_ = schtasks("/End", "/TN", t)
		_ = schtasks("/Delete", "/F", "/TN", t)
	}
	// dá um instante para a tarefa encerrada liberar o executável antigo
	time.Sleep(2 * time.Second)
	for _, nome := range []string{"inventario-agente.exe.antigo", "inventario-agente.exe.novo", "inventario-agente.antigo.exe"} {
		_ = os.Remove(filepath.Join(dirPrograma(), nome))
	}
}

func instalar(cfg Config) error {
	if err := os.MkdirAll(dirPrograma(), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(dirDados(), 0o755); err != nil {
		return err
	}
	// remove a versão anterior antes de colocar a nova (evita conflito de tarefa/arquivo)
	limparInstalacaoAnterior()
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
	if err := schtasks("/Create", "/F", "/TN", tarefas[0], "/XML", xmlPath); err != nil {
		return err
	}
	// serviço em tempo real (SYSTEM): a partir daqui as atualizações não pedem permissão
	if err := instalarServico(destino); err != nil {
		registrar("aviso: não foi possível criar o serviço em tempo real: " + err.Error() + " (a tarefa agendada continua funcionando)")
	} else {
		_ = os.WriteFile(caminhoMarcadorServico(), []byte(versao), 0o644)
	}
	// remove o executável da versão anterior deixado para trás pela troca
	_ = os.Remove(destino + ".antigo")
	gravarMarcadorTarefa(cfg)
	return nil
}

func caminhoMarcadorServico() string { return filepath.Join(dirDados(), "servico.ver") }

// garantirServico: máquinas instaladas antes da 1.12 ganham o serviço sozinhas — a tarefa
// agendada roda como SYSTEM, então pode criá-lo sem pedir permissão. Uma vez por versão.
func garantirServico() {
	if !executandoInstalado() {
		return
	}
	if b, err := os.ReadFile(caminhoMarcadorServico()); err == nil && strings.TrimSpace(string(b)) == versao {
		return
	}
	if err := instalarServico(filepath.Join(dirPrograma(), "inventario-agente.exe")); err != nil {
		registrar("não foi possível criar/atualizar o serviço em tempo real: " + err.Error())
		return
	}
	_ = os.WriteFile(caminhoMarcadorServico(), []byte(versao), 0o644)
	registrar("serviço em tempo real configurado (v" + versao + ")")
}

// caminhoMarcadorTarefa guarda para qual versão/intervalo a tarefa agendada já foi configurada.
func caminhoMarcadorTarefa() string { return filepath.Join(dirDados(), "tarefa.ver") }

func marcadorTarefa(cfg Config) string { return fmt.Sprintf("%s|%dh", versao, cfg.Intervalo) }

func gravarMarcadorTarefa(cfg Config) {
	_ = os.WriteFile(caminhoMarcadorTarefa(), []byte(marcadorTarefa(cfg)), 0o644)
}

// garantirTarefa reaplica a definição da tarefa agendada quando a versão/intervalo mudou
// (ex.: depois de um auto-update, para passar a usar o novo intervalo). Recria a definição
// com schtasks /Create /F, o que não derruba a instância que já está rodando.
func garantirTarefa(cfg Config) {
	if !executandoInstalado() {
		return
	}
	alvo := marcadorTarefa(cfg)
	if atual, err := os.ReadFile(caminhoMarcadorTarefa()); err == nil && strings.TrimSpace(string(atual)) == alvo {
		return
	}
	exe := filepath.Join(dirPrograma(), "inventario-agente.exe")
	xmlPath := filepath.Join(dirDados(), "tarefa.xml")
	if err := os.WriteFile(xmlPath, utf16LE(montarXMLTarefa(exe, cfg.Intervalo)), 0o644); err != nil {
		return
	}
	if err := schtasks("/Create", "/F", "/TN", tarefas[0], "/XML", xmlPath); err != nil {
		registrar("não foi possível reconfigurar a tarefa: " + err.Error())
		return
	}
	gravarMarcadorTarefa(cfg)
	registrar("tarefa agendada reconfigurada (" + alvo + ")")
}

// autoAtualizar baixa do servidor a versão mais nova do agente e troca o próprio executável
// instalado (Program Files), sem reinstalação manual. A troca usa renomeação (um .exe em uso
// pode ser renomeado, não sobrescrito): a nova versão passa a valer na próxima execução da
// tarefa. Só age quando o agente está rodando a partir da pasta de instalação.
func autoAtualizar(cfg Config, a modelo.Atualizacao) bool {
	if !executandoInstalado() {
		exe, _ := os.Executable()
		falhaAutoUpdate("o agente não está rodando de " + dirPrograma() + " (está em " + exe + "); reinstale para o auto-update funcionar")
		return false
	}
	if a.Versao != "" && !versaoMenor(versao, a.Versao) {
		return false // já estamos na versão indicada (ou em uma mais nova)
	}
	// a tarefa agendada e o serviço podem tentar ao mesmo tempo: só um troca o executável
	trava := filepath.Join(dirDados(), "auto-update.trava")
	if fi, err := os.Stat(trava); err == nil && time.Since(fi.ModTime()) > 10*time.Minute {
		_ = os.Remove(trava)
	}
	ft, err := os.OpenFile(trava, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	ft.Close()
	defer os.Remove(trava)
	// baixa sempre do servidor com quem o agente acabou de falar (garantidamente acessível),
	// não da URL informada na resposta — que poderia vir com um host interno/localhost.
	url := strings.TrimRight(cfg.Servidor, "/") + "/api/v1/agente.exe"
	exe := filepath.Join(dirPrograma(), "inventario-agente.exe")
	novo := exe + ".novo"
	if err := baixarComToken(url, cfg.Token, novo); err != nil {
		falhaAutoUpdate("falha ao baixar: " + err.Error())
		return false
	}
	if fi, err := os.Stat(novo); err != nil || fi.Size() < 1_000_000 {
		_ = os.Remove(novo)
		falhaAutoUpdate("download inválido (arquivo incompleto ou removido pelo antivírus)")
		return false
	}
	// 1.21: só instala se estiver assinado pela T.I (e for mesmo uma versão mais nova)
	if err := verificarAtualizacao(cfg, novo, a.Versao); err != nil {
		_ = os.Remove(novo)
		falhaAutoUpdate("atualização RECUSADA (assinatura): " + err.Error())
		return false
	}
	_ = os.Remove(exe + ".antigo")
	if err := os.Rename(exe, exe+".antigo"); err != nil {
		_ = os.Remove(novo)
		falhaAutoUpdate("não foi possível liberar o executável atual: " + err.Error())
		return false
	}
	if err := os.Rename(novo, exe); err != nil {
		_ = os.Rename(exe+".antigo", exe) // desfaz
		falhaAutoUpdate("falha ao aplicar o novo executável: " + err.Error())
		return false
	}
	// força reconfigurar a tarefa na próxima execução (a nova versão pode ter outro intervalo)
	_ = os.Remove(caminhoMarcadorTarefa())
	_ = os.Remove(caminhoErroAutoUpdate())
	registrar("auto-update: versão " + a.Versao + " baixada; passa a valer na próxima execução da tarefa")
	return true
}

// falhaAutoUpdate registra no log e guarda o motivo para o próximo inventário levar ao
// servidor (aparece em Administração → Auto-update dos agentes).
func falhaAutoUpdate(msg string) {
	registrar("auto-update: " + msg)
	_ = os.WriteFile(caminhoErroAutoUpdate(), []byte(time.Now().Format("2006-01-02 15:04")+" "+msg), 0o644)
}

func caminhoErroAutoUpdate() string { return filepath.Join(dirDados(), "auto-update-erro.txt") }

// baixarComToken baixa uma URL autenticando com o token do agente e grava em dest.
func baixarComToken(url, token, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Agente-Token", token)
	cli := &http.Client{Timeout: 5 * time.Minute}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("servidor respondeu %d", resp.StatusCode)
	}
	tmp := dest + ".parcial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 100<<20))
	f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
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
	removerServico()
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
