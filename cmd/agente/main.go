// Agente de inventário: coleta a configuração da máquina e envia ao servidor.
//
// Uso (Windows):
//
//	inventario-agente.exe                       duplo clique: instala (se compilado com servidor/token) ou envia agora
//	inventario-agente.exe -instalar             instala e agenda execuções automáticas (admin)
//	inventario-agente.exe -desinstalar          remove agendamentos e arquivos (admin)
//	inventario-agente.exe -relatorio            gera e abre um relatório HTML local
//	inventario-agente.exe -json > saida.json    imprime o inventário em JSON
//	opções: -servidor http://srv:8080 -token XXXX -intervalo 4 -silencioso
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"inventario/internal/modelo"
)

// versaoMenor diz se a versão a é anterior a b ("1.5.0" < "1.6.0"). Compara maior.menor.revisão.
func versaoMenor(a, b string) bool {
	pa, pb := partesVersao(a), partesVersao(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func partesVersao(v string) [3]int {
	var r [3]int
	for i, p := range strings.SplitN(strings.TrimSpace(v), ".", 3) {
		if i > 2 {
			break
		}
		n, _ := strconv.Atoi(strings.TrimFunc(p, func(c rune) bool { return c < '0' || c > '9' }))
		r[i] = n
	}
	return r
}

// Valores padrão que podem ser gravados na compilação com:
//
//	-ldflags "-X main.servidorPadrao=http://10.0.0.5:8080 -X main.tokenPadrao=SEU_TOKEN"
var (
	servidorPadrao = ""
	tokenPadrao    = ""
	versao         = "1.17.0"
)

type Config struct {
	Servidor  string `json:"servidor"`
	Token     string `json:"token"`
	Intervalo int    `json:"intervalo_horas"`
}

type arquivoID struct {
	AgentID string `json:"agent_id"`
	HWUUID  string `json:"hw_uuid"`
}

var silencioso bool

func main() {
	fServidor := flag.String("servidor", "", "URL do servidor (ex.: http://10.0.0.5:8080)")
	fToken := flag.String("token", "", "token dos agentes (exibido no painel, menu Administração)")
	fIntervalo := flag.Int("intervalo", 0, "intervalo entre envios automáticos, em horas (padrão 1)")
	fInstalar := flag.Bool("instalar", false, "instala o agente e cria as tarefas agendadas")
	fDesinstalar := flag.Bool("desinstalar", false, "remove o agente e as tarefas agendadas")
	fRelatorio := flag.Bool("relatorio", false, "gera um relatório HTML local e abre no navegador")
	fJSON := flag.Bool("json", false, "imprime o inventário em JSON na saída padrão")
	flag.BoolVar(&silencioso, "silencioso", false, "não exibe mensagens (usado pelas tarefas agendadas)")
	fServico := flag.Bool("servico", false, "roda a conexão em tempo real com o servidor (usado pelo serviço InventarioAgente)")
	flag.Parse()

	if *fServico {
		silencioso = true
		_ = os.MkdirAll(dirDados(), 0o755)
		rodarServico()
		return
	}

	_ = os.MkdirAll(dirDados(), 0o755)
	cfg := carregarConfig()
	if *fServidor != "" {
		cfg.Servidor = strings.TrimRight(strings.TrimSpace(*fServidor), "/")
	}
	if *fToken != "" {
		cfg.Token = strings.TrimSpace(*fToken)
	}
	if *fIntervalo > 0 {
		cfg.Intervalo = *fIntervalo
	} else if cfg.Intervalo <= 0 || cfg.Intervalo == 4 {
		// Novo padrão: 1 h (antes 4 h). Migra instalações antigas que ficaram com 4 h,
		// a não ser que um intervalo tenha sido informado explicitamente com -intervalo.
		cfg.Intervalo = 1
	}

	// Em instalações já existentes, garante que a tarefa agendada esteja com a configuração
	// (intervalo) desta versão — reaplica uma vez por versão, sem derrubar a execução atual.
	if executandoInstalado() {
		garantirTarefa(cfg)
		garantirServico()
	}

	switch {
	case *fDesinstalar:
		if err := desinstalar(); err != nil {
			falhar("Erro ao desinstalar: " + err.Error())
		}
		avisar("Agente de inventário removido desta máquina.")
		return
	case *fInstalar || instalacaoAutomatica(cfg):
		if cfg.Servidor == "" || cfg.Token == "" {
			falhar("Informe o servidor e o token: -servidor http://IP:8080 -token SEU_TOKEN")
		}
		errInst := salvarConfig(cfg)
		if errInst == nil {
			errInst = instalar(cfg)
		}
		if errInst != nil {
			if !*fInstalar {
				// duplo clique sem permissão de administrador: apenas envia uma vez
				registrar("instalação automática falhou: " + errInst.Error())
				rel, err := coletarCompleto()
				if err == nil {
					_, err = enviar(cfg, rel)
				}
				if err != nil {
					falhar("Falha ao enviar para " + cfg.Servidor + ":\n" + err.Error())
				}
				avisar("Inventário enviado para " + cfg.Servidor + ".\n\nPara instalar o envio automático, execute novamente com o botão direito > Executar como administrador.")
				return
			}
			falhar("Erro ao instalar (execute como administrador): " + errInst.Error())
		}
		rel, err := coletarCompleto()
		if err == nil {
			_, err = enviar(cfg, rel)
		}
		msg := fmt.Sprintf("Agente instalado. Envio automático a cada %d h e a cada logon.", cfg.Intervalo)
		if err != nil {
			msg += "\n\nAtenção: o primeiro envio falhou: " + err.Error()
		} else {
			msg += "\n\nPrimeiro inventário enviado com sucesso para " + cfg.Servidor
		}
		avisar(msg)
		return
	}

	rel, err := coletarCompleto()
	if err != nil {
		falhar("Erro na coleta: " + err.Error())
	}

	if *fJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rel)
		return
	}
	if *fRelatorio {
		caminho, err := gerarRelatorioHTML(rel)
		if err != nil {
			falhar("Erro ao gerar relatório: " + err.Error())
		}
		abrirArquivo(caminho)
		return
	}

	if cfg.Servidor == "" || cfg.Token == "" {
		falhar("Servidor/token não configurados. Use -servidor e -token ou execute com -instalar.")
	}
	resp, err := enviar(cfg, rel)
	if err != nil {
		registrar("falha no envio: " + err.Error())
		falhar("Falha ao enviar para " + cfg.Servidor + ":\n" + err.Error() +
			"\n\nO inventário foi salvo localmente em " + filepath.Join(dirDados(), "ultimo_inventario.json"))
	}
	registrar("inventário enviado para " + cfg.Servidor + " (agente v" + versao + ")")
	// O servidor diz qual programa monitorar; se mudou, confere e reenvia já (sem esperar 1 h).
	if resp != nil && resp.Monitorar != nil && salvarAlvoMonitor(resp.Monitorar) && executandoInstalado() {
		registrar("programa monitorado atualizado pelo servidor: " + resp.Monitorar.Nome)
		if rel2, err := coletarCompleto(); err == nil {
			_, _ = enviar(cfg, rel2)
		}
	}
	// O servidor trocou de token e ainda aceita o antigo: passa a usar o atual.
	if resp != nil && resp.NovoToken != "" && resp.NovoToken != cfg.Token && executandoInstalado() {
		novo := cfg
		novo.Token = resp.NovoToken
		if err := salvarConfig(novo); err == nil {
			registrar("token dos agentes atualizado pelo servidor")
			cfg = novo
		}
	}
	// O servidor pode pedir a migração para um novo endereço (ex.: mudança para a nuvem).
	if resp != nil && resp.Migrar != nil {
		migrarServidor(cfg, *resp.Migrar, rel)
	}
	// O servidor pode avisar que há uma versão mais nova do agente: baixa e troca sozinho.
	if resp != nil && resp.Atualizar != nil {
		autoAtualizar(cfg, *resp.Atualizar)
	}
	avisar("Inventário de " + rel.Hostname + " enviado com sucesso para " + cfg.Servidor)
}

