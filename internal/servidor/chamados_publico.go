package servidor

import (
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Abertura de chamado SEM login (rede interna).
//
//   GET/POST /abrir-chamado            formulário para qualquer funcionário
//   GET      /acompanhar/{token}       acompanhamento pelo link secreto do chamado
//   POST     /acompanhar/{token}/comentar
//   GET      /acompanhar/{token}/anexos/{aid}
//
// A pessoa escolhe o próprio nome (lista do inventário) ou digita; a máquina é vinculada
// sozinha pelo IP de onde o chamado foi aberto (ou pelo último login da pessoa).
// Proteções: liga/desliga em Administração, limite de envios por IP, campo isca contra robôs.
// Quando o login Microsoft/AD chegar, esta página pode exigir login sem mudar o resto.
// ----------------------------------------------------------------------------

// ---------------- limite de envios por IP ----------------

type contadorJanela struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

func (c *contadorJanela) permitir(chave string, max int, janela time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string][]time.Time{}
	}
	lim := time.Now().Add(-janela)
	var r []time.Time
	for _, t := range c.m[chave] {
		if t.After(lim) {
			r = append(r, t)
		}
	}
	if len(r) >= max {
		c.m[chave] = r
		return false
	}
	c.m[chave] = append(r, time.Now())
	return true
}

var limitePublico contadorJanela

// ---------------- pessoas, setores e máquina por IP ----------------

type Pessoa struct {
	UsuarioID          int64
	Login, Nome, Setor string
	Rotulo             string // "Nome (login)" — valor usado no formulário
}

// nomeUsuarioSQL: nome do cadastro; se vazio, o nome de exibição do Windows visto no último logon.
const nomeUsuarioSQL = `CASE WHEN u.nome<>'' THEN u.nome ELSE COALESCE((SELECT m.ultimo_usuario_nome FROM maquinas m
	WHERE m.ultimo_usuario=u.login AND m.ultimo_usuario_nome<>'' ORDER BY m.ultimo_contato DESC LIMIT 1), '') END`

func rotuloPessoa(login, nome string) string {
	if strings.TrimSpace(nome) == "" {
		return login
	}
	return nome + " (" + login + ")"
}

// PessoasConhecidas: usuários do inventário (sem contas de sistema/ignoradas) para a lista
// de sugestões do formulário.
func (b *Banco) PessoasConhecidas() []Pessoa {
	rows, err := b.db.Query(`SELECT u.id, u.login, ` + nomeUsuarioSQL + ` AS n, u.setor FROM usuarios u WHERE COALESCE(u.ignorado,0)=0
		ORDER BY CASE WHEN n<>'' THEN n ELSE u.login END COLLATE NOCASE`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []Pessoa
	for rows.Next() {
		var p Pessoa
		if rows.Scan(&p.UsuarioID, &p.Login, &p.Nome, &p.Setor) == nil && !loginSistema(p.Login) {
			p.Rotulo = rotuloPessoa(p.Login, p.Nome)
			l = append(l, p)
		}
	}
	return l
}

// Setores conhecidos (cadastro de usuários e de máquinas), para sugestão.
func (b *Banco) Setores() []string {
	if fixos := b.SetoresFixos(); len(fixos) > 0 {
		return fixos
	}
	rows, err := b.db.Query(`SELECT DISTINCT TRIM(setor) s FROM (SELECT setor FROM usuarios UNION SELECT setor FROM maquinas
		UNION SELECT solicitante_setor FROM chamados) WHERE TRIM(setor)<>'' ORDER BY s COLLATE NOCASE`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			l = append(l, s)
		}
	}
	return l
}

var reRotulo = regexp.MustCompile(`\(([^()]+)\)\s*$`)

