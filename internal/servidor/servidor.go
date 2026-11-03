package servidor

import (
	"bytes"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"inventario/internal/modelo"
)

//go:embed web
var arquivosWeb embed.FS

type Servidor struct {
	b            *Banco
	paginas      map[string]*template.Template
	lim          *limitador
	Versao       string // versão do servidor
	VersaoAgente string // versão do agente embutido (pode ficar para trás do servidor)
	dirBackup    string // pasta do backup automático (para mostrar o último no Início)
	eventos      *hubEventos
	CaminhoBanco string // arquivo do banco (para importar um banco de outro servidor)
}

// VersaoAgente é a versão do agente embutido em payload/. Os scripts de compilação gravam o
// valor lido de cmd/agente/main.go (-X inventario/internal/servidor.VersaoAgente=...).
// Fica separada da versão do servidor para que uma versão só de servidor (ex.: 1.8, chamados)
// não dispare o auto-update em todas as máquinas nem marque os agentes como desatualizados.
var VersaoAgente = "1.17.0"

// Programa monitorado (Work Monitor): configurado em Configurações (monitor.go, 1.15).

// serieVersao devolve a série "maior.menor" de uma versão ("1.5.1" -> "1.5").
// Usada para comparar versões de agente/servidor: uma correção de revisão (1.5.0 -> 1.5.1)
// não marca os agentes como desatualizados.
func serieVersao(v string) string {
	v = strings.TrimSpace(v)
	p := strings.Split(v, ".")
	if len(p) >= 2 {
		return p[0] + "." + p[1]
	}
	return v
}

func Novo(b *Banco, versao string) (*Servidor, error) {
	s := &Servidor{b: b, paginas: map[string]*template.Template{}, lim: &limitador{falha: map[string][]time.Time{}}, Versao: versao, VersaoAgente: VersaoAgente, eventos: novoHub()}
	funcs := template.FuncMap{
		"tam":            formatarTamanho,
		"gb":             func(v int64) string { return formatarTamanho(uint64(v)) },
		"data":           formatarData,
		"rel":            tempoRelativo,
		"status":         classeStatus,
		"join":           strings.Join,
		"upper":          strings.ToUpper,
		"anydesk":        formatarAnyDesk,
		"rotuloAnyDesk":  rotuloAnyDesk,
		"anydeskAusente": anyDeskAusente,
		"setoresFixos":   func() []string { return s.b.SetoresFixos() },
		"contem": func(l []string, v string) bool {
			for _, x := range l {
				if strings.EqualFold(x, v) {
					return true
				}
			}
			return false
		},
		"pct": func(livre, total uint64) int {
			if total == 0 {
				return 0
			}
			return int(100 - livre*100/total)
		},
		"add":   func(a, b int) int { return a + b },
		"serie": serieVersao,
		// monitoradoAtivo: recurso "programa monitorado" (Work Monitor) — desligado na 1.10,
		// pendente para uma versão futura (ver CONTEXTO.md). O código continua no lugar.
		"monitoradoAtivo": func() bool { return s.b.ConfigMonitor().Ativo },
		"rotuloMonitor":   rotuloMonitor,
		"monitorAusente":  monitorAusente,
		// inicial: letra do avatar (nome, senão usuário)
		"inicial": func(a Admin) string {
			n := strings.TrimSpace(a.Nome)
			if n == "" {
				n = a.Usuario
			}
			for _, r := range n {
				return strings.ToUpper(string(r))
			}
			return "?"
		},
		// dict monta um mapa para passar vários valores a um template interno ({{template "x" (dict "A" 1 "B" 2)}})
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				if k, ok := kv[i].(string); ok {
					m[k] = kv[i+1]
				}
			}
			return m
		},
	}
	nomes, err := fs.Glob(arquivosWeb, "web/templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, n := range nomes {
		base := strings.TrimSuffix(strings.TrimPrefix(n, "web/templates/"), ".html")
		if base == "layout" {
			continue
		}
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(arquivosWeb, "web/templates/layout.html", n)
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", n, err)
		}
		s.paginas[base] = t
	}
	return s, nil
}

