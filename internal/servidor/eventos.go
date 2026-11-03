package servidor

// Eventos em tempo real (1.12): cada agente 1.12+ roda um serviço do Windows que mantém uma
// conexão aberta (Server-Sent Events) em /api/v1/eventos. Assim que o servidor sobe numa versão
// nova (ou o admin pede), todos os agentes conectados recebem "versao" e se atualizam na hora.
// O teste de conexão manda "teste" para todos e cada agente responde se aceitaria a atualização.

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"inventario/internal/modelo"
)

type eventoSSE struct {
	nome  string
	dados []byte
}

type clienteEventos struct {
	id       int64
	AgentID  string
	Hostname string
	Versao   string
	IP       string
	Desde    time.Time
	ch       chan eventoSSE
}

type RespostaTeste struct {
	modelo.RespostaTeste
	IP string
	Em time.Time
}

type TesteConexao struct {
	ID        string
	Em        time.Time
	Versao    string
	Enviados  []clienteEventos
	Respostas map[string]RespostaTeste // chave: hostname em maiúsculas
}

type hubEventos struct {
	mu       sync.Mutex
	prox     int64
	clientes map[int64]*clienteEventos
	testes   map[string]*TesteConexao
	ultimo   string
}

func novoHub() *hubEventos {
	return &hubEventos{clientes: map[int64]*clienteEventos{}, testes: map[string]*TesteConexao{}}
}

func (h *hubEventos) entrar(c *clienteEventos) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prox++
	c.id = h.prox
	// a mesma máquina reconectando: derruba a conexão anterior
	for id, o := range h.clientes {
		if (c.AgentID != "" && o.AgentID == c.AgentID) || (c.AgentID == "" && strings.EqualFold(o.Hostname, c.Hostname)) {
			close(o.ch)
			delete(h.clientes, id)
		}
	}
	h.clientes[c.id] = c
}

func (h *hubEventos) sair(c *clienteEventos) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if atual, ok := h.clientes[c.id]; ok && atual == c {
		close(c.ch)
		delete(h.clientes, c.id)
	}
}

// enviar manda um evento para todos os conectados; devolve quantos receberam.
func (h *hubEventos) enviar(nome string, v any) int {
	dados, _ := json.Marshal(v)
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.clientes {
		select {
		case c.ch <- eventoSSE{nome, dados}:
			n++
		default: // fila cheia: cliente travado, ignora
		}
	}
	return n
}

func (h *hubEventos) Conectados() []clienteEventos {
	h.mu.Lock()
	defer h.mu.Unlock()
	var r []clienteEventos
	for _, c := range h.clientes {
		r = append(r, *c)
	}
	sort.Slice(r, func(i, j int) bool { return strings.ToLower(r[i].Hostname) < strings.ToLower(r[j].Hostname) })
	return r
}

// eventoVersao: o que o servidor distribui e se o agente deve se atualizar.
func (s *Servidor) eventoVersao() modelo.EventoVersao {
	return modelo.EventoVersao{Versao: s.VersaoAgente, Atualizar: s.b.Config("auto_update") != "0" && payloadConfere()}
}

// apiEventos mantém a conexão SSE com um agente.
func (s *Servidor) apiEventos(w http.ResponseWriter, r *http.Request) {
	if ok, _ := s.b.validarTokenAgente(r.Header.Get("X-Agente-Token")); !ok {
		http.Error(w, "token inválido", http.StatusUnauthorized)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "sem suporte a streaming", http.StatusInternalServerError)
		return
	}
	// conexão longa: tira os limites de tempo de leitura/escrita do servidor
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})

	c := &clienteEventos{
		AgentID: cortar(r.Header.Get("X-Agente-ID"), 64), Hostname: cortar(r.Header.Get("X-Agente-Host"), 100),
		Versao: cortar(r.Header.Get("X-Agente-Versao"), 20), IP: ipCliente(r), Desde: time.Now(), ch: make(chan eventoSSE, 16),
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s.eventos.entrar(c)
	defer s.eventos.sair(c)

	escrever := func(nome string, dados []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
		var err error
		if nome == "" {
			_, err = io.WriteString(w, ": ping\n\n")
		} else {
			_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", nome, dados)
		}
		if err == nil {
			fl.Flush()
		}
		return err == nil
	}
	v, _ := json.Marshal(s.eventoVersao())
	if !escrever("versao", v) {
		return
	}
	if mg := s.eventoMigrar(c.Hostname); mg != nil {
		m, _ := json.Marshal(mg)
		if !escrever("migrar", m) {
			return
		}
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, aberto := <-c.ch:
			if !aberto || !escrever(ev.nome, ev.dados) {
				return
			}
		case <-ping.C:
			if !escrever("", nil) {
				return
			}
		}
	}
}