// usuarioPorLogin encontra o usuário do inventário pelo login (ou apelido de um mesclado).
func (b *Banco) usuarioPorLogin(login string) *Pessoa {
	login = strings.TrimSpace(login)
	if login == "" {
		return nil
	}
	var p Pessoa
	err := b.db.QueryRow(`SELECT u.id, u.login, `+nomeUsuarioSQL+`, u.setor FROM usuarios u WHERE u.login=?
		UNION ALL SELECT u.id, u.login, `+nomeUsuarioSQL+`, u.setor FROM usuario_apelidos a JOIN usuarios u ON u.id=a.usuario_id WHERE a.apelido=?
		LIMIT 1`, login, login).Scan(&p.UsuarioID, &p.Login, &p.Nome, &p.Setor)
	if err != nil {
		// tenta sem o domínio (EMPRESA\joao -> joao) e vice-versa
		if i := strings.LastIndex(login, `\`); i >= 0 {
			return b.usuarioPorLogin(login[i+1:])
		}
		var id int64
		if b.db.QueryRow(`SELECT id FROM usuarios WHERE login LIKE ? ESCAPE '!' LIMIT 1`, `%\`+escaparLike(login)).Scan(&id) == nil {
			_ = b.db.QueryRow(`SELECT u.id, u.login, `+nomeUsuarioSQL+`, u.setor FROM usuarios u WHERE u.id=?`, id).Scan(&p.UsuarioID, &p.Login, &p.Nome, &p.Setor)
		} else {
			return nil
		}
	}
	p.Rotulo = rotuloPessoa(p.Login, p.Nome)
	return &p
}

func escaparLike(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}

// ReconhecerPessoa interpreta o que foi digitado no campo "seu nome": "Nome (login)" da
// lista, um login, ou um nome livre (não reconhecido — fica só o texto).
func (b *Banco) ReconhecerPessoa(texto string) Pessoa {
	texto = strings.TrimSpace(texto)
	if m := reRotulo.FindStringSubmatch(texto); m != nil {
		if p := b.usuarioPorLogin(m[1]); p != nil {
			if p.Nome == "" {
				p.Nome = strings.TrimSpace(strings.TrimSuffix(texto, m[0]))
				if p.Nome == "" {
					p.Nome = p.Login
				}
			}
			return *p
		}
	}
	if !strings.Contains(texto, " ") {
		if p := b.usuarioPorLogin(texto); p != nil {
			if p.Nome == "" {
				p.Nome = p.Login
			}
			return *p
		}
	}
	// nome igual ao cadastrado (sem ambiguidade)
	var p Pessoa
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM usuarios WHERE nome=? COLLATE NOCASE`, texto).Scan(&n)
	if n == 1 && b.db.QueryRow(`SELECT id, login, nome, setor FROM usuarios WHERE nome=? COLLATE NOCASE`, texto).
		Scan(&p.UsuarioID, &p.Login, &p.Nome, &p.Setor) == nil {
		p.Rotulo = rotuloPessoa(p.Login, p.Nome)
		return p
	}
	return Pessoa{Nome: texto, Rotulo: texto}
}

// UltimaMaquinaDoUsuario: máquina em que a pessoa entrou por último.
func (b *Banco) UltimaMaquinaDoUsuario(uid int64) int64 {
	var id int64
	_ = b.db.QueryRow(`SELECT maquina_id FROM logins WHERE usuario_id=? ORDER BY ultimo DESC LIMIT 1`, uid).Scan(&id)
	return id
}

type maquinaIP struct {
	ID                                           int64
	Hostname, UsuarioAtual, UltimoUsuario, Setor string
}

// MaquinaPorIP: máquina do inventário com esse IP (o que o servidor viu no último contato,
// ou o IPv4 de uma placa de rede). Ignora o próprio servidor (localhost).
func (b *Banco) MaquinaPorIP(ip string) *maquinaIP {
	if p := net.ParseIP(ip); p == nil || p.IsLoopback() || p.IsUnspecified() {
		return nil
	}
	var m maquinaIP
	err := b.db.QueryRow(`SELECT id, hostname, usuario_atual, ultimo_usuario, setor FROM maquinas WHERE ip_origem=? ORDER BY ultimo_contato DESC LIMIT 1`, ip).
		Scan(&m.ID, &m.Hostname, &m.UsuarioAtual, &m.UltimoUsuario, &m.Setor)
	if err != nil {
		err = b.db.QueryRow(`SELECT m.id, m.hostname, m.usuario_atual, m.ultimo_usuario, m.setor FROM interfaces i JOIN maquinas m ON m.id=i.maquina_id
			WHERE i.ipv4=? ORDER BY m.ultimo_contato DESC LIMIT 1`, ip).
			Scan(&m.ID, &m.Hostname, &m.UsuarioAtual, &m.UltimoUsuario, &m.Setor)
	}
	if err != nil {
		return nil
	}
	return &m
}

// ---------------- páginas públicas ----------------

func (s *Servidor) publicoAtivo() bool { return s.b.Config("chamados_publico") != "0" }

func (s *Servidor) paginaAbrirChamado(w http.ResponseWriter, r *http.Request) {
	if !s.publicoAtivo() {
		s.render(w, r, "abrir_chamado", "Abrir chamado", "", nil, map[string]any{"Desativado": true})
		return
	}
	d := map[string]any{
		"Categorias": CategoriasChamado, "Prioridades": PrioridadesPublicas,
		"Pessoas": s.b.PessoasConhecidas(), "Setores": s.b.Setores(),
		"MaxAnexoMB": maxAnexo >> 20, "MaxAnexos": maxAnexosPorEnvio,
	}
	// sugere quem é e de qual máquina está abrindo, pelo IP
	if m := s.b.MaquinaPorIP(ipCliente(r)); m != nil {
		d["Maquina"] = m.Hostname
		login := m.UsuarioAtual
		if login == "" {
			login = m.UltimoUsuario
		}
		if p := s.b.usuarioPorLogin(login); p != nil && !loginSistema(p.Login) {
			d["Nome"] = p.Rotulo
			d["Setor"] = p.Setor
		}
		if d["Setor"] == nil || d["Setor"] == "" {
			d["Setor"] = m.Setor
		}
	}
	s.render(w, r, "abrir_chamado", "Abrir chamado", "", nil, d)
}

func (s *Servidor) acaoAbrirChamado(w http.ResponseWriter, r *http.Request) {
	if !s.publicoAtivo() {
		http.Redirect(w, r, "/abrir-chamado", http.StatusSeeOther)
		return
	}
	ip := ipCliente(r)
	// campo isca: invisível para pessoas, robôs costumam preencher
	if strings.TrimSpace(r.FormValue("site")) != "" {
		http.Redirect(w, r, "/abrir-chamado", http.StatusSeeOther)
		return
	}
	if !limitePublico.permitir("abrir:"+ip, 10, time.Hour) {
		redirecionar(w, r, "/abrir-chamado", "", fmt.Errorf("muitos chamados enviados deste computador na última hora; fale com a TI"))
		return
	}
	nome := strings.TrimSpace(r.FormValue("nome"))
	if nome == "" {
		redirecionar(w, r, "/abrir-chamado", "", fmt.Errorf("informe o seu nome"))
		return
	}
	arquivos, err := lerAnexos(r)
	if err != nil {
		redirecionar(w, r, "/abrir-chamado", "", err)
		return
	}
	p := s.b.ReconhecerPessoa(nome)
	setor := strings.TrimSpace(r.FormValue("setor"))
	if !s.b.setorValido(setor) {
		setor = "" // com lista de setores definida, só aceita os da lista
	}
	if setor == "" {
		setor = p.Setor
	}
	n := NovoChamado{
		Titulo: r.FormValue("titulo"), Descricao: r.FormValue("descricao"), Categoria: r.FormValue("categoria"),
		Prioridade: restringirPrioridade(r.FormValue("prioridade")), Origem: "publico", IP: ip,
		SolicitanteNome: p.Nome, SolicitanteEmail: strings.TrimSpace(r.FormValue("email")), SolicitanteSetor: setor,
		SolicitanteUID: p.UsuarioID,
	}
	if m := s.b.MaquinaPorIP(ip); m != nil {
		n.MaquinaID = m.ID
	} else if p.UsuarioID > 0 {
		n.MaquinaID = s.b.UltimaMaquinaDoUsuario(p.UsuarioID)
	}
	n.AtribuidoA = s.responsavelAutomatico()
	id, token, err := s.b.CriarChamado(n, p.Nome)
	if err != nil {
		redirecionar(w, r, "/abrir-chamado", "", err)
		return
	}
	s.salvarAnexos(id, 0, arquivos, p.Nome)
	s.avisarNovoChamadoPorEmail(r, id, 0)
	s.b.Auditar("(página pública)", "abrir chamado", fmt.Sprintf("#%d por %s (%s)", id, p.Nome, ip))
	s.avisarChamado(fmt.Sprintf("🆕 Chamado #%d (%s): %s — %s%s · %s", id, rotuloPrioridade(n.Prioridade), limitar(n.Titulo, 200),
		p.Nome, prefixo(" · ", setor), s.baseURL(r)+"/chamados/"+strconv.FormatInt(id, 10)))
	redirecionar(w, r, "/acompanhar/"+token, fmt.Sprintf("Chamado #%d aberto! Guarde o link desta página para acompanhar.", id), nil)
}

func prefixo(p, s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return p + s
}

func (s *Servidor) chamadoDoToken(w http.ResponseWriter, r *http.Request) *Chamado {
	c, err := s.b.ChamadoPorToken(r.PathValue("token"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		s.render(w, r, "acompanhar", "Chamado não encontrado", "", nil, map[string]any{"NaoEncontrado": true})
		return nil
	}
	return c
}

func (s *Servidor) paginaAcompanhar(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer") // o link é o "senha" do chamado
	c := s.chamadoDoToken(w, r)
	if c == nil {
		return
	}
	anexos, _ := s.b.AnexosChamado(c.ID)
	var abertura []Anexo
	for _, a := range anexos {
		if a.ComentarioID == 0 {
			abertura = append(abertura, a)
		}
	}
	// o solicitante vê comentários públicos e as mudanças de status/responsável
	var linha []ItemLinha
	for _, it := range s.b.LinhaDoTempo(c.ID, false, true) {
		if it.Evento != nil && it.Evento.Tipo != "status" && it.Evento.Tipo != "responsavel" && it.Evento.Tipo != "criado" {
			continue
		}
		linha = append(linha, it)
	}
	s.render(w, r, "acompanhar", fmt.Sprintf("Chamado #%d", c.ID), "", nil, map[string]any{
		"C": c, "Linha": linha, "AnexosAbertura": abertura, "Token": c.TokenPublico,
		"Link": s.baseURL(r) + "/acompanhar/" + c.TokenPublico, "MaxAnexoMB": maxAnexo >> 20, "MaxAnexos": maxAnexosPorEnvio,
	})
}

func (s *Servidor) acaoComentarPublico(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	c := s.chamadoDoToken(w, r)
	if c == nil {
		return
	}
	destino := "/acompanhar/" + c.TokenPublico
	if strings.TrimSpace(r.FormValue("site")) != "" {
		http.Redirect(w, r, destino, http.StatusSeeOther)
		return
	}
	if c.Status == "fechado" {
		redirecionar(w, r, destino, "", fmt.Errorf("este chamado está fechado; abra um novo se o problema voltou"))
		return
	}
	if !limitePublico.permitir("comentar:"+ipCliente(r), 30, time.Hour) {
		redirecionar(w, r, destino, "", fmt.Errorf("muitas mensagens em pouco tempo; aguarde um pouco"))
		return
	}
	arquivos, err := lerAnexos(r)
	if err != nil {
		redirecionar(w, r, destino, "", err)
		return
	}
	autor := c.Solicitante()
	cid, err := s.b.AdicionarComentario(c.ID, 0, autor, r.FormValue("texto"), false, len(arquivos) > 0)
	if err != nil {
		redirecionar(w, r, destino, "", err)
		return
	}
	s.salvarAnexos(c.ID, cid, arquivos, autor)
	reaberto := s.b.ReabrirPeloSolicitante(c.ID, autor)
	aviso := fmt.Sprintf("💬 %s respondeu o chamado #%d: %s · %s", autor, c.ID, c.Titulo, s.baseURL(r)+"/chamados/"+strconv.FormatInt(c.ID, 10))
	if reaberto {
		aviso = "↩ " + aviso + " (reaberto)"
	}
	s.avisarChamado(aviso)
	redirecionar(w, r, destino+"#fim", "Mensagem enviada para a TI.", nil)
}

func (s *Servidor) baixarAnexoPublico(w http.ResponseWriter, r *http.Request) {
	c, err := s.b.ChamadoPorToken(r.PathValue("token"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a, dados, err := s.b.Anexo(c.ID, parseID(r.PathValue("aid")))
	if err != nil || a.Interno {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	servirAnexo(w, r, a, dados)
}