func (s *Servidor) Rotas() http.Handler {
	mux := http.NewServeMux()
	estaticos, _ := fs.Sub(arquivosWeb, "web/static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(estaticos))))

	mux.HandleFunc("POST /api/v1/relatorio", s.apiRelatorio)
	mux.HandleFunc("GET /api/v1/agente.exe", s.apiAgenteExe)
	mux.HandleFunc("GET /api/v1/eventos", s.apiEventos)
	mux.HandleFunc("POST /api/v1/eventos/resposta", s.apiRespostaTeste)
	mux.HandleFunc("GET /saude", s.saude)
	mux.HandleFunc("GET /login", s.paginaLogin)
	mux.HandleFunc("POST /login", s.acaoLogin)

	// --- Chamados sem login (rede interna; liga/desliga em Administração) ---
	mux.HandleFunc("GET /abrir-chamado", s.paraNovoServidor(s.paginaAbrirChamado))
	mux.HandleFunc("POST /abrir-chamado", s.paraNovoServidor(limitarUpload(s.acaoAbrirChamado)))
	mux.HandleFunc("GET /acompanhar/{token}", s.paraNovoServidor(s.paginaAcompanhar))
	mux.HandleFunc("POST /acompanhar/{token}/comentar", s.paraNovoServidor(limitarUpload(s.acaoComentarPublico)))
	mux.HandleFunc("GET /acompanhar/{token}/anexos/{aid}", s.paraNovoServidor(s.baixarAnexoPublico))

	// p   = qualquer usuário logado (chamados)
	// pInv = exige permissão de inventário (admin ou técnico)
	// pAdm = exige administrador
	p := func(padrao string, h func(http.ResponseWriter, *http.Request, *Sessao)) {
		mux.HandleFunc(padrao, s.protegido(h))
	}
	pInv := func(padrao string, h func(http.ResponseWriter, *http.Request, *Sessao)) {
		mux.HandleFunc(padrao, s.protegido(s.exige(h, func(a Admin) bool { return a.PodeInventario() })))
	}
	pAdm := func(padrao string, h func(http.ResponseWriter, *http.Request, *Sessao)) {
		mux.HandleFunc(padrao, s.protegido(s.exige(h, func(a Admin) bool { return a.EhAdmin() })))
	}
	// pGer = gerencia chamados (admin ou técnico)
	pGer := func(padrao string, h func(http.ResponseWriter, *http.Request, *Sessao)) {
		mux.HandleFunc(padrao, s.protegido(s.exige(h, func(a Admin) bool { return a.PodeGerenciarChamados() })))
	}
	// pUp = qualquer logado, aceitando anexos (limite de tamanho antes de ler o formulário)
	pUp := func(padrao string, h func(http.ResponseWriter, *http.Request, *Sessao)) {
		mux.HandleFunc(padrao, limitarUpload(s.protegido(h)))
	}
	p("POST /sair", s.acaoSair)
	p("GET /{$}", s.paginaInicial)

	// --- Chamados (qualquer usuário logado) ---
	p("GET /chamados", s.paginaChamados)
	p("GET /chamados/novo", s.paginaNovoChamado)
	pUp("POST /chamados/novo", s.acaoCriarChamado)
	p("GET /chamados/{id}", s.paginaChamado)
	pUp("POST /chamados/{id}/comentar", s.acaoComentarChamado)
	p("POST /chamados/{id}/atualizar", s.acaoAtualizarChamado)
	p("POST /chamados/{id}/assumir", s.acaoAssumirChamado)
	p("GET /api/chamados/novidades", s.apiNovidadesChamados)
	p("GET /chamados/{id}/anexos/{aid}", s.baixarAnexo)
	pGer("GET /chamados/relatorios", s.paginaRelatorioChamados)
	pGer("GET /chamados/exportar.csv", s.exportarChamadosCSV)
	pAdm("POST /admin/chamados", s.acaoConfigChamados)
	pAdm("POST /admin/chamados/testar", s.acaoTestarWebhookChamados)
	pAdm("POST /admin/email", s.acaoConfigEmail)
	pAdm("POST /admin/email/testar", s.acaoTestarEmail)
	pAdm("POST /admin/{id}/dados", s.acaoDadosAdmin)
	p("POST /conta/dados", s.acaoMeusDados)

	// --- Inventário (admin ou técnico) ---
	pInv("GET /inventario", s.paginaPainel)
	pInv("GET /maquinas", s.paginaMaquinas)
	pInv("GET /maquinas/{id}", s.paginaMaquina)
	pInv("POST /maquinas/{id}/editar", s.acaoEditarMaquina)
	pInv("POST /maquinas/{id}/excluir", s.acaoExcluirMaquina)
	pInv("GET /lixeira", s.paginaLixeira)
	pInv("POST /lixeira/maquinas/{id}/restaurar", s.acaoRestaurarMaquina)
	pAdm("POST /lixeira/maquinas/{id}/apagar", s.acaoApagarMaquina)
	pInv("POST /lixeira/ramais/{id}/restaurar", s.acaoRestaurarRamal)
	pAdm("POST /lixeira/ramais/{id}/apagar", s.acaoApagarRamal)
	pInv("POST /ramais/linha/{id}/excluir", s.acaoExcluirRamalLinha)
	pInv("POST /ramais/{ramal}/excluir", s.acaoExcluirRamal)
	pInv("POST /maquinas/{id}/mesclar", s.acaoMesclarMaquina)
	pInv("GET /usuarios", s.paginaUsuarios)
	pInv("POST /usuarios/locais", s.acaoOcultarLocais)
	pInv("GET /usuarios/{id}", s.paginaUsuario)
	pInv("POST /usuarios/{id}/editar", s.acaoEditarUsuario)
	pInv("POST /usuarios/{id}/excluir", s.acaoExcluirUsuario)
	pInv("POST /usuarios/{id}/mesclar", s.acaoMesclarUsuario)
	pInv("POST /usuarios/{id}/apelido/remover", s.acaoRemoverApelido)
	pInv("POST /usuarios/{id}/ignorar", s.acaoIgnorarUsuario)
	pInv("POST /filtros/criar", s.acaoCriarFiltro)
	pInv("POST /filtros/{id}/excluir", s.acaoExcluirFiltro)
	pInv("GET /ramais", s.paginaRamais)
	pInv("GET /ramais/exportar.csv", s.exportarRamaisCSV)
	pInv("GET /ramais/{ramal}", s.paginaRamal)
	pInv("POST /ramais/{ramal}/ajustar", s.acaoAjustarRamal)
	pInv("GET /notificacoes", s.paginaNotificacoes)
	pInv("POST /notificacoes", s.acaoNotificacoes)
	pInv("GET /duplicados", s.paginaDuplicados)
	pInv("POST /duplicados/resolver", s.acaoResolverMaquinas)
	pInv("POST /usuarios/resolver", s.acaoResolverUsuarios)
	pInv("POST /macs/ignorar", s.acaoIgnorarMAC)
	pInv("POST /macs/remover", s.acaoRemoverIgnorado)
	pInv("GET /exportar/inventario.xlsx", s.exportarXLSX)
	pInv("GET /exportar/maquinas.csv", s.exportarCSVMaquinas)
	pInv("GET /exportar/usuarios.csv", s.exportarCSVUsuarios)

	// --- Administração (só administrador) ---
	pAdm("GET /admin", s.paginaAdmin)
	pAdm("POST /admin/criterios-nivel", s.acaoCriteriosNivel)
	pAdm("POST /admin/senha", s.acaoAlterarSenha)
	pAdm("POST /admin/novo", s.acaoNovoAdmin)
	pAdm("POST /admin/{id}/excluir", s.acaoExcluirAdmin)
	pAdm("POST /admin/{id}/papel", s.acaoPapel)
	pAdm("POST /admin/token", s.acaoNovoToken)
	pAdm("POST /admin/token/aceitar", s.acaoAceitarToken)
	pAdm("POST /admin/token/remover", s.acaoRemoverTokenAceito)
	pAdm("POST /admin/endereco", s.acaoEndereco)
	pAdm("POST /admin/monitorado", s.acaoProgramaMonitorado)
	pAdm("POST /admin/auto-update", s.acaoAutoUpdate)
	pAdm("GET /admin/conexoes", s.paginaConexoes)
	pAdm("POST /admin/conexoes/testar", s.acaoTestarConexoes)
	pAdm("POST /admin/conexoes/avisar", s.acaoAvisarVersao)
	pAdm("GET /admin/backup", s.baixarBackup)
	pAdm("POST /admin/webhook", s.acaoWebhook)
	pAdm("POST /admin/webhook/testar", s.acaoTestarWebhook)
	pAdm("GET /admin/agente/exe", s.baixarAgenteExe)
	pAdm("GET /admin/agente/config", s.baixarAgenteConfig)
	pAdm("GET /admin/agente/zip", s.baixarAgenteZip)
	pAdm("POST /admin/atualizar", s.acaoAtualizar)
	pAdm("POST /admin/migrar", s.acaoMigrar)
	pAdm("POST /admin/migrar/autorizar", s.acaoAutorizarMigracao)
	mux.HandleFunc("POST /admin/importar-banco", s.protegido(s.exige(s.acaoImportarBanco, func(a Admin) bool { return a.EhAdmin() })))
	// "Minha conta" (trocar a própria senha) para qualquer usuário logado
	p("GET /configuracoes", s.paginaConfiguracoes)
	p("POST /configuracoes/aparencia", s.acaoAparencia)
	pAdm("POST /configuracoes/setores", s.acaoSetores)
	pAdm("POST /configuracoes/monitor", s.acaoConfigMonitor)
	p("GET /conta", s.paginaConta)
	p("POST /conta/senha", s.acaoAlterarSenha)
	return limitarConcorrencia(cabecalhosSeguranca(mux), 24)
}

func cabecalhosSeguranca(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}

// limitarConcorrencia garante que no máximo n requisições sejam processadas ao mesmo tempo.
// As demais aguardam um pouco; isso protege o servidor em picos de carga e evita sobrecarregar
// o banco de dados. Arquivos estáticos (CSS, imagens) passam direto, sem ocupar vaga.
func limitarConcorrencia(h http.Handler, n int) http.Handler {
	vagas := make(chan struct{}, n)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// arquivos estáticos e as conexões de eventos (longas, uma por máquina) não ocupam vaga
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/api/v1/eventos" {
			h.ServeHTTP(w, r)
			return
		}
		select {
		case vagas <- struct{}{}:
			defer func() { <-vagas }()
			h.ServeHTTP(w, r)
		case <-time.After(30 * time.Second):
			http.Error(w, "servidor ocupado, tente novamente em instantes", http.StatusServiceUnavailable)
		}
	})
}

// ---------------- infraestrutura ----------------

const cookieSessao = "inventario_sessao"

func (s *Servidor) protegido(h func(http.ResponseWriter, *http.Request, *Sessao)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieSessao)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		sess, err := s.b.SessaoPorToken(c.Value)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost {
			if r.FormValue("csrf") != sess.CSRF {
				http.Error(w, "Requisição inválida (token CSRF). Recarregue a página e tente novamente.", http.StatusForbidden)
				return
			}
		}
		h(w, r, sess)
	}
}

// exige envolve um handler protegido com uma checagem de permissão por papel.
func (s *Servidor) exige(h func(http.ResponseWriter, *http.Request, *Sessao), permitido func(Admin) bool) func(http.ResponseWriter, *http.Request, *Sessao) {
	return func(w http.ResponseWriter, r *http.Request, sess *Sessao) {
		if !permitido(sess.Admin) {
			redirecionar(w, r, "/chamados", "", fmt.Errorf("você não tem permissão para acessar essa área"))
			return
		}
		h(w, r, sess)
	}
}

// paginaInicial manda o usuário para a área certa conforme a permissão.
func (s *Servidor) paginaInicial(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	if sess.Admin.PodeGerenciarChamados() {
		s.paginaInicio(w, r, sess) // painel "Início" (inicio.go)
		return
	}
	http.Redirect(w, r, "/chamados", http.StatusSeeOther)
}

type dadosPagina struct {
	MigrandoPara string // servidor antigo durante a migração para a nuvem
	Titulo       string
	Menu         string
	Secao        string // chamados | inventario
	Sessao       *Sessao
	Msg          string
	Erro         string
	Resumo       Resumo
	Versao       string
	D            map[string]any
}

