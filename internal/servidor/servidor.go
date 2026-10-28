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
	"time"

	"inventario/internal/modelo"
)

//go:embed web
var arquivosWeb embed.FS

type Servidor struct {
	b       *Banco
	paginas map[string]*template.Template
	lim     *limitador
	Versao  string
}

func Novo(b *Banco, versao string) (*Servidor, error) {
	s := &Servidor{b: b, paginas: map[string]*template.Template{}, lim: &limitador{falha: map[string][]time.Time{}}, Versao: versao}
	funcs := template.FuncMap{
		"tam":    formatarTamanho,
		"gb":     func(v int64) string { return formatarTamanho(uint64(v)) },
		"data":   formatarData,
		"rel":    tempoRelativo,
		"status": classeStatus,
		"join":   strings.Join,
		"pct": func(livre, total uint64) int {
			if total == 0 {
				return 0
			}
			return int(100 - livre*100/total)
		},
		"add": func(a, b int) int { return a + b },
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
	mux.HandleFunc("GET /saude", s.saude)
	mux.HandleFunc("GET /login", s.paginaLogin)
	mux.HandleFunc("POST /login", s.acaoLogin)

	p := func(padrao string, h func(http.ResponseWriter, *http.Request, *Sessao)) {
		mux.HandleFunc(padrao, s.protegido(h))
	}
	p("POST /sair", s.acaoSair)
	p("GET /{$}", s.paginaPainel)
	p("GET /maquinas", s.paginaMaquinas)
	p("GET /maquinas/{id}", s.paginaMaquina)
	p("POST /maquinas/{id}/editar", s.acaoEditarMaquina)
	p("POST /maquinas/{id}/excluir", s.acaoExcluirMaquina)
	p("POST /maquinas/{id}/mesclar", s.acaoMesclarMaquina)
	p("GET /usuarios", s.paginaUsuarios)
	p("GET /usuarios/{id}", s.paginaUsuario)
	p("POST /usuarios/{id}/editar", s.acaoEditarUsuario)
	p("POST /usuarios/{id}/excluir", s.acaoExcluirUsuario)
	p("POST /usuarios/{id}/mesclar", s.acaoMesclarUsuario)
	p("POST /usuarios/{id}/apelido/remover", s.acaoRemoverApelido)
	p("POST /filtros/criar", s.acaoCriarFiltro)
	p("POST /filtros/{id}/excluir", s.acaoExcluirFiltro)
	p("GET /notificacoes", s.paginaNotificacoes)
	p("POST /notificacoes", s.acaoNotificacoes)
	p("GET /duplicados", s.paginaDuplicados)
	p("POST /duplicados/resolver", s.acaoResolverMaquinas)
	p("POST /usuarios/resolver", s.acaoResolverUsuarios)
	p("POST /macs/ignorar", s.acaoIgnorarMAC)
	p("POST /macs/remover", s.acaoRemoverIgnorado)
	p("GET /exportar/inventario.xlsx", s.exportarXLSX)
	p("GET /exportar/maquinas.csv", s.exportarCSVMaquinas)
	p("GET /exportar/usuarios.csv", s.exportarCSVUsuarios)
	p("GET /admin", s.paginaAdmin)
	p("POST /admin/senha", s.acaoAlterarSenha)
	p("POST /admin/novo", s.acaoNovoAdmin)
	p("POST /admin/{id}/excluir", s.acaoExcluirAdmin)
	p("POST /admin/token", s.acaoNovoToken)
	p("GET /admin/backup", s.baixarBackup)
	p("POST /admin/webhook", s.acaoWebhook)
	p("POST /admin/webhook/testar", s.acaoTestarWebhook)
	p("GET /admin/agente/exe", s.baixarAgenteExe)
	p("GET /admin/agente/config", s.baixarAgenteConfig)
	p("GET /admin/agente/zip", s.baixarAgenteZip)
	p("POST /admin/atualizar", s.acaoAtualizar)
	p("POST /admin/migrar", s.acaoMigrar)
	p("POST /admin/migrar/autorizar", s.acaoAutorizarMigracao)
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
		if strings.HasPrefix(r.URL.Path, "/static/") {
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

type dadosPagina struct {
	Titulo string
	Menu   string
	Sessao *Sessao
	Msg    string
	Erro   string
	Resumo Resumo
	Versao string
	D      map[string]any
}

func (s *Servidor) render(w http.ResponseWriter, r *http.Request, pagina, titulo, menu string, sess *Sessao, d map[string]any) {
	t, ok := s.paginas[pagina]
	if !ok {
		http.Error(w, "página não encontrada", http.StatusInternalServerError)
		return
	}
	dp := dadosPagina{Titulo: titulo, Menu: menu, Sessao: sess, Msg: r.URL.Query().Get("msg"), Erro: r.URL.Query().Get("erro"), Versao: s.Versao, D: d}
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
		if strings.Contains(destino, "?") {
			destino += "&" + v.Encode()
		} else {
			destino += "?" + v.Encode()
		}
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
	esperado := s.b.Config("token_agente")
	if esperado == "" || token != esperado {
		http.Error(w, "token inválido", http.StatusUnauthorized)
		return
	}
	var rel modelo.Relatorio
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&rel); err != nil {
		http.Error(w, "JSON inválido: "+err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.b.Ingerir(&rel, ipCliente(r))
	if err != nil {
		log.Printf("erro ao gravar relatório de %s: %v", rel.Hostname, err)
		http.Error(w, "erro ao gravar: "+err.Error(), http.StatusBadRequest)
		return
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
	resp := modelo.Resposta{OK: true, MaquinaID: res.MaquinaID, Nova: res.Nova, MACDuplicado: dup}
	// A migração só é anunciada aos agentes depois de ser AUTORIZADA manualmente por um
	// administrador (migrar_ativa=1). Enquanto não for autorizada, o destino fica só salvo.
	// Se houver uma fase canário (lista de hostnames), só essas máquinas recebem a ordem.
	if destino := strings.TrimRight(s.b.Config("migrar_servidor"), "/"); destino != "" && s.b.Config("migrar_ativa") == "1" {
		if !strings.EqualFold(destino, strings.TrimRight(rel.ServidorAtual, "/")) && noCanario(s.b.Config("migrar_canario"), rel.Hostname) {
			resp.Migrar = &modelo.Migracao{Servidor: destino, Token: s.b.Config("migrar_token")}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
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
	nAlertas := 0
	if al, err := s.b.Alertas(); err == nil {
		nAlertas = len(al)
	}
	s.render(w, r, "painel", "Painel", "painel", sess, map[string]any{
		"Recentes": recentes, "Duplicados": dup, "P": p,
		"Rosca": rosca, "GrafSO": grafSO, "GrafSetor": grafSetor, "NAlertas": nAlertas,
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
	default:
		if id, _ := strconv.ParseInt(filtro, 10, 64); id > 0 {
			if f, err := s.b.Filtro(id); err == nil {
				nomeFiltro = f.Nome
				maqs = AplicarFiltro(maqs, f)
			}
		}
	}
	filtros, _ := s.b.Filtros()
	s.render(w, r, "maquinas", "Máquinas", "maquinas", sess, map[string]any{
		"Maquinas": maqs, "Q": q, "Filtro": filtro, "NomeFiltro": nomeFiltro, "Filtros": filtros,
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
	porTipo := map[string][]Alerta{}
	for _, a := range alertas {
		porTipo[a.Tipo] = append(porTipo[a.Tipo], a)
	}
	s.render(w, r, "notificacoes", "Notificações", "notificacoes", sess, map[string]any{
		"Genericos": porTipo["generico"], "Inativas": porTipo["inativa"], "Discos": porTipo["disco"],
		"LigGenerico": s.b.Config("alerta_generico") == "1",
		"PadroesGen":  s.b.Config("alerta_generico_padroes"),
		"DiasInativa": s.b.Config("alerta_inativa_dias"),
		"PctDisco":    s.b.Config("alerta_disco_pct"),
	})
}

func (s *Servidor) acaoNotificacoes(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	if r.FormValue("generico") != "" {
		_ = s.b.DefinirConfig("alerta_generico", "1")
	} else {
		_ = s.b.DefinirConfig("alerta_generico", "")
	}
	_ = s.b.DefinirConfig("alerta_generico_padroes", strings.TrimSpace(r.FormValue("padroes")))
	_ = s.b.DefinirConfig("alerta_inativa_dias", soNumero(r.FormValue("dias_inativa")))
	_ = s.b.DefinirConfig("alerta_disco_pct", soNumero(r.FormValue("disco_pct")))
	s.b.Auditar(sess.Admin.Usuario, "configurar notificações", "")
	redirecionar(w, r, "/notificacoes", "Configurações salvas.", nil)
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
	s.render(w, r, "maquina", m.Hostname, "maquinas", sess, map[string]any{"M": m, "R": rel, "Interfaces": ifs, "Logins": logins, "Outras": outras, "DupMACs": dupMACs})
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
	err := s.b.ExcluirMaquina(id)
	if err == nil && m != nil {
		s.b.Auditar(sess.Admin.Usuario, "excluir máquina", fmt.Sprintf("#%d %s", id, m.Hostname))
	}
	voltar := r.FormValue("voltar")
	if voltar == "" || !strings.HasPrefix(voltar, "/") || strings.HasPrefix(voltar, "//") {
		voltar = "/maquinas"
	}
	redirecionar(w, r, voltar, "Máquina excluída.", err)
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
	s.render(w, r, "usuarios", "Usuários", "usuarios", sess, map[string]any{"Usuarios": us, "Q": q, "Duplicados": dup})
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
	esquema := "http"
	if r.TLS != nil {
		esquema = "https"
	}
	s.render(w, r, "admin", "Administração", "admin", sess, map[string]any{
		"Admins": admins, "Auditoria": aud, "Token": s.b.Config("token_agente"), "Webhook": s.b.Config("webhook_url"),
		"URL": esquema + "://" + r.Host, "Versao": s.Versao,
		"Migracao": s.b.Migracao(), "MigrarToken": s.b.Config("migrar_token"), "Canario": s.b.Config("migrar_canario"),
	})
}

func (s *Servidor) acaoAlterarSenha(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	if r.FormValue("nova") != r.FormValue("confirma") {
		redirecionar(w, r, "/admin", "", fmt.Errorf("a confirmação não confere com a nova senha"))
		return
	}
	err := s.b.AlterarSenha(sess.Admin.ID, r.FormValue("atual"), r.FormValue("nova"))
	if err != nil {
		redirecionar(w, r, "/admin", "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "alterar senha", "")
	redirecionar(w, r, "/login", "Senha alterada. Entre novamente.", nil)
}

func (s *Servidor) acaoNovoAdmin(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	err := s.b.CriarAdmin(r.FormValue("usuario"), r.FormValue("senha"))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "criar administrador", r.FormValue("usuario"))
	}
	redirecionar(w, r, "/admin", "Administrador criado.", err)
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
	redirecionar(w, r, "/admin", "Migração autorizada. A partir de agora, cada máquina migra ao se comunicar.", nil)
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