// migrarServidor troca o endereço/token do agente SOMENTE depois de confirmar que o novo
// servidor aceita o relatório. Se o novo endereço não responder, mantém o atual — assim um
// endereço errado no painel nunca deixa a máquina sem contato.
func migrarServidor(atual Config, m modelo.Migracao, rel *modelo.Relatorio) {
	novo := Config{
		Servidor:  strings.TrimRight(strings.TrimSpace(m.Servidor), "/"),
		Token:     strings.TrimSpace(m.Token),
		Intervalo: atual.Intervalo,
	}
	if novo.Servidor == "" || strings.EqualFold(novo.Servidor, atual.Servidor) {
		return
	}
	if novo.Token == "" {
		novo.Token = atual.Token
	}
	if _, err := enviar(novo, rel); err != nil {
		registrar("migração para " + novo.Servidor + " adiada (novo servidor não respondeu): " + err.Error())
		return
	}
	if err := salvarConfig(novo); err != nil {
		registrar("migração: não foi possível salvar a nova configuração: " + err.Error())
		return
	}
	registrar("migrado para o novo servidor: " + novo.Servidor)
}

// instalacaoAutomatica: executado sem parâmetros (duplo clique), com servidor e token conhecidos
// e fora da pasta de instalação → instala.
func instalacaoAutomatica(cfg Config) bool {
	return flag.NFlag() == 0 && suportaInstalacao && !executandoInstalado() && cfg.Servidor != "" && cfg.Token != ""
}