func (s *Servidor) render(w http.ResponseWriter, r *http.Request, pagina, titulo, menu string, sess *Sessao, d map[string]any) {
	t, ok := s.paginas[pagina]
	if !ok {
		http.Error(w, "página não encontrada", http.StatusInternalServerError)
		return
	}
	secao := "inventario"
	switch {
	case menu == "inicio":
		secao = "inicio"
	case menu == "chamados_relatorio":
		secao = "relatorios"
	case strings.HasPrefix(menu, "chamados"):
		secao = "chamados"
	case menu == "config":
		secao = "config"
	case strings.HasPrefix(menu, "admin"):
		secao = "admin"
	case strings.HasPrefix(menu, "ramais"):
		secao = "ramais"
	}
	dp := dadosPagina{Titulo: titulo, Menu: menu, Secao: secao, Sessao: sess, Msg: r.URL.Query().Get("msg"), Erro: r.URL.Query().Get("erro"), Versao: s.Versao, D: d}
	if destino, ok := s.migracaoAtiva(); ok {
		dp.MigrandoPara = destino
	}
	if sess != nil {
		dp.Resumo = s.b.Resumo()
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout.html", dp); err != nil {
		log.Printf("erro no template %s: %v", pagina, err)
		http.Error(w, "erro ao montar a página", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func redirecionar(w http.ResponseWriter, r *http.Request, destino, msg string, err error) {
	v := url.Values{}
	if err != nil {
		v.Set("erro", err.Error())
	} else if msg != "" {
		v.Set("msg", msg)
	}
	if len(v) > 0 {
		// a mensagem vai antes da âncora (#...), senão o navegador a descarta
		ancora := ""
		if i := strings.Index(destino, "#"); i >= 0 {
			destino, ancora = destino[:i], destino[i:]
		}
		if strings.Contains(destino, "?") {
			destino += "&" + v.Encode()
		} else {
			destino += "?" + v.Encode()
		}
		destino += ancora
	}
	http.Redirect(w, r, destino, http.StatusSeeOther)
}

func idDaURL(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func ipCliente(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// ---------------- API do agente ----------------

func (s *Servidor) apiRelatorio(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Agente-Token")
	tokOK, tokAntigo := s.b.validarTokenAgente(token)
	if !tokOK {
		s.b.registrarRecusa(token, ipCliente(r), r.Body)
		http.Error(w, "token inválido", http.StatusUnauthorized)
		return
	}
	var rel modelo.Relatorio
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&rel); err != nil {
		http.Error(w, "JSON inválido: "+err.Error(), http.StatusBadRequest)
		return
	}
	// máquina excluída (na lixeira): aceita o envio mas não recadastra
	if s.b.NaLixeira(rel.AgentID) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(modelo.Resposta{OK: true, Monitorar: s.b.ConfigMonitor().Alvo()})
		return
	}
	// o histórico de chamadas do MicroSIP vai para tabela própria (não para o dados_json)
	microsip := rel.MicroSIP
	rel.MicroSIP = nil
	for _, m := range microsip {
		m.Chamadas = nil
		rel.MicroSIP = append(rel.MicroSIP, m)
	}
	res, err := s.b.Ingerir(&rel, ipCliente(r))
	if err != nil {
		log.Printf("erro ao gravar relatório de %s: %v", rel.Hostname, err)
		http.Error(w, "erro ao gravar: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.b.GravarAnyDesk(res.MaquinaID, &rel)
	s.b.GravarMonitor(res.MaquinaID, &rel)
	if err := s.b.GravarRamais(res.MaquinaID, microsip, rel.AgentVersion); err != nil {
		log.Printf("ramais de %s: %v", rel.Hostname, err)
	}
	estado := "atualizada"
	if res.Nova {
		estado = "NOVA"
	}
	log.Printf("relatório recebido: %s (%s) máquina #%d %s", rel.Hostname, ipCliente(r), res.MaquinaID, estado)
	for _, g := range res.NovosAvisos {
		var nomes []string
		for _, m := range g.Maquinas {
			nomes = append(nomes, fmt.Sprintf("%s (#%d)", m.Hostname, m.ID))
		}
		msg := fmt.Sprintf("Inventário: MAC duplicado %s encontrado nas máquinas %s", g.MAC, strings.Join(nomes, ", "))
		log.Printf("ALERTA: %s", msg)
		go s.enviarWebhook(msg)
	}
	var dup []string
	for _, g := range res.Duplicados {
		dup = append(dup, g.MAC)
	}
	resp := modelo.Resposta{OK: true, MaquinaID: res.MaquinaID, Nova: res.Nova, MACDuplicado: dup, Monitorar: s.b.ConfigMonitor().Alvo()}
	if tokAntigo {
		resp.NovoToken = s.b.Config("token_agente") // agentes 1.11.2+ passam a usar o token atual
	}
	// A migração só é anunciada aos agentes depois de ser AUTORIZADA manualmente por um
	// administrador (migrar_ativa=1). Enquanto não for autorizada, o destino fica só salvo.
	// Se houver uma fase canário (lista de hostnames), só essas máquinas recebem a ordem.
	if destino := strings.TrimRight(s.b.Config("migrar_servidor"), "/"); destino != "" && s.b.Config("migrar_ativa") == "1" {
		if !strings.EqualFold(destino, strings.TrimRight(rel.ServidorAtual, "/")) && noCanario(s.b.Config("migrar_canario"), rel.Hostname) {
			resp.Migrar = &modelo.Migracao{Servidor: destino, Token: s.b.Config("migrar_token")}
		}
	}
	// Auto-update: se o agente está numa versão anterior à que este servidor distribui, avisa
	// para ele baixar e trocar o próprio executável. Desligável com a config "auto_update=0".
	if s.b.Config("auto_update") != "0" && payloadConfere() && versaoMenor(rel.AgentVersion, s.VersaoAgente) {
		resp.Atualizar = &modelo.Atualizacao{Versao: s.VersaoAgente, URL: s.baseURL(r) + "/api/v1/agente.exe"}
		log.Printf("auto-update: oferecida a v%s para %s (está na v%s)", s.VersaoAgente, rel.Hostname, rel.AgentVersion)
	}
	s.b.RegistrarSituacaoAutoUpdate(res.MaquinaID, resp.Atualizar != nil, s.VersaoAgente, rel.AutoUpdateErro)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// apiAgenteExe entrega o executável do agente embutido, autenticando pelo token do agente.
// É o endpoint que o auto-update usa para baixar a versão nova.
func (s *Servidor) apiAgenteExe(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Agente-Token")
	if ok, _ := s.b.validarTokenAgente(token); !ok {
		http.Error(w, "token inválido", http.StatusUnauthorized)
		return
	}
	host := s.b.RegistrarDownloadAgente(ipCliente(r))
	log.Printf("auto-update: agente v%s baixado por %s (%s)", s.VersaoAgente, ipCliente(r), host)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Agente-Versao", s.VersaoAgente)
	_, _ = w.Write(agenteExe)
}

// versaoMenor diz se a versão a é anterior a b ("1.5.0" < "1.6.0").
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

// noCanario diz se um hostname deve receber a migração. Lista vazia = todas as máquinas;
// caso contrário, só os hostnames listados (separados por vírgula, sem diferenciar maiúsculas).
func noCanario(lista, hostname string) bool {
	lista = strings.TrimSpace(lista)
	if lista == "" {
		return true
	}
	for _, h := range strings.Split(lista, ",") {
		if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(hostname)) {
			return true
		}
	}
	return false
}

// saude é um endpoint público e leve para monitorar se o servidor está no ar.
func (s *Servidor) saude(w http.ResponseWriter, r *http.Request) {
	var n int
	_ = s.b.db.QueryRow(`SELECT COUNT(*) FROM maquinas`).Scan(&n)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "versao": s.Versao, "maquinas": n, "hora": time.Now().Format(time.RFC3339)})
}

// BackupAutomatico grava um backup por dia na pasta informada e remove os mais antigos que
// "dias". Roda em segundo plano enquanto o servidor estiver no ar.
func (s *Servidor) BackupAutomatico(dir string, dias int) {
	s.dirBackup = dir
	go func() {
		for {
			_ = os.MkdirAll(dir, 0o755)
			nome := filepath.Join(dir, "inventario_"+time.Now().Format("2006-01-02")+".db")
			if _, err := os.Stat(nome); err != nil {
				if err := s.b.Backup(nome); err != nil {
					log.Printf("backup automático falhou: %v", err)
				} else {
					log.Printf("backup automático gravado: %s", nome)
				}
			}
			limparBackupsAntigos(dir, dias)
			time.Sleep(6 * time.Hour)
		}
	}()
}

func limparBackupsAntigos(dir string, dias int) {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	limite := time.Now().AddDate(0, 0, -dias)
	for _, e := range entradas {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "inventario_") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		data := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "inventario_"), ".db")
		t, err := time.Parse("2006-01-02", data)
		if err == nil && t.Before(limite) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

func (s *Servidor) enviarWebhook(msg string) error {
	u := s.b.Config("webhook_url")
	if u == "" {
		return nil
	}
	return s.postarWebhook(u, msg)
}

// postarWebhook envia a mensagem no formato aceito por Teams (webhook de entrada), Slack,
// Google Chat ("text") e Discord ("content").
func (s *Servidor) postarWebhook(u, msg string) error {
	corpo, _ := json.Marshal(map[string]string{"text": msg, "content": msg})
	cli := &http.Client{Timeout: 15 * time.Second}
	resp, err := cli.Post(u, "application/json", bytes.NewReader(corpo))
	if err != nil {
		log.Printf("webhook: %v", err)
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("webhook respondeu %d", resp.StatusCode)
		return fmt.Errorf("o webhook respondeu %d", resp.StatusCode)
	}
	return nil
}

// ---------------- login ----------------

func (s *Servidor) paginaLogin(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "login", "Entrar", "", nil, nil)
}

func (s *Servidor) acaoLogin(w http.ResponseWriter, r *http.Request) {
	ip := ipCliente(r)
	if s.lim.bloqueado(ip) {
		redirecionar(w, r, "/login", "", fmt.Errorf("muitas tentativas; aguarde 10 minutos"))
		return
	}
	sess, err := s.b.Entrar(r.FormValue("usuario"), r.FormValue("senha"))
	if err != nil {
		s.lim.registrarFalha(ip)
		redirecionar(w, r, "/login", "", err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieSessao, Value: sess.Token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: 12 * 3600})
	s.b.Auditar(sess.Admin.Usuario, "login", ip)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Servidor) acaoSair(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	s.b.Sair(sess.Token)
	http.SetCookie(w, &http.Cookie{Name: cookieSessao, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---------------- páginas ----------------

func (s *Servidor) paginaPainel(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	maqs, _ := s.b.Maquinas("")
	dup, _ := s.b.DuplicadosMAC()
	// últimas máquinas que se comunicaram
	recentes := append([]*Maquina(nil), maqs...)
	for i := 0; i < len(recentes); i++ {
		for j := i + 1; j < len(recentes); j++ {
			if recentes[j].UltimoContato > recentes[i].UltimoContato {
				recentes[i], recentes[j] = recentes[j], recentes[i]
			}
		}
	}
	if len(recentes) > 10 {
		recentes = recentes[:10]
	}
	p := s.b.EstatisticasPainel()
	rosca := grafRosca([]Fatia{
		{Rotulo: "Ativas (até 7 dias)", Valor: p.Ativas, Cor: "var(--verde)"},
		{Rotulo: "Ociosas (7 a 14 dias)", Valor: p.Ociosas, Cor: "var(--amarelo)"},
		{Rotulo: "Inativas (+14 dias)", Valor: p.Inativas, Cor: "var(--vermelho)"},
	})
	grafSO := grafBarras(p.SOs, "var(--pri)")
	grafSetor := grafBarras(p.Setores, "var(--pri)")
	// distribuição de versões do agente, destacando a versão atual do servidor (a mais nova)
	verc := map[string]int{}
	var ordemVer []string
	desatualizados, semAnyDesk := 0, 0
	for _, m := range maqs {
		if anyDeskAusente(m.AnyDeskSituacao) {
			semAnyDesk++
		}
		v := strings.TrimSpace(m.VersaoAgente)
		if v == "" {
			v = "(desconhecida)"
		}
		if verc[v] == 0 {
			ordemVer = append(ordemVer, v)
		}
		verc[v]++
		if serieVersao(v) != serieVersao(s.VersaoAgente) {
			desatualizados++
		}
	}
	var versoes []Fatia
	for _, v := range ordemVer {
		cor := "var(--amarelo)"
		rot := "v" + v
		if v == s.VersaoAgente {
			cor = "var(--verde)"
			rot = "v" + v + " (atual)"
		} else if v == "(desconhecida)" {
			cor, rot = "var(--vermelho)", v
		}
		versoes = append(versoes, Fatia{Rotulo: rot, Valor: verc[v], Cor: cor})
	}
	ordenarFatias(versoes)
	grafVersoes := grafBarras(versoes, "var(--pri)")
	nAlertas := 0
	if al, err := s.b.Alertas(); err == nil {
		nAlertas = len(al)
	}
	// relatório de nível (saúde) do parque
	pn := s.b.ParamsNivel()
	nOK, nAlerta, nCritico := 0, 0, 0
	for _, m := range maqs {
		switch s.b.Avaliar(m, pn).Classe {
		case "OK":
			nOK++
		case "Alerta":
			nAlerta++
		default:
			nCritico++
		}
	}
	roscaNivel := grafRosca([]Fatia{
		{Rotulo: "OK", Valor: nOK, Cor: "var(--verde)"},
		{Rotulo: "Alerta", Valor: nAlerta, Cor: "var(--amarelo)"},
		{Rotulo: "Crítico", Valor: nCritico, Cor: "var(--vermelho)"},
	})
	// Programa monitorado (ex.: Work Monitor): quantas máquinas estão sem ele.
	prog := s.programaMonitorado()
	semMonitorado := 0
	for _, m := range maqs {
		if monitorAusente(m.MonitorSituacao) {
			semMonitorado++
		}
	}
	s.render(w, r, "painel", "Painel", "painel", sess, map[string]any{
		"Recentes": recentes, "Duplicados": dup, "P": p,
		"Rosca": rosca, "GrafSO": grafSO, "GrafSetor": grafSetor, "NAlertas": nAlertas,
		"GrafVersoes": grafVersoes, "Desatualizados": desatualizados, "SemAnyDesk": semAnyDesk, "Versao": s.Versao, "VersaoAgente": s.VersaoAgente,
		"RoscaNivel": roscaNivel, "NOK": nOK, "NAlerta": nAlerta, "NCritico": nCritico,
		"Monitorado": prog, "SemMonitorado": semMonitorado, "TotalMaq": len(maqs),
	})
}

func (s *Servidor) paginaMaquinas(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	q := r.URL.Query().Get("q")
	maqs, err := s.b.Maquinas(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	filtro := r.URL.Query().Get("filtro")
	nomeFiltro := ""
	switch filtro {
	case "duplicadas":
		nomeFiltro = "Com MAC duplicado"
		var f []*Maquina
		for _, m := range maqs {
			if m.Duplicada {
				f = append(f, m)
			}
		}
		maqs = f
	case "genericas":
		nomeFiltro = "Com nome genérico"
		var f []*Maquina
		for _, m := range maqs {
			if nomeGenerico(m.Hostname, s.b.Config("alerta_generico_padroes")) {
				f = append(f, m)
			}
		}
		maqs = f
	case "desatualizadas":
		nomeFiltro = "Agente desatualizado"
		var f []*Maquina
		for _, m := range maqs {
			if serieVersao(strings.TrimSpace(m.VersaoAgente)) != serieVersao(s.VersaoAgente) {
				f = append(f, m)
			}
		}
		maqs = f
	case "sem_anydesk":
		nomeFiltro = "Sem AnyDesk instalado"
		var f []*Maquina
		for _, m := range maqs {
			if anyDeskAusente(m.AnyDeskSituacao) {
				f = append(f, m)
			}
		}
		maqs = f
	case "nivel_ok", "nivel_alerta", "nivel_critico":
		classe := map[string]string{"nivel_ok": "OK", "nivel_alerta": "Alerta", "nivel_critico": "Crítico"}[filtro]
		nomeFiltro = "Nível: " + classe
		pn := s.b.ParamsNivel()
		var f []*Maquina
		for _, m := range maqs {
			if s.b.Avaliar(m, pn).Classe == classe {
				f = append(f, m)
			}
		}
		maqs = f
	case "sem_monitorado", "com_monitorado":
		prog := s.programaMonitorado()
		querTem := filtro == "com_monitorado"
		nomeFiltro = map[bool]string{true: "Com " + prog, false: "Sem " + prog}[querTem]
		var f []*Maquina
		for _, m := range maqs {
			tem := m.MonitorSituacao == "rodando" || m.MonitorSituacao == "instalado"
			if (querTem && tem) || (!querTem && monitorAusente(m.MonitorSituacao)) {
				f = append(f, m)
			}
		}
		maqs = f
	default:
		if id, _ := strconv.ParseInt(filtro, 10, 64); id > 0 {
			if f, err := s.b.Filtro(id); err == nil {
				nomeFiltro = f.Nome
				maqs = AplicarFiltro(maqs, f)
			}
		}
	}
	filtros, _ := s.b.Filtros()
	pn := s.b.ParamsNivel()
	aval := map[int64]Avaliacao{}
	for _, m := range maqs {
		aval[m.ID] = s.b.Avaliar(m, pn)
	}
	s.render(w, r, "maquinas", "Máquinas", "maquinas", sess, map[string]any{
		"Maquinas": maqs, "Q": q, "Filtro": filtro, "NomeFiltro": nomeFiltro, "Filtros": filtros, "Versao": s.VersaoAgente,
		"Aval": aval, "Monitorado": s.programaMonitorado(),
	})
}

func (s *Servidor) acaoCriarFiltro(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	err := s.b.CriarFiltro(r.FormValue("nome"), r.FormValue("campo"), r.FormValue("operador"), r.FormValue("valor"))
	redirecionar(w, r, "/maquinas", "Filtro salvo.", err)
}

func (s *Servidor) acaoExcluirFiltro(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	err := s.b.ExcluirFiltro(idDaURL(r))
	redirecionar(w, r, "/maquinas", "Filtro excluído.", err)
}

func (s *Servidor) paginaNotificacoes(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	alertas, _ := s.b.Alertas()
	dias, pct := s.b.LimitesAlertas()
	porTipo := map[string][]Alerta{}
	for _, a := range alertas {
		porTipo[a.Tipo] = append(porTipo[a.Tipo], a)
	}
	s.render(w, r, "notificacoes", "Notificações", "notificacoes", sess, map[string]any{
		"Genericos": porTipo["generico"], "Inativas": porTipo["inativa"], "Discos": porTipo["disco"], "AnyDesks": porTipo["anydesk"],
		"LigAnyDesk": s.b.Config("alerta_anydesk") != "0",
		"RamaisDup":  s.b.RamaisDuplicados(),
		"Monitores":  porTipo["monitor"], "LigMonitor": s.b.Config("alerta_monitor") != "0", "CfgMonitor": s.b.ConfigMonitor(),
		"LigGenerico": s.b.Config("alerta_generico") == "1",
		"PadroesGen":  s.b.Config("alerta_generico_padroes"),
		"DiasInativa": dias, "PctDisco": pct, "LigInativa": dias > 0, "LigDisco": pct > 0,
		"ValorDias": valorOuPadrao(s.b.Config("alerta_inativa_dias"), 14), "ValorPct": valorOuPadrao(s.b.Config("alerta_disco_pct"), 10),
	})
}

func (s *Servidor) acaoNotificacoes(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	liga := func(campo string) string {
		if r.FormValue(campo) == "1" {
			return "1"
		}
		return "0"
	}
	gen := ""
	if liga("generico") == "1" {
		gen = "1"
	}
	_ = s.b.DefinirConfig("alerta_generico", gen)
	_ = s.b.DefinirConfig("alerta_generico_padroes", strings.TrimSpace(r.FormValue("padroes")))
	_ = s.b.DefinirConfig("alerta_inativa", liga("inativa"))
	if v := soNumero(r.FormValue("dias_inativa")); v != "" {
		_ = s.b.DefinirConfig("alerta_inativa_dias", v)
	}
	_ = s.b.DefinirConfig("alerta_disco", liga("disco"))
	_ = s.b.DefinirConfig("alerta_anydesk", liga("anydesk"))
	_ = s.b.DefinirConfig("alerta_monitor", liga("monitor"))
	if v := soNumero(r.FormValue("disco_pct")); v != "" {
		_ = s.b.DefinirConfig("alerta_disco_pct", v)
	}
	dias, pct := s.b.LimitesAlertas()
	s.b.Auditar(sess.Admin.Usuario, "configurar notificações", fmt.Sprintf("genéricos=%s, inativas=%d dias, disco=%d%%", map[string]string{"1": "ligado", "": "desligado"}[gen], dias, pct))
	redirecionar(w, r, "/notificacoes", "Avisos atualizados.", nil)
}

func valorOuPadrao(s string, padrao int) int {
	n := 0
	fmt.Sscan(strings.TrimSpace(s), &n)
	if n <= 0 {
		return padrao
	}
	return n
}

func soNumero(s string) string {
	n := 0
	fmt.Sscan(strings.TrimSpace(s), &n)
	if n <= 0 {
		return ""
	}
	return fmt.Sprint(n)
}

func (s *Servidor) paginaMaquina(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	m, err := s.b.Maquina(idDaURL(r))
	if err != nil {
		redirecionar(w, r, "/maquinas", "", fmt.Errorf("máquina não encontrada"))
		return
	}
	var rel modelo.Relatorio
	_ = json.Unmarshal([]byte(m.DadosJSON), &rel)
	ifs, _ := s.b.Interfaces(m.ID)
	logins, _ := s.b.Logins(m.ID, 0)
	todas, _ := s.b.Maquinas("")
	// MACs desta máquina presentes em outras
	dupMACs := map[string]bool{}
	if grupos, err := s.b.DuplicadosMAC(); err == nil {
		for _, g := range grupos {
			for _, x := range g.Maquinas {
				if x.ID == m.ID {
					dupMACs[g.MAC] = true
				}
			}
		}
	}
	var outras []*Maquina
	for _, x := range todas {
		if x.ID != m.ID {
			outras = append(outras, x)
		}
	}
	s.render(w, r, "maquina", m.Hostname, "maquinas", sess, map[string]any{"M": m, "R": rel, "Interfaces": ifs, "Logins": logins, "Outras": outras, "DupMACs": dupMACs, "Aval": s.b.Avaliar(m, s.b.ParamsNivel()), "Monitorado": s.programaMonitorado()})
}

func (s *Servidor) acaoEditarMaquina(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	err := s.b.AtualizarMaquina(id, strings.TrimSpace(r.FormValue("patrimonio")), strings.TrimSpace(r.FormValue("setor")),
		strings.TrimSpace(r.FormValue("responsavel")), strings.TrimSpace(r.FormValue("localizacao")), strings.TrimSpace(r.FormValue("observacoes")))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "editar máquina", fmt.Sprint("#", id))
	}
	redirecionar(w, r, fmt.Sprintf("/maquinas/%d", id), "Dados salvos.", err)
}

func (s *Servidor) acaoExcluirMaquina(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	m, _ := s.b.Maquina(id)
	err := s.b.MoverMaquinaParaLixeira(id, sess.Admin.Usuario)
	if err == nil && m != nil {
		s.b.Auditar(sess.Admin.Usuario, "excluir máquina (lixeira)", fmt.Sprintf("#%d %s", id, m.Hostname))
	}
	voltar := r.FormValue("voltar")
	if voltar == "" || !strings.HasPrefix(voltar, "/") || strings.HasPrefix(voltar, "//") {
		voltar = "/maquinas"
	}
	redirecionar(w, r, voltar, "Máquina movida para a Lixeira — dá para restaurar em Inventário → Lixeira.", err)
}

func (s *Servidor) acaoMesclarMaquina(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	origem := idDaURL(r)
	destino, _ := strconv.ParseInt(r.FormValue("destino"), 10, 64)
	err := s.b.MesclarMaquinas(origem, destino)
	if err != nil {
		redirecionar(w, r, fmt.Sprintf("/maquinas/%d", origem), "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "mesclar máquinas", fmt.Sprintf("#%d -> #%d", origem, destino))
	redirecionar(w, r, fmt.Sprintf("/maquinas/%d", destino), "Máquinas mescladas: o registro duplicado foi removido.", nil)
}

func (s *Servidor) paginaUsuarios(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	q := r.URL.Query().Get("q")
	us, err := s.b.Usuarios(q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	dup, _ := s.b.UsuariosDuplicados()
	ocultar := s.b.OcultarUsuariosLocais()
	locais := 0
	var visiveis []*Usuario
	for _, u := range us {
		if u.Local {
			locais++
			if ocultar {
				continue
			}
		}
		visiveis = append(visiveis, u)
	}
	s.render(w, r, "usuarios", "Usuários", "usuarios", sess, map[string]any{"Usuarios": visiveis, "Q": q, "Duplicados": dup,
		"Ocultar": ocultar, "Locais": locais})
}

func (s *Servidor) acaoOcultarLocais(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	v := "0"
	if r.FormValue("ocultar") == "1" {
		v = "1"
	}
	err := s.b.DefinirConfig("ocultar_usuarios_locais", v)
	redirecionar(w, r, "/usuarios", "", err)
}

func (s *Servidor) paginaUsuario(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	u, err := s.b.Usuario(idDaURL(r))
	if err != nil {
		redirecionar(w, r, "/usuarios", "", fmt.Errorf("usuário não encontrado"))
		return
	}
	logins, _ := s.b.Logins(0, u.ID)
	todos, _ := s.b.Usuarios("")
	var outros, sugeridos []*Usuario
	for _, x := range todos {
		if x.ID == u.ID {
			continue
		}
		outros = append(outros, x)
		if chaveCurta(x.Login) == chaveCurta(u.Login) {
			sugeridos = append(sugeridos, x)
		}
	}
	var apelidos []string
	if u.Apelidos != "" {
		apelidos = strings.Split(u.Apelidos, ", ")
	}
	s.render(w, r, "usuario", u.Login, "usuarios", sess, map[string]any{"U": u, "Logins": logins, "Outros": outros, "Sugeridos": sugeridos, "Apelidos": apelidos})
}

func (s *Servidor) acaoEditarUsuario(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	err := s.b.AtualizarUsuario(id, r.FormValue("login"), strings.TrimSpace(r.FormValue("nome")), strings.TrimSpace(r.FormValue("email")),
		strings.TrimSpace(r.FormValue("setor")), strings.TrimSpace(r.FormValue("observacoes")))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "editar usuário", fmt.Sprintf("#%d %s", id, r.FormValue("login")))
	}
	redirecionar(w, r, fmt.Sprintf("/usuarios/%d", id), "Usuário atualizado.", err)
}

func (s *Servidor) acaoExcluirUsuario(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	u, _ := s.b.Usuario(id)
	err := s.b.ExcluirUsuario(id)
	if err == nil && u != nil {
		s.b.Auditar(sess.Admin.Usuario, "excluir usuário", fmt.Sprintf("#%d %s", id, u.Login))
	}
	redirecionar(w, r, "/usuarios", "Usuário excluído.", err)
}

func (s *Servidor) acaoMesclarUsuario(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	origem := idDaURL(r)
	destino, _ := strconv.ParseInt(r.FormValue("destino"), 10, 64)
	err := s.b.MesclarUsuarios(origem, destino)
	if err != nil {
		redirecionar(w, r, fmt.Sprintf("/usuarios/%d", origem), "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "mesclar usuários", fmt.Sprintf("#%d -> #%d", origem, destino))
	redirecionar(w, r, fmt.Sprintf("/usuarios/%d", destino), "Usuários mesclados. O login antigo virou apelido deste cadastro.", nil)
}

func (s *Servidor) acaoIgnorarUsuario(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	ignorar := r.FormValue("ignorar") == "1"
	err := s.b.IgnorarUsuario(id, ignorar)
	if err == nil {
		acao := "desmarcar usuário local"
		if ignorar {
			acao = "marcar usuário como local"
		}
		s.b.Auditar(sess.Admin.Usuario, acao, fmt.Sprint("#", id))
	}
	msg := "Usuário desmarcado."
	if ignorar {
		msg = "Usuário marcado como local — não entra mais na checagem de duplicados."
	}
	redirecionar(w, r, fmt.Sprintf("/usuarios/%d", id), msg, err)
}

func (s *Servidor) acaoCriteriosNivel(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	_ = s.b.DefinirConfig("nivel_ok_min", soNumero(r.FormValue("ok_min")))
	_ = s.b.DefinirConfig("nivel_alerta_min", soNumero(r.FormValue("alerta_min")))
	_ = s.b.DefinirConfig("nivel_ram_critico", soNumero(r.FormValue("ram_critico")))
	s.b.Auditar(sess.Admin.Usuario, "ajustar critérios de nível", "")
	redirecionar(w, r, "/admin", "Critérios de nível salvos.", nil)
}

func (s *Servidor) acaoRemoverApelido(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	err := s.b.RemoverApelido(r.FormValue("apelido"))
	redirecionar(w, r, fmt.Sprintf("/usuarios/%d", id), "Apelido removido.", err)
}

func (s *Servidor) paginaDuplicados(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	macs, _ := s.b.DuplicadosMAC()
	nomes, _ := s.b.DuplicadosHostname()
	users, _ := s.b.UsuariosDuplicados()
	ign, _ := s.b.MACsIgnorados()
	s.render(w, r, "duplicados", "Duplicados", "duplicados", sess, map[string]any{"MACs": macs, "Nomes": nomes, "Usuarios": users, "Ignorados": ign})
}

func idsDoForm(r *http.Request) (int64, []int64) {
	_ = r.ParseForm()
	destino, _ := strconv.ParseInt(r.FormValue("destino"), 10, 64)
	var origens []int64
	for _, v := range r.Form["origem"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id != destino {
			origens = append(origens, id)
		}
	}
	return destino, origens
}

func (s *Servidor) acaoResolverMaquinas(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	destino, origens := idsDoForm(r)
	for _, o := range origens {
		if err := s.b.MesclarMaquinas(o, destino); err != nil {
			redirecionar(w, r, "/duplicados", "", err)
			return
		}
		s.b.Auditar(sess.Admin.Usuario, "mesclar máquinas", fmt.Sprintf("#%d -> #%d", o, destino))
	}
	redirecionar(w, r, "/duplicados", fmt.Sprintf("%d registro(s) mesclado(s) na máquina #%d.", len(origens), destino), nil)
}

func (s *Servidor) acaoResolverUsuarios(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	destino, origens := idsDoForm(r)
	for _, o := range origens {
		if err := s.b.MesclarUsuarios(o, destino); err != nil {
			redirecionar(w, r, "/duplicados#usuarios", "", err)
			return
		}
		s.b.Auditar(sess.Admin.Usuario, "mesclar usuários", fmt.Sprintf("#%d -> #%d", o, destino))
	}
	redirecionar(w, r, "/duplicados", fmt.Sprintf("%d usuário(s) mesclado(s).", len(origens)), nil)
}

func (s *Servidor) acaoIgnorarMAC(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	err := s.b.IgnorarMAC(r.FormValue("mac"), strings.TrimSpace(r.FormValue("motivo")))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "ignorar MAC", r.FormValue("mac"))
	}
	redirecionar(w, r, "/duplicados", "MAC adicionado à lista de ignorados.", err)
}

func (s *Servidor) acaoRemoverIgnorado(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	err := s.b.RemoverIgnorado(r.FormValue("mac"))
	redirecionar(w, r, "/duplicados", "MAC removido da lista de ignorados.", err)
}

// ---------------- administração ----------------

func (s *Servidor) paginaAdmin(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	admins, _ := s.b.Admins()
	aud, _ := s.b.Auditoria(100)
	s.render(w, r, "admin", "Administração", "admin", sess, map[string]any{
		"Admins": admins, "Auditoria": aud, "Token": s.b.Config("token_agente"), "Webhook": s.b.Config("webhook_url"),
		"URL": s.baseURL(r), "EnderecoFixo": s.b.Config("endereco_servidor"), "Versao": s.Versao, "VersaoAgente": s.VersaoAgente,
		"Migracao": s.b.Migracao(), "MigrarToken": s.b.Config("migrar_token"), "Canario": s.b.Config("migrar_canario"),
		"Nivel": s.b.ParamsNivel(), "AutoUpdate": s.b.Config("auto_update") != "0",
		"DiagAU":    s.b.DiagnosticoAutoUpdate(s.VersaoAgente, s.b.Config("auto_update") != "0", payloadConfere()),
		"PayloadOK": payloadConfere(), "TokensRecusados": s.b.TokensRecusados(), "TokensAceitos": s.b.TokensAceitosResumo(),
		"Monitorado": s.programaMonitorado(), "MeuID": sess.Admin.ID,
		// chamados
		"SLA": s.slaAtual(), "ChamadosPublico": s.publicoAtivo(), "AvisarChamados": s.b.Config("avisar_chamados") != "0",
		"WebhookChamados": s.b.Config("webhook_chamados"), "LinkPublico": s.baseURL(r) + "/abrir-chamado",
		// e-mail
		"SMTP": s.b.ConfigSMTP(), "SMTPSenhaSalva": s.b.Config("smtp_senha") != "",
		"EmailAtribuicao": s.b.Config("email_atribuicao") != "0", "EmailEquipe": s.b.Config("email_equipe") == "1",
		"RespPadrao": s.responsavelAutomatico(), "Tecnicos": s.tecnicos(), "MeuEmail": sess.Admin.Email,
		"Envios": s.b.UltimosEnvios(15),
	})
}

func (s *Servidor) acaoAutoUpdate(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	v := "1"
	if r.FormValue("ativar") != "1" {
		v = "0"
	}
	err := s.b.DefinirConfig("auto_update", v)
	if err == nil {
		estado := "ligado"
		if v == "0" {
			estado = "desligado"
		}
		s.b.Auditar(sess.Admin.Usuario, "auto-update dos agentes", estado)
		s.eventos.enviar("versao", s.eventoVersao()) // máquinas conectadas ficam sabendo na hora
	}
	redirecionar(w, r, "/admin", "Preferência de auto-update salva.", err)
}

func (s *Servidor) programaMonitorado() string {
	return s.b.ConfigMonitor().Nome
}

func (s *Servidor) acaoProgramaMonitorado(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	nome := strings.TrimSpace(r.FormValue("programa"))
	err := s.b.DefinirConfig("programa_monitorado", nome)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "programa monitorado", nome)
	}
	redirecionar(w, r, "/admin", "Programa monitorado salvo.", err)
}

func (s *Servidor) acaoEndereco(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	e := strings.TrimRight(strings.TrimSpace(r.FormValue("endereco")), "/")
	if e != "" && !strings.HasPrefix(e, "http://") && !strings.HasPrefix(e, "https://") {
		redirecionar(w, r, "/admin", "", fmt.Errorf("o endereço deve começar com http:// ou https://"))
		return
	}
	err := s.b.DefinirConfig("endereco_servidor", e)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "definir endereço do servidor", e)
	}
	redirecionar(w, r, "/admin", "Endereço salvo. O instalador do agente passa a usar este endereço.", err)
}