func cortar(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// apiRespostaTeste recebe a resposta de um agente ao teste de conexão.
func (s *Servidor) apiRespostaTeste(w http.ResponseWriter, r *http.Request) {
	if ok, _ := s.b.validarTokenAgente(r.Header.Get("X-Agente-Token")); !ok {
		http.Error(w, "token inválido", http.StatusUnauthorized)
		return
	}
	var resp modelo.RespostaTeste
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&resp); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}
	resp.Hostname = cortar(resp.Hostname, 100)
	resp.Motivo = cortar(resp.Motivo, 500)
	s.eventos.mu.Lock()
	t := s.eventos.testes[resp.TesteID]
	if t != nil {
		t.Respostas[strings.ToUpper(resp.Hostname)] = RespostaTeste{RespostaTeste: resp, IP: ipCliente(r), Em: time.Now()}
	}
	s.eventos.mu.Unlock()
	if t == nil {
		http.Error(w, "teste desconhecido", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- painel: Administração → Conexões ----------

type linhaTeste struct {
	Hostname  string
	Versao    string
	IP        string
	Respondeu bool
	R         RespostaTeste
	MaquinaID int64
}

type maquinaDesconectada struct {
	ID            int64
	Hostname      string
	Versao        string
	UltimoContato string
	Motivo        string
}

func (s *Servidor) paginaConexoes(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	conectados := s.eventos.Conectados()
	ids := s.b.idsPorHostname()
	online := map[string]bool{}
	for _, c := range conectados {
		online[strings.ToUpper(c.Hostname)] = true
	}
	// teste pedido (ou o último)
	idTeste := r.URL.Query().Get("teste")
	s.eventos.mu.Lock()
	if idTeste == "" {
		idTeste = s.eventos.ultimo
	}
	var teste *TesteConexao
	var linhas []linhaTeste
	var respondeu, aceitaria, jaAtual int
	if t := s.eventos.testes[idTeste]; t != nil {
		cp := *t
		teste = &cp
		for _, e := range t.Enviados {
			l := linhaTeste{Hostname: e.Hostname, Versao: e.Versao, IP: e.IP, MaquinaID: ids[strings.ToUpper(e.Hostname)]}
			if rr, ok := t.Respostas[strings.ToUpper(e.Hostname)]; ok {
				l.Respondeu, l.R = true, rr
				respondeu++
				if rr.JaAtual {
					jaAtual++
				} else if rr.PodeAtualizar {
					aceitaria++
				}
			}
			linhas = append(linhas, l)
		}
	}
	s.eventos.mu.Unlock()
	sort.SliceStable(linhas, func(i, j int) bool { return linhas[i].Respondeu && !linhas[j].Respondeu })

	// máquinas com contato nos últimos 7 dias que não estão conectadas
	var fora []maquinaDesconectada
	limite := time.Now().AddDate(0, 0, -7).UTC().Format(time.RFC3339)
	if rows, err := s.b.db.Query(`SELECT id, hostname, versao_agente, ultimo_contato FROM maquinas WHERE ultimo_contato>=? ORDER BY hostname`, limite); err == nil {
		for rows.Next() {
			var m maquinaDesconectada
			if rows.Scan(&m.ID, &m.Hostname, &m.Versao, &m.UltimoContato) != nil || online[strings.ToUpper(m.Hostname)] {
				continue
			}
			if versaoMenor(m.Versao, "1.12.0") {
				m.Motivo = "agente v" + m.Versao + " (sem o serviço). Ganha o serviço sozinho quando atualizar pelo envio de hora em hora."
			} else {
				m.Motivo = "agente 1.12+, mas o serviço \"InventarioAgente\" não está conectado (máquina desligada, serviço parado ou sem rede)."
			}
			fora = append(fora, m)
		}
		rows.Close()
	}
	aguardando := teste != nil && time.Since(teste.Em) < 20*time.Second && respondeu < len(linhas)
	s.render(w, r, "conexoes", "Conexões com as máquinas", "admin_conexoes", sess, map[string]any{
		"Conectados": conectados, "Teste": teste, "Linhas": linhas, "Respondeu": respondeu, "Aceitaria": aceitaria, "JaAtual": jaAtual,
		"SemResposta": len(linhas) - respondeu, "Fora": fora, "Aguardando": aguardando, "Evento": s.eventoVersao(), "IDs": ids,
	})
}

func (b *Banco) idsPorHostname() map[string]int64 {
	r := map[string]int64{}
	rows, err := b.db.Query(`SELECT id, hostname FROM maquinas ORDER BY ultimo_contato`)
	if err != nil {
		return r
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var h string
		if rows.Scan(&id, &h) == nil {
			r[strings.ToUpper(h)] = id
		}
	}
	return r
}

func (s *Servidor) acaoTestarConexoes(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t := &TesteConexao{ID: aleatorio(6), Em: time.Now(), Versao: s.VersaoAgente, Respostas: map[string]RespostaTeste{}}
	t.Enviados = s.eventos.Conectados()
	s.eventos.mu.Lock()
	// guarda só os 10 testes mais recentes
	if len(s.eventos.testes) >= 10 {
		var maisAntigo string
		for id, x := range s.eventos.testes {
			if maisAntigo == "" || x.Em.Before(s.eventos.testes[maisAntigo].Em) {
				maisAntigo = id
			}
		}
		delete(s.eventos.testes, maisAntigo)
	}
	s.eventos.testes[t.ID] = t
	s.eventos.ultimo = t.ID
	s.eventos.mu.Unlock()
	n := s.eventos.enviar("teste", modelo.EventoTeste{ID: t.ID, Versao: s.VersaoAgente, Atualizar: s.eventoVersao().Atualizar})
	s.b.Auditar(sess.Admin.Usuario, "testar conexão com as máquinas", fmt.Sprintf("%d máquina(s)", n))
	log.Printf("teste de conexão %s enviado para %d agente(s)", t.ID, n)
	redirecionar(w, r, "/admin/conexoes?teste="+t.ID, "", nil)
}

func (s *Servidor) acaoAvisarVersao(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	ev := s.eventoVersao()
	if !ev.Atualizar {
		redirecionar(w, r, "/admin/conexoes", "", fmt.Errorf("o auto-update está desligado (ou o servidor não tem o agente embutido) — ligue em Administração"))
		return
	}
	n := s.eventos.enviar("versao", ev)
	s.b.Auditar(sess.Admin.Usuario, "avisar agentes da versão "+ev.Versao, fmt.Sprintf("%d máquina(s)", n))
	redirecionar(w, r, "/admin/conexoes", fmt.Sprintf("Aviso da v%s enviado para %d máquina(s) conectada(s). As desatualizadas trocam de versão em até 2 minutos.", ev.Versao, n), nil)
}

// enviarPara manda um evento para um cliente específico; devolve 1 se entregou.
func (h *hubEventos) enviarPara(id int64, nome string, v any) int {
	dados, _ := json.Marshal(v)
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.clientes[id]; ok {
		select {
		case c.ch <- eventoSSE{nome, dados}:
			return 1
		default:
		}
	}
	return 0
}