func coletarCompleto() (*modelo.Relatorio, error) {
	rel, err := coletar()
	if err != nil {
		return nil, err
	}
	coletarMicroSIP(rel) // ramais do softphone (sem senha)
	coletarAnyDesk(rel)  // ID do AnyDesk (só o ID e o apelido)
	coletarMonitor(rel)  // programa monitorado (ex.: Work Monitor), se o servidor pediu
	if b, err := os.ReadFile(filepath.Join(dirDados(), "auto-update-erro.txt")); err == nil {
		rel.AutoUpdateErro = strings.TrimSpace(string(b))
	}
	rel.AgentVersion = versao
	rel.ColetadoEm = time.Now().UTC().Format(time.RFC3339)
	rel.AgentID = obterAgentID(rel.HWUUID)
	if b, err := json.MarshalIndent(rel, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(dirDados(), "ultimo_inventario.json"), b, 0o644)
	}
	return rel, nil
}

// obterAgentID devolve o identificador desta instalação. Se o UUID de hardware mudou
// (ex.: imagem de disco clonada para outra máquina), um novo ID é gerado.
func obterAgentID(hwUUID string) string {
	caminho := filepath.Join(dirDados(), "agent_id.json")
	var a arquivoID
	if b, err := os.ReadFile(caminho); err == nil {
		_ = json.Unmarshal(b, &a)
	}
	if a.AgentID != "" && (a.HWUUID == "" || hwUUID == "" || strings.EqualFold(a.HWUUID, hwUUID)) {
		if a.HWUUID == "" && hwUUID != "" {
			a.HWUUID = hwUUID
			if b, err := json.Marshal(a); err == nil {
				_ = os.WriteFile(caminho, b, 0o644)
			}
		}
		return a.AgentID
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	a = arquivoID{AgentID: hex.EncodeToString(buf), HWUUID: hwUUID}
	if b, err := json.Marshal(a); err == nil {
		_ = os.WriteFile(caminho, b, 0o644)
	}
	return a.AgentID
}

func enviar(cfg Config, rel *modelo.Relatorio) (*modelo.Resposta, error) {
	rel.ServidorAtual = cfg.Servidor
	corpo, err := json.Marshal(rel)
	if err != nil {
		return nil, err
	}
	var ultimoErro error
	for tentativa := 1; tentativa <= 3; tentativa++ {
		req, err := http.NewRequest(http.MethodPost, cfg.Servidor+"/api/v1/relatorio", bytes.NewReader(corpo))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Agente-Token", cfg.Token)
		cli := &http.Client{Timeout: 30 * time.Second}
		resp, err := cli.Do(req)
		if err == nil {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				var r modelo.Resposta
				_ = json.Unmarshal(msg, &r)
				return &r, nil
			}
			ultimoErro = fmt.Errorf("servidor respondeu %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest {
				return nil, ultimoErro
			}
		} else {
			ultimoErro = err
		}
		time.Sleep(time.Duration(tentativa*5) * time.Second)
	}
	return nil, ultimoErro
}

// carregarConfig define servidor e token, nesta ordem de prioridade:
//   - executável já instalado (Program Files): config gravada na instalação (ProgramData);
//   - executável novo: config.json ao lado do .exe > valores embutidos na compilação > ProgramData.
//
// Assim, um agente recompilado com token novo substitui a configuração antiga ao ser executado.
func carregarConfig() Config {
	ler := func(caminho string) (Config, bool) {
		var c Config
		b, err := os.ReadFile(caminho)
		if err != nil || json.Unmarshal(b, &c) != nil || c.Servidor == "" || c.Token == "" {
			return c, false
		}
		return c, true
	}
	daInstalacao := filepath.Join(dirDados(), "config.json")
	cfg := Config{Servidor: servidorPadrao, Token: tokenPadrao}
	if !executandoInstalado() {
		if exe, err := os.Executable(); err == nil {
			if c, ok := ler(filepath.Join(filepath.Dir(exe), "config.json")); ok {
				cfg = c
			}
		}
		if cfg.Servidor == "" || cfg.Token == "" {
			if c, ok := ler(daInstalacao); ok {
				cfg = c
			}
		}
	} else if c, ok := ler(daInstalacao); ok {
		cfg = c
	} else if exe, err := os.Executable(); err == nil {
		// instalado pelo MSI: a config pode estar ao lado do executável
		if c, ok := ler(filepath.Join(filepath.Dir(exe), "config.json")); ok {
			cfg = c
		}
	}
	cfg.Servidor = strings.TrimRight(strings.TrimSpace(cfg.Servidor), "/")
	cfg.Token = strings.TrimSpace(cfg.Token)
	return cfg
}

func salvarConfig(cfg Config) error {
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(filepath.Join(dirDados(), "config.json"), b, 0o600)
}

func registrar(msg string) {
	caminho := filepath.Join(dirDados(), "agente.log")
	if st, err := os.Stat(caminho); err == nil && st.Size() > 1<<20 {
		_ = os.Rename(caminho, caminho+".old")
	}
	f, err := os.OpenFile(caminho, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
}

func avisar(msg string) {
	if !silencioso {
		mostrarMensagem("Inventário", msg, false)
	}
}

func falhar(msg string) {
	registrar(msg)
	if !silencioso {
		mostrarMensagem("Inventário - erro", msg, true)
	}
	os.Exit(1)
}

var errNaoSuportado = errors.New("operação disponível apenas no Windows")