func (s *Servidor) acaoAlterarSenha(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	voltar := r.FormValue("voltar")
	if voltar == "" {
		voltar = "/admin"
	}
	if r.FormValue("nova") != r.FormValue("confirma") {
		redirecionar(w, r, voltar, "", fmt.Errorf("a confirmação não confere com a nova senha"))
		return
	}
	err := s.b.AlterarSenha(sess.Admin.ID, r.FormValue("atual"), r.FormValue("nova"))
	if err != nil {
		redirecionar(w, r, voltar, "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "alterar senha", "")
	redirecionar(w, r, "/login", "Senha alterada. Entre novamente.", nil)
}

func (s *Servidor) paginaConta(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	s.render(w, r, "conta", "Minha conta", "chamados", sess, map[string]any{})
}

func (s *Servidor) acaoNovoAdmin(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	papel := r.FormValue("papel")
	err := s.b.CriarAdmin(r.FormValue("usuario"), r.FormValue("senha"), r.FormValue("nome"), r.FormValue("email"), papel)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "criar usuário", r.FormValue("usuario")+" ("+papel+")")
	}
	redirecionar(w, r, "/admin", "Usuário criado.", err)
}

func (s *Servidor) acaoPapel(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	err := s.b.AtualizarPapel(id, r.FormValue("papel"))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "alterar perfil", r.FormValue("papel"))
	}
	redirecionar(w, r, "/admin", "Perfil atualizado.", err)
}

func (s *Servidor) acaoExcluirAdmin(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	if id == sess.Admin.ID {
		redirecionar(w, r, "/admin", "", fmt.Errorf("você não pode excluir o próprio usuário"))
		return
	}
	err := s.b.ExcluirAdmin(id)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "excluir administrador", fmt.Sprint("#", id))
	}
	redirecionar(w, r, "/admin", "Administrador excluído.", err)
}

func (s *Servidor) acaoNovoToken(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	err := s.b.DefinirConfig("token_agente", aleatorio(20))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "gerar novo token de agente", "")
	}
	redirecionar(w, r, "/admin", "Novo token gerado. Atualize a configuração dos agentes (reinstale com -token).", err)
}

func (s *Servidor) acaoMigrar(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	destino := strings.TrimRight(strings.TrimSpace(r.FormValue("destino")), "/")
	if r.FormValue("cancelar") != "" {
		_ = s.b.DefinirConfig("migrar_servidor", "")
		_ = s.b.DefinirConfig("migrar_token", "")
		_ = s.b.DefinirConfig("migrar_ativa", "")
		_ = s.b.DefinirConfig("migrar_canario", "")
		s.b.Auditar(sess.Admin.Usuario, "cancelar migração de agentes", "")
		redirecionar(w, r, "/admin", "Migração cancelada. Os agentes continuam neste servidor.", nil)
		return
	}
	if destino != "" && !strings.HasPrefix(destino, "http://") && !strings.HasPrefix(destino, "https://") {
		redirecionar(w, r, "/admin", "", fmt.Errorf("o endereço deve começar com http:// ou https://"))
		return
	}
	_ = s.b.DefinirConfig("migrar_servidor", destino)
	_ = s.b.DefinirConfig("migrar_token", strings.TrimSpace(r.FormValue("token")))
	_ = s.b.DefinirConfig("migrar_canario", strings.TrimSpace(r.FormValue("canario")))
	// Salvar ou alterar o destino sempre desativa a autorização: é preciso autorizar de novo.
	_ = s.b.DefinirConfig("migrar_ativa", "")
	s.b.Auditar(sess.Admin.Usuario, "configurar migração de agentes", destino)
	redirecionar(w, r, "/admin", "Destino salvo. A migração está DESATIVADA até você autorizá-la com sua senha, abaixo.", nil)
}

// acaoAutorizarMigracao ativa ou pausa a migração. Ativar exige que o administrador digite a
// senha novamente, confirmando que autoriza os agentes a mudarem de servidor.
func (s *Servidor) acaoAutorizarMigracao(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	if r.FormValue("pausar") != "" {
		_ = s.b.DefinirConfig("migrar_ativa", "")
		s.b.Auditar(sess.Admin.Usuario, "pausar migração de agentes", "")
		redirecionar(w, r, "/admin", "Migração pausada. Os agentes que ainda não migraram permanecem neste servidor.", nil)
		return
	}
	destino := strings.TrimRight(s.b.Config("migrar_servidor"), "/")
	if destino == "" {
		redirecionar(w, r, "/admin", "", fmt.Errorf("configure o endereço de destino antes de autorizar"))
		return
	}
	if !s.b.VerificarSenha(sess.Admin.ID, r.FormValue("senha")) {
		s.b.Auditar(sess.Admin.Usuario, "autorização de migração RECUSADA (senha incorreta)", destino)
		redirecionar(w, r, "/admin", "", fmt.Errorf("senha incorreta: a migração NÃO foi autorizada"))
		return
	}
	_ = s.b.DefinirConfig("migrar_ativa", "1")
	s.b.Auditar(sess.Admin.Usuario, "AUTORIZAR migração de agentes", destino)
	n := s.avisarMigracaoConectados()
	redirecionar(w, r, "/admin", fmt.Sprintf("Migração autorizada. %d máquina(s) conectada(s) em tempo real foram avisadas agora; as demais migram no próximo envio. Este servidor continua no ar.", n), nil)
}

func (s *Servidor) acaoWebhook(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	u := strings.TrimSpace(r.FormValue("webhook"))
	if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		redirecionar(w, r, "/admin", "", fmt.Errorf("o webhook deve começar com http:// ou https://"))
		return
	}
	err := s.b.DefinirConfig("webhook_url", u)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "alterar webhook", "")
	}
	redirecionar(w, r, "/admin", "Webhook salvo.", err)
}

func (s *Servidor) acaoTestarWebhook(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	if s.b.Config("webhook_url") == "" {
		redirecionar(w, r, "/admin", "", fmt.Errorf("nenhum webhook configurado"))
		return
	}
	err := s.enviarWebhook("Inventário: mensagem de teste enviada por " + sess.Admin.Usuario)
	redirecionar(w, r, "/admin", "Mensagem de teste enviada.", err)
}

func (s *Servidor) baixarBackup(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	tmp, err := os.MkdirTemp("", "inventario-backup")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer os.RemoveAll(tmp)
	arq := filepath.Join(tmp, "backup.db")
	if err := s.b.Backup(arq); err != nil {
		http.Error(w, "erro no backup: "+err.Error(), 500)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "baixar backup", "")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="inventario_backup_`+time.Now().Format("2006-01-02_1504")+`.db"`)
	http.ServeFile(w, r, arq)
}

// ---------------- exportação ----------------

func gbNum(b int64) float64 { return float64(b) / (1024 * 1024 * 1024) }

func (s *Servidor) linhasMaquinas() ([]string, [][]Celula, error) {
	maqs, err := s.b.Maquinas("")
	if err != nil {
		return nil, nil, err
	}
	cab := []string{"ID", "Hostname", "Patrimônio", "Setor", "Responsável", "Localização", "Usuário atual", "Último usuário", "Nome do último usuário",
		"Último usuário visto em", "Fabricante", "Modelo", "Número de série", "Sistema operacional", "Versão", "Build", "Arquitetura",
		"Processador", "Núcleos", "Threads", "RAM (GB)", "Disco total (GB)", "Discos", "Vídeo", "IPs", "MACs", "Domínio", "BIOS",
		"Instalação do SO", "Último boot", "Primeiro contato", "Último contato", "IP de origem", "Versão do agente", "MAC duplicado", "Observações"}
	var linhas [][]Celula
	for _, m := range maqs {
		dup := "Não"
		if m.Duplicada {
			dup = "SIM"
		}
		linhas = append(linhas, []Celula{m.ID, m.Hostname, m.Patrimonio, m.Setor, m.Responsavel, m.Localizacao, m.UsuarioAtual, m.UltimoUsuario,
			m.UltimoUsuarioNome, formatarData(m.UltimoUsuarioEm), m.Fabricante, m.Modelo, m.Serial, m.SO, m.SOVersao, m.SOBuild, m.SOArq,
			m.CPU, m.CPUNucleos, m.CPUThreads, gbNum(m.RAM), gbNum(m.DiscoTotal), m.DiscosResumo, m.GPU, m.IPs, m.MACs, m.Dominio, m.BIOS,
			formatarData(m.SOInstalacao), formatarData(m.UltimoBoot), formatarData(m.PrimeiroContato), formatarData(m.UltimoContato),
			m.IPOrigem, m.VersaoAgente, dup, m.Obs})
	}
	return cab, linhas, nil
}

func (s *Servidor) linhasUsuarios() ([]string, [][]Celula, error) {
	us, err := s.b.Usuarios("")
	if err != nil {
		return nil, nil, err
	}
	cab := []string{"ID", "Login", "Nome", "E-mail", "Setor", "Qtd. máquinas", "Máquinas", "Apelidos", "Primeiro visto", "Último visto", "Observações"}
	var l [][]Celula
	for _, u := range us {
		l = append(l, []Celula{u.ID, u.Login, u.Nome, u.Email, u.Setor, u.QtdMaquinas, u.Maquinas, u.Apelidos, formatarData(u.PrimeiroVisto), formatarData(u.UltimoVisto), u.Obs})
	}
	return cab, l, nil
}

func (s *Servidor) exportarXLSX(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	cabM, linM, err := s.linhasMaquinas()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	cabU, linU, _ := s.linhasUsuarios()

	maqs, _ := s.b.Maquinas("")
	var linDiscos, linVol, linMem, linPerfis [][]Celula
	for _, m := range maqs {
		var rel modelo.Relatorio
		if json.Unmarshal([]byte(m.DadosJSON), &rel) != nil {
			continue
		}
		for _, d := range rel.Discos {
			linDiscos = append(linDiscos, []Celula{m.Hostname, d.Modelo, d.Serial, d.Tipo, d.Barramento, gbNum(int64(d.Tamanho))})
		}
		for _, v := range rel.Volumes {
			usado := 0.0
			if v.Tamanho > 0 {
				usado = float64(v.Tamanho-v.Livre) * 100 / float64(v.Tamanho)
			}
			linVol = append(linVol, []Celula{m.Hostname, v.Letra, v.Rotulo, v.Sistema, gbNum(int64(v.Tamanho)), gbNum(int64(v.Livre)), usado})
		}
		for _, x := range rel.Memorias {
			linMem = append(linMem, []Celula{m.Hostname, x.Slot, gbNum(int64(x.Capacidade)), x.Velocidade, x.Fabricante, x.PartNumber})
		}
		for _, p := range rel.Perfis {
			linPerfis = append(linPerfis, []Celula{m.Hostname, p.Usuario, p.Caminho, formatarData(p.UltimoUso)})
		}
	}
	ifs, _ := s.b.Interfaces(0)
	var linRede [][]Celula
	for _, i := range ifs {
		linRede = append(linRede, []Celula{i.Hostname, i.Nome, i.MAC, i.IPv4, simNao(i.Conectada), simNao(i.Fisica)})
	}
	logins, _ := s.b.Logins(0, 0)
	var linLog [][]Celula
	for _, l := range logins {
		linLog = append(linLog, []Celula{l.Hostname, l.Login, l.Nome, formatarData(l.Primeiro), formatarData(l.Ultimo)})
	}
	grupos, _ := s.b.DuplicadosMAC()
	var linDup [][]Celula
	for _, g := range grupos {
		for _, m := range g.Maquinas {
			linDup = append(linDup, []Celula{g.MAC, m.ID, m.Hostname, m.Serial, m.UltimoUsuario, formatarData(m.UltimoContato)})
		}
	}

	larg := make([]float64, len(cabM))
	for i := range larg {
		larg[i] = 18
	}
	larg[0], larg[1], larg[17], larg[22], larg[23] = 6, 20, 38, 45, 35

	nome := "inventario_" + time.Now().Format("2006-01-02_1504") + ".xlsx"
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+nome+`"`)
	s.b.Auditar(sess.Admin.Usuario, "exportar planilha", nome)
	err = EscreverXLSX(w, []Planilha{
		{Nome: "Máquinas", Cabecalho: cabM, Larguras: larg, Linhas: linM},
		{Nome: "Usuários", Cabecalho: cabU, Larguras: []float64{6, 28, 30, 30, 18, 12, 50, 30, 18, 18, 30}, Linhas: linU},
		{Nome: "Logins", Cabecalho: []string{"Máquina", "Login", "Nome", "Primeiro registro", "Último registro"}, Larguras: []float64{20, 28, 30, 18, 18}, Linhas: linLog},
		{Nome: "Rede", Cabecalho: []string{"Máquina", "Adaptador", "MAC", "IPv4", "Conectada", "Física"}, Larguras: []float64{20, 50, 20, 30, 10, 10}, Linhas: linRede},
		{Nome: "Discos", Cabecalho: []string{"Máquina", "Modelo", "Serial", "Tipo", "Barramento", "Tamanho (GB)"}, Larguras: []float64{20, 40, 28, 8, 12, 14}, Linhas: linDiscos},
		{Nome: "Volumes", Cabecalho: []string{"Máquina", "Unidade", "Rótulo", "Sistema", "Tamanho (GB)", "Livre (GB)", "Uso (%)"}, Larguras: []float64{20, 8, 20, 10, 14, 14, 10}, Linhas: linVol},
		{Nome: "Memória", Cabecalho: []string{"Máquina", "Slot", "Capacidade (GB)", "Velocidade (MHz)", "Fabricante", "Part number"}, Larguras: []float64{20, 16, 16, 16, 20, 24}, Linhas: linMem},
		{Nome: "Perfis", Cabecalho: []string{"Máquina", "Usuário", "Pasta", "Último uso"}, Larguras: []float64{20, 30, 40, 18}, Linhas: linPerfis},
		{Nome: "MAC duplicado", Cabecalho: []string{"MAC", "ID da máquina", "Máquina", "Serial", "Último usuário", "Último contato"}, Larguras: []float64{20, 12, 20, 24, 28, 18}, Linhas: linDup},
	})
	if err != nil {
		log.Printf("exportação xlsx: %v", err)
	}
}

func simNao(b bool) string {
	if b {
		return "Sim"
	}
	return "Não"
}

func escreverCSV(w http.ResponseWriter, nome string, cab []string, linhas [][]Celula) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+nome+`"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM para o Excel reconhecer UTF-8
	c := csv.NewWriter(w)
	c.Comma = ';' // padrão do Excel em português
	_ = c.Write(cab)
	for _, l := range linhas {
		reg := make([]string, len(l))
		for i, v := range l {
			if f, ok := v.(float64); ok {
				reg[i] = strings.Replace(strconv.FormatFloat(f, 'f', 2, 64), ".", ",", 1)
			} else {
				reg[i] = fmt.Sprint(v)
			}
		}
		_ = c.Write(reg)
	}
	c.Flush()
}

func (s *Servidor) exportarCSVMaquinas(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	cab, l, err := s.linhasMaquinas()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	escreverCSV(w, "maquinas_"+time.Now().Format("2006-01-02")+".csv", cab, l)
}

func (s *Servidor) exportarCSVUsuarios(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	cab, l, err := s.linhasUsuarios()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	escreverCSV(w, "usuarios_"+time.Now().Format("2006-01-02")+".csv", cab, l)
}

// ---------------- formatação ----------------

func formatarTamanho(b uint64) string {
	if b == 0 {
		return "-"
	}
	const gb = 1024 * 1024 * 1024
	if b >= 1024*gb {
		return fmt.Sprintf("%.2f TB", float64(b)/(1024*gb))
	}
	if b < gb {
		return fmt.Sprintf("%.0f MB", float64(b)/(1024*1024))
	}
	return strings.Replace(fmt.Sprintf("%.1f GB", float64(b)/gb), ".", ",", 1)
}

func parseData(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func formatarData(s string) string {
	t, ok := parseData(s)
	if !ok {
		return s
	}
	return t.Local().Format("02/01/2006 15:04")
}

func tempoRelativo(s string) string {
	t, ok := parseData(s)
	if !ok {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "agora"
	case d < time.Hour:
		return fmt.Sprintf("há %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("há %d h", int(d.Hours()))
	default:
		return fmt.Sprintf("há %d dias", int(d.Hours()/24))
	}
}

func classeStatus(s string) string {
	t, ok := parseData(s)
	if !ok {
		return "off"
	}
	d := time.Since(t)
	switch {
	case d < 24*time.Hour:
		return "on"
	case d < 7*24*time.Hour:
		return "warn"
	}
	return "off"
}

// ---------------- nome e e-mail das contas (1.10) ----------------

func (s *Servidor) acaoMeusDados(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	err := s.b.AtualizarDadosAdmin(sess.Admin.ID, r.FormValue("nome"), r.FormValue("email"))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "alterar meus dados", strings.TrimSpace(r.FormValue("email")))
	}
	redirecionar(w, r, "/conta", "Dados atualizados.", err)
}

func (s *Servidor) acaoDadosAdmin(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	err := s.b.AtualizarDadosAdmin(id, r.FormValue("nome"), r.FormValue("email"))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "alterar dados de usuário", fmt.Sprintf("#%d %s", id, strings.TrimSpace(r.FormValue("email"))))
	}
	redirecionar(w, r, "/admin#usuarios", "Dados do usuário salvos.", err)
}

// payloadConfere: proteção contra "laço" de auto-update. Se o agente embutido não for da
// versão anunciada (ex.: servidor compilado sem passar pelo compilar.sh), as máquinas
// baixariam o mesmo .exe antigo a cada contato. Nesse caso o auto-update não é oferecido.
var payloadOK = sync.OnceValue(func() bool {
	ok := bytes.Contains(agenteExe, []byte(VersaoAgente))
	if !ok {
		log.Printf("ATENÇÃO: o agente embutido não é a v%s; auto-update desativado (recompile com compilar.sh/compilar.ps1)", VersaoAgente)
	}
	return ok
})

func payloadConfere() bool { return payloadOK() }
