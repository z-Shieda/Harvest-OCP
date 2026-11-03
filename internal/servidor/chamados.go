package servidor

import (
	"database/sql"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ----------------------------------------------------------------------------
// Sistema de chamados (1.8 — Beta).
//
// Perfis: administrador e técnico gerenciam todos os chamados; usuário comum (conta
// local) abre e vê os próprios. Funcionários sem conta abrem pela página pública
// /abrir-chamado (ver chamados_publico.go) e acompanham por um link secreto.
//
// Cada chamado guarda: solicitante (nome/e-mail/setor e, se reconhecido, o usuário do
// inventário), máquina vinculada, prazo (SLA) calculado pela prioridade, histórico de
// eventos (chamado_eventos), comentários (públicos ou notas internas) e anexos.
// ----------------------------------------------------------------------------

var CategoriasChamado = []string{"Hardware", "Software", "Rede", "Acesso/Senha", "Impressora", "E-mail", "Outro"}
var PrioridadesChamado = []string{"baixa", "normal", "alta", "urgente"}

// PrioridadesPublicas: o que o funcionário pode escolher na página sem login.
// "Urgente" fica a critério da TI.
var PrioridadesPublicas = []string{"baixa", "normal", "alta"}
var StatusChamado = []string{"aberto", "em_andamento", "resolvido", "fechado"}

// Prazos padrão (horas corridas) por prioridade. Ajustáveis em Administração.
var slaPadrao = map[string]int{"urgente": 4, "alta": 8, "normal": 24, "baixa": 72}

func rotuloStatus(s string) string {
	switch s {
	case "aberto":
		return "Aberto"
	case "em_andamento":
		return "Em andamento"
	case "resolvido":
		return "Resolvido"
	case "fechado":
		return "Fechado"
	}
	return s
}

func rotuloPrioridade(p string) string {
	switch p {
	case "baixa":
		return "Baixa"
	case "normal":
		return "Normal"
	case "alta":
		return "Alta"
	case "urgente":
		return "Urgente"
	}
	return p
}

func statusAberto(s string) bool { return s == "aberto" || s == "em_andamento" }

// ---------------- Modelo ----------------

type Chamado struct {
	ID                                                  int64
	Titulo, Descricao, Categoria, Prioridade, Status    string
	CriadoPorNome, AtribuidoNome, MaquinaHostname       string
	SolicitanteNome, SolicitanteEmail, SolicitanteSetor string
	Origem, TokenPublico, IPOrigem                      string
	CriadoPorID, AtribuidoID, MaquinaID, SolicitanteUID int64
	CriadoEm, AtualizadoEm, FechadoEm, ResolvidoEm      string
	Prazo                                               string
	QtdComentarios, QtdAnexos                           int
	AnyDeskID, AnyDeskMaquina                           string // gravado no chamado / atual da máquina
}

// AnyDesk: o ID gravado no chamado ou, se não houver, o atual da máquina.
func (c Chamado) AnyDesk() string {
	if c.AnyDeskID != "" {
		return c.AnyDeskID
	}
	return c.AnyDeskMaquina
}

func (c Chamado) RotuloStatus() string     { return rotuloStatus(c.Status) }
func (c Chamado) RotuloPrioridade() string { return rotuloPrioridade(c.Prioridade) }
func (c Chamado) Aberto() bool             { return statusAberto(c.Status) }
func (c Chamado) Publico() bool            { return c.Origem == "publico" }

// Solicitante: quem pediu. Chamados da página pública (ou registrados pela TI em nome
// de alguém) guardam o nome; os demais usam a conta que abriu.
func (c Chamado) Solicitante() string {
	if strings.TrimSpace(c.SolicitanteNome) != "" {
		return c.SolicitanteNome
	}
	if c.CriadoPorNome != "" {
		return c.CriadoPorNome
	}
	return "—"
}

// SituacaoPrazo: "" (sem prazo), "ok", "vence" (falta pouco), "atrasado",
// "cumprido" (resolvido dentro do prazo) ou "estourado" (resolvido fora do prazo).
func (c Chamado) SituacaoPrazo() string {
	prazo, ok := parseData(c.Prazo)
	if !ok {
		return ""
	}
	if !c.Aberto() {
		fim, ok := parseData(c.ResolvidoEm)
		if !ok {
			fim, ok = parseData(c.FechadoEm)
		}
		if !ok {
			return ""
		}
		if fim.After(prazo) {
			return "estourado"
		}
		return "cumprido"
	}
	agoraT := time.Now()
	if agoraT.After(prazo) {
		return "atrasado"
	}
	inicio, _ := parseData(c.CriadoEm)
	total := prazo.Sub(inicio)
	if falta := prazo.Sub(agoraT); falta < 2*time.Hour || (total > 0 && falta < total/4) {
		return "vence"
	}
	return "ok"
}

func (c Chamado) TextoPrazo() string {
	prazo, ok := parseData(c.Prazo)
	if !ok {
		return ""
	}
	switch c.SituacaoPrazo() {
	case "atrasado":
		return "atrasado há " + duracaoCurta(time.Since(prazo))
	case "vence", "ok":
		return "vence em " + duracaoCurta(time.Until(prazo))
	case "cumprido":
		return "resolvido no prazo"
	case "estourado":
		return "resolvido fora do prazo"
	}
	return ""
}

func duracaoCurta(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Hour:
		m := int(d.Minutes())
		if m < 1 {
			m = 1
		}
		return fmt.Sprintf("%d min", m)
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d dias", int(d.Hours()/24))
	}
}

type ComentarioChamado struct {
	ID            int64
	AutorNome     string
	Texto         string
	CriadoEm      string
	Interno       bool
	DoSolicitante bool
	Anexos        []Anexo
}

type EventoChamado struct {
	Em, Autor, Tipo, Detalhe string
}

// ItemLinha é uma entrada da linha do tempo do chamado (comentário ou evento).
type ItemLinha struct {
	Evento     *EventoChamado
	Comentario *ComentarioChamado
	Em         string
}

func nomeAdmin(a Admin) string {
	if strings.TrimSpace(a.Nome) != "" {
		return a.Nome
	}
	return a.Usuario
}

// ---------------- SLA ----------------

// HorasSLA devolve o prazo em horas para a prioridade (configuração "sla_<prioridade>").
func (b *Banco) HorasSLA(prioridade string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(b.Config("sla_" + prioridade))); err == nil && n > 0 {
		return n
	}
	if n, ok := slaPadrao[prioridade]; ok {
		return n
	}
	return slaPadrao["normal"]
}

func (b *Banco) calcularPrazo(criadoEm, prioridade string) string {
	t, ok := parseDataBanco(criadoEm)
	if !ok {
		t = time.Now().UTC()
	}
	return t.Add(time.Duration(b.HorasSLA(prioridade)) * time.Hour).UTC().Format(time.RFC3339)
}

// migrarChamados18 completa os chamados criados antes da 1.8: prazo, data de resolução e
// token do link de acompanhamento. Roda a cada início; só mexe no que estiver vazio.
func (b *Banco) migrarChamados18() {
	rows, err := b.db.Query(`SELECT id, criado_em, prioridade, status, COALESCE(fechado_em,''), atualizado_em FROM chamados
		WHERE prazo='' OR token_publico='' OR (resolvido_em='' AND status IN ('resolvido','fechado'))`)
	if err != nil {
		return
	}
	type pend struct {
		id                                        int64
		criado, prio, status, fechado, atualizado string
	}
	var l []pend
	for rows.Next() {
		var p pend
		if rows.Scan(&p.id, &p.criado, &p.prio, &p.status, &p.fechado, &p.atualizado) == nil {
			l = append(l, p)
		}
	}
	rows.Close()
	for _, p := range l {
		resolvido := ""
		if !statusAberto(p.status) {
			resolvido = p.fechado
			if resolvido == "" {
				resolvido = p.atualizado
			}
		}
		_, _ = b.db.Exec(`UPDATE chamados SET
			prazo = CASE WHEN prazo='' THEN ? ELSE prazo END,
			token_publico = CASE WHEN token_publico='' THEN ? ELSE token_publico END,
			resolvido_em = CASE WHEN resolvido_em='' THEN ? ELSE resolvido_em END
			WHERE id=?`, b.calcularPrazo(p.criado, p.prio), aleatorio(16), resolvido, p.id)
	}
}

// ---------------- Consultas ----------------

const colunasChamado = `c.id, c.titulo, c.descricao, c.categoria, c.prioridade, c.status,
	COALESCE(CASE WHEN cp.nome<>'' THEN cp.nome ELSE cp.usuario END, ''),
	COALESCE(CASE WHEN at.nome<>'' THEN at.nome ELSE at.usuario END, ''),
	COALESCE(m.hostname, ''),
	c.solicitante_nome, c.solicitante_email, c.solicitante_setor, c.origem, c.token_publico, c.ip_origem,
	COALESCE(c.criado_por,0), COALESCE(c.atribuido_a,0), COALESCE(c.maquina_id,0), COALESCE(c.solicitante_usuario_id,0),
	c.criado_em, c.atualizado_em, COALESCE(c.fechado_em,''), c.resolvido_em, c.prazo,
	(SELECT COUNT(*) FROM chamado_comentarios cc WHERE cc.chamado_id=c.id),
	(SELECT COUNT(*) FROM chamado_anexos ca WHERE ca.chamado_id=c.id),
	c.anydesk_id, COALESCE(m.anydesk_id, '')`

const joinsChamado = ` FROM chamados c
	LEFT JOIN admins cp ON cp.id=c.criado_por
	LEFT JOIN admins at ON at.id=c.atribuido_a
	LEFT JOIN maquinas m ON m.id=c.maquina_id `

type linhaScanner interface{ Scan(dest ...any) error }

func lerChamado(r linhaScanner) (Chamado, error) {
	var c Chamado
	err := r.Scan(&c.ID, &c.Titulo, &c.Descricao, &c.Categoria, &c.Prioridade, &c.Status,
		&c.CriadoPorNome, &c.AtribuidoNome, &c.MaquinaHostname,
		&c.SolicitanteNome, &c.SolicitanteEmail, &c.SolicitanteSetor, &c.Origem, &c.TokenPublico, &c.IPOrigem,
		&c.CriadoPorID, &c.AtribuidoID, &c.MaquinaID, &c.SolicitanteUID,
		&c.CriadoEm, &c.AtualizadoEm, &c.FechadoEm, &c.ResolvidoEm, &c.Prazo,
		&c.QtdComentarios, &c.QtdAnexos, &c.AnyDeskID, &c.AnyDeskMaquina)
	return c, err
}

// FiltroChamados: Status ("", "abertos", ou um status), Responsavel ("", "meus", "sem" ou id),
// Busca (texto ou #número), Atrasados, SoDoUsuario (perfil "usuário": só os que abriu).
type FiltroChamados struct {
	Status, Responsavel, Busca string
	Atrasados                  bool
	SoDoUsuario, EuID          int64
	Desde                      string // criado_em >= (RFC3339), para relatórios
}

func (b *Banco) Chamados(f FiltroChamados) ([]Chamado, error) {
	cond := []string{"1=1"}
	var args []any
	if contemStr(StatusChamado, f.Status) {
		cond = append(cond, "c.status = ?")
		args = append(args, f.Status)
	} else if f.Status == "abertos" {
		cond = append(cond, "c.status IN ('aberto','em_andamento')")
	}
	switch f.Responsavel {
	case "":
	case "meus":
		cond = append(cond, "c.atribuido_a = ?")
		args = append(args, f.EuID)
	case "sem":
		cond = append(cond, "c.atribuido_a IS NULL")
	default:
		if id := parseID(f.Responsavel); id > 0 {
			cond = append(cond, "c.atribuido_a = ?")
			args = append(args, id)
		}
	}
	if f.Atrasados {
		cond = append(cond, "c.status IN ('aberto','em_andamento') AND c.prazo<>'' AND c.prazo < ?")
		args = append(args, agora())
	}
	if f.SoDoUsuario > 0 {
		cond = append(cond, "c.criado_por = ?")
		args = append(args, f.SoDoUsuario)
	}
	if f.Desde != "" {
		cond = append(cond, "(c.criado_em >= ? OR c.resolvido_em >= ?)")
		args = append(args, f.Desde, f.Desde)
	}
	if q := strings.TrimSpace(f.Busca); q != "" {
		if n, err := strconv.ParseInt(strings.TrimPrefix(q, "#"), 10, 64); err == nil {
			cond = append(cond, "c.id = ?")
			args = append(args, n)
		} else {
			like := "%" + q + "%"
			cond = append(cond, `(c.titulo LIKE ? OR c.descricao LIKE ? OR c.solicitante_nome LIKE ? OR c.solicitante_setor LIKE ?
				OR m.hostname LIKE ? OR cp.nome LIKE ? OR cp.usuario LIKE ?)`)
			args = append(args, like, like, like, like, like, like, like)
		}
	}
	q := `SELECT ` + colunasChamado + joinsChamado + ` WHERE ` + strings.Join(cond, " AND ") + `
		ORDER BY (c.status IN ('resolvido','fechado')) ASC,
			CASE c.prioridade WHEN 'urgente' THEN 0 WHEN 'alta' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END ASC,
			c.atualizado_em DESC`
	rows, err := b.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []Chamado
	for rows.Next() {
		if c, err := lerChamado(rows); err == nil {
			l = append(l, c)
		}
	}
	return l, rows.Err()
}

func (b *Banco) Chamado(id int64) (*Chamado, error) {
	c, err := lerChamado(b.db.QueryRow(`SELECT `+colunasChamado+joinsChamado+` WHERE c.id=?`, id))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (b *Banco) ChamadoPorToken(token string) (*Chamado, error) {
	token = strings.TrimSpace(token)
	if len(token) < 16 {
		return nil, sql.ErrNoRows
	}
	c, err := lerChamado(b.db.QueryRow(`SELECT `+colunasChamado+joinsChamado+` WHERE c.token_publico=?`, token))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ---------------- Criação e alterações ----------------

type NovoChamado struct {
	Titulo, Descricao, Categoria, Prioridade            string
	CriadoPor, AtribuidoA, MaquinaID, SolicitanteUID    int64
	SolicitanteNome, SolicitanteEmail, SolicitanteSetor string
	Origem, IP                                          string
}

// CriarChamado insere o chamado (com prazo e token de acompanhamento) e registra o evento.
func (b *Banco) CriarChamado(n NovoChamado, autor string) (int64, string, error) {
	n.Titulo = strings.TrimSpace(n.Titulo)
	if n.Titulo == "" {
		return 0, "", fmt.Errorf("informe um título para o chamado")
	}
	if len([]rune(n.Titulo)) > 200 {
		n.Titulo = string([]rune(n.Titulo)[:200])
	}
	if !contemStr(PrioridadesChamado, n.Prioridade) {
		n.Prioridade = "normal"
	}
	if !contemStr(CategoriasChamado, n.Categoria) {
		n.Categoria = "Outro"
	}
	if n.Origem == "" {
		n.Origem = "painel"
	}
	t := agora()
	token := aleatorio(16)
	res, err := b.db.Exec(`INSERT INTO chamados(titulo,descricao,categoria,prioridade,status,criado_por,atribuido_a,maquina_id,
		criado_em,atualizado_em,solicitante_nome,solicitante_email,solicitante_setor,solicitante_usuario_id,origem,token_publico,ip_origem,prazo)
		VALUES(?,?,?,?, 'aberto', ?,?,?, ?,?, ?,?,?,?, ?,?,?,?)`,
		n.Titulo, strings.TrimSpace(n.Descricao), n.Categoria, n.Prioridade, nullID(n.CriadoPor), nullID(n.AtribuidoA), nullID(n.MaquinaID),
		t, t, limitar(n.SolicitanteNome, 120), limitar(n.SolicitanteEmail, 160), limitar(n.SolicitanteSetor, 80), nullID(n.SolicitanteUID),
		n.Origem, token, n.IP, b.calcularPrazo(t, n.Prioridade))
	if err != nil {
		return 0, "", err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, "", err
	}
	det := "prioridade " + rotuloPrioridade(n.Prioridade)
	if n.Origem == "publico" {
		det += " · aberto pela página sem login"
	}
	b.RegistrarEvento(id, autor, "criado", det)
	if n.MaquinaID > 0 {
		b.anexarAnyDesk(id, n.MaquinaID, autor)
	}
	if n.AtribuidoA > 0 {
		if a, err := b.AdminPorID(n.AtribuidoA); err == nil {
			b.RegistrarEvento(id, autor, "responsavel", "atribuído a "+nomeAdmin(*a))
		}
	}
	return id, token, nil
}

func limitar(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func nullID(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func contemStr(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// Mudancas descreve o que uma atualização alterou (para o histórico e o aviso).
type Mudancas struct {
	Itens           []string
	NovoResponsavel string // nome, se o responsável mudou para alguém
	Resolvido       bool   // passou para resolvido/fechado
	Reaberto        bool
}

func (b *Banco) AtualizarChamado(id int64, status, prioridade, categoria string, atribuidoA, maquinaID int64, autor string) (Mudancas, error) {
	var mud Mudancas
	if !contemStr(StatusChamado, status) {
		return mud, fmt.Errorf("status inválido")
	}
	if !contemStr(PrioridadesChamado, prioridade) {
		prioridade = "normal"
	}
	antes, err := b.Chamado(id)
	if err != nil {
		return mud, fmt.Errorf("chamado não encontrado")
	}
	t := agora()
	prazo := antes.Prazo
	resolvido := antes.ResolvidoEm
	fechado := antes.FechadoEm

	if status != antes.Status {
		mud.Itens = append(mud.Itens, fmt.Sprintf("status: %s → %s", rotuloStatus(antes.Status), rotuloStatus(status)))
		b.RegistrarEvento(id, autor, "status", rotuloStatus(antes.Status)+" → "+rotuloStatus(status))
		if statusAberto(antes.Status) && !statusAberto(status) {
			mud.Resolvido = true
			if resolvido == "" {
				resolvido = t
			}
		}
		if !statusAberto(antes.Status) && statusAberto(status) {
			mud.Reaberto = true
			resolvido, fechado = "", ""
		}
		if status == "fechado" && fechado == "" {
			fechado = t
		}
	}
	if prioridade != antes.Prioridade {
		mud.Itens = append(mud.Itens, fmt.Sprintf("prioridade: %s → %s", rotuloPrioridade(antes.Prioridade), rotuloPrioridade(prioridade)))
		b.RegistrarEvento(id, autor, "prioridade", rotuloPrioridade(antes.Prioridade)+" → "+rotuloPrioridade(prioridade))
		prazo = b.calcularPrazo(antes.CriadoEm, prioridade)
	}
	if categoria != antes.Categoria {
		b.RegistrarEvento(id, autor, "categoria", vazioTraco(antes.Categoria)+" → "+vazioTraco(categoria))
		mud.Itens = append(mud.Itens, "categoria: "+vazioTraco(categoria))
	}
	if atribuidoA != antes.AtribuidoID {
		novo := "ninguém"
		if atribuidoA > 0 {
			if a, err := b.AdminPorID(atribuidoA); err == nil {
				novo = nomeAdmin(*a)
				mud.NovoResponsavel = novo
			} else {
				atribuidoA = 0
			}
		}
		b.RegistrarEvento(id, autor, "responsavel", "responsável: "+vazioTraco(antes.AtribuidoNome)+" → "+novo)
		mud.Itens = append(mud.Itens, "responsável: "+novo)
	}
	if maquinaID != antes.MaquinaID {
		novo := "nenhuma"
		if maquinaID > 0 {
			var h string
			if b.db.QueryRow(`SELECT hostname FROM maquinas WHERE id=?`, maquinaID).Scan(&h) == nil {
				novo = h
			} else {
				maquinaID = 0
			}
		}
		b.RegistrarEvento(id, autor, "maquina", "máquina: "+vazioTraco(antes.MaquinaHostname)+" → "+novo)
		mud.Itens = append(mud.Itens, "máquina: "+novo)
		defer b.anexarAnyDesk(id, maquinaID, autor) // depois do UPDATE: troca o AnyDesk do chamado
	}
	if len(mud.Itens) == 0 {
		return mud, nil
	}
	_, err = b.db.Exec(`UPDATE chamados SET status=?, prioridade=?, categoria=?, atribuido_a=?, maquina_id=?,
		fechado_em=?, resolvido_em=?, prazo=?, atualizado_em=? WHERE id=?`,
		status, prioridade, categoria, nullID(atribuidoA), nullID(maquinaID), fechado, resolvido, prazo, t, id)
	return mud, err
}

// ReabrirPeloSolicitante: quando o solicitante responde a um chamado resolvido, ele volta
// para "em andamento" (fechado continua fechado).
func (b *Banco) ReabrirPeloSolicitante(id int64, autor string) bool {
	res, err := b.db.Exec(`UPDATE chamados SET status='em_andamento', resolvido_em='', atualizado_em=? WHERE id=? AND status='resolvido'`, agora(), id)
	if err != nil {
		return false
	}
	if n, _ := res.RowsAffected(); n > 0 {
		b.RegistrarEvento(id, autor, "status", "Resolvido → Em andamento (resposta do solicitante)")
		return true
	}
	return false
}

func vazioTraco(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func (b *Banco) RegistrarEvento(chamadoID int64, autor, tipo, detalhe string) {
	_, _ = b.db.Exec(`INSERT INTO chamado_eventos(chamado_id,em,autor,tipo,detalhe) VALUES(?,?,?,?,?)`,
		chamadoID, agora(), autor, tipo, detalhe)
}

func (b *Banco) EventosChamado(id int64) ([]EventoChamado, error) {
	rows, err := b.db.Query(`SELECT em, autor, tipo, detalhe FROM chamado_eventos WHERE chamado_id=? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []EventoChamado
	for rows.Next() {
		var e EventoChamado
		if rows.Scan(&e.Em, &e.Autor, &e.Tipo, &e.Detalhe) == nil {
			l = append(l, e)
		}
	}
	return l, rows.Err()
}

// ---------------- Comentários ----------------

// AdicionarComentario grava um comentário. autorID=0 indica o solicitante (página pública),
// identificado por autorNome. Devolve o id do comentário (para vincular anexos).
func (b *Banco) AdicionarComentario(chamadoID, autorID int64, autorNome, texto string, interno, permitirVazio bool) (int64, error) {
	texto = strings.TrimSpace(texto)
	if texto == "" && !permitirVazio {
		return 0, fmt.Errorf("escreva um comentário")
	}
	if len(texto) > 20000 {
		texto = texto[:20000]
	}
	in := 0
	if interno {
		in = 1
	}
	res, err := b.db.Exec(`INSERT INTO chamado_comentarios(chamado_id,autor_id,autor_nome,texto,interno,criado_em) VALUES(?,?,?,?,?,?)`,
		chamadoID, nullID(autorID), limitar(autorNome, 120), texto, in, agora())
	if err != nil {
		return 0, err
	}
	_, _ = b.db.Exec(`UPDATE chamados SET atualizado_em=? WHERE id=?`, agora(), chamadoID)
	return res.LastInsertId()
}

func (b *Banco) ComentariosChamado(id int64, incluirInternos bool) ([]ComentarioChamado, error) {
	rows, err := b.db.Query(`SELECT cc.id,
		COALESCE(CASE WHEN a.nome<>'' THEN a.nome ELSE a.usuario END, NULLIF(cc.autor_nome,''), 'removido'),
		cc.texto, cc.criado_em, cc.interno, cc.autor_id IS NULL
		FROM chamado_comentarios cc LEFT JOIN admins a ON a.id=cc.autor_id
		WHERE cc.chamado_id=? ORDER BY cc.criado_em ASC, cc.id ASC`, id)
	if err != nil {
		return nil, err
	}
	var l []ComentarioChamado
	for rows.Next() {
		var c ComentarioChamado
		var interno, doSol int
		if rows.Scan(&c.ID, &c.AutorNome, &c.Texto, &c.CriadoEm, &interno, &doSol) == nil {
			c.Interno, c.DoSolicitante = interno == 1, doSol == 1
			if c.Interno && !incluirInternos {
				continue
			}
			l = append(l, c)
		}
	}
	rows.Close()
	anexos, _ := b.AnexosChamado(id)
	for i := range l {
		for _, a := range anexos {
			if a.ComentarioID == l[i].ID {
				l[i].Anexos = append(l[i].Anexos, a)
			}
		}
	}
	return l, nil
}

// LinhaDoTempo junta comentários e eventos em ordem cronológica.
func (b *Banco) LinhaDoTempo(id int64, incluirInternos, incluirEventos bool) []ItemLinha {
	var itens []ItemLinha
	coment, _ := b.ComentariosChamado(id, incluirInternos)
	for i := range coment {
		itens = append(itens, ItemLinha{Comentario: &coment[i], Em: coment[i].CriadoEm})
	}
	if incluirEventos {
		ev, _ := b.EventosChamado(id)
		for i := range ev {
			if ev[i].Tipo == "anexo" {
				continue // os anexos já aparecem junto da descrição ou do comentário
			}
			itens = append(itens, ItemLinha{Evento: &ev[i], Em: ev[i].Em})
		}
	}
	sort.SliceStable(itens, func(i, j int) bool { return itens[i].Em < itens[j].Em })
	return itens
}

// ResumoChamados conta os chamados abertos/em andamento (para o menu).
func (b *Banco) ResumoChamados() int {
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM chamados WHERE status IN ('aberto','em_andamento')`).Scan(&n)
	return n
}

func (b *Banco) AdminPorID(id int64) (*Admin, error) {
	a, err := lerAdmin(b.db.QueryRow(`SELECT `+colunasAdmin+` FROM admins WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ---------------- Avisos (webhook) ----------------

// avisarChamado envia a mensagem para o webhook dos chamados (ou o geral, se não houver
// um específico). Desligável em Administração ("avisar_chamados=0").
func (s *Servidor) avisarChamado(msg string) {
	if s.b.Config("avisar_chamados") == "0" {
		return
	}
	u := strings.TrimSpace(s.b.Config("webhook_chamados"))
	if u == "" {
		u = s.b.Config("webhook_url")
	}
	if u == "" {
		return
	}
	go func() { _ = s.postarWebhook(u, msg) }()
}

// ---------------- Handlers (área logada) ----------------

func (s *Servidor) filtroDaURL(r *http.Request, sess *Sessao) FiltroChamados {
	q := r.URL.Query()
	f := FiltroChamados{Status: q.Get("status"), Responsavel: q.Get("resp"), Busca: q.Get("q"),
		Atrasados: q.Get("atrasados") == "1", EuID: sess.Admin.ID}
	if _, definido := q["status"]; !definido && sess.Admin.PodeGerenciarChamados() {
		f.Status = "abertos" // padrão da fila da TI: o que ainda precisa de atenção
	}
	if !sess.Admin.PodeGerenciarChamados() {
		f.SoDoUsuario = sess.Admin.ID // usuário comum só vê os próprios
		f.Responsavel = ""
	}
	return f
}

func (s *Servidor) paginaChamados(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	f := s.filtroDaURL(r, sess)
	lista, _ := s.b.Chamados(f)
	atrasados := 0
	semResp := 0
	for _, c := range lista {
		if c.SituacaoPrazo() == "atrasado" {
			atrasados++
		}
		if c.Aberto() && c.AtribuidoID == 0 {
			semResp++
		}
	}
	s.render(w, r, "chamados", "Chamados", "chamados", sess, map[string]any{
		"Chamados": lista, "F": f, "PodeGerenciar": sess.Admin.PodeGerenciarChamados(),
		"Tecnicos": s.tecnicos(), "Atrasados": atrasados, "SemResponsavel": semResp,
		"LinkPublico": s.baseURL(r) + "/abrir-chamado", "PublicoAtivo": s.b.Config("chamados_publico") != "0",
	})
}

func (s *Servidor) paginaNovoChamado(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	d := map[string]any{
		"Categorias": CategoriasChamado, "Prioridades": PrioridadesChamado,
		"PodeGerenciar": sess.Admin.PodeGerenciarChamados(), "MaxAnexoMB": maxAnexo >> 20, "MaxAnexos": maxAnexosPorEnvio,
	}
	if sess.Admin.PodeGerenciarChamados() {
		d["Tecnicos"] = s.tecnicos()
		d["Maquinas"] = s.maquinasLeves()
		d["Pessoas"] = s.b.PessoasConhecidas()
		d["Setores"] = s.b.Setores()
	}
	s.render(w, r, "chamado_novo", "Novo chamado", "chamados_novo", sess, d)
}

func (s *Servidor) acaoCriarChamado(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	arquivos, err := lerAnexos(r)
	if err != nil {
		redirecionar(w, r, "/chamados/novo", "", err)
		return
	}
	n := NovoChamado{
		Titulo: r.FormValue("titulo"), Descricao: r.FormValue("descricao"), Categoria: r.FormValue("categoria"),
		Prioridade: r.FormValue("prioridade"), CriadoPor: sess.Admin.ID, Origem: "painel", IP: ipCliente(r),
	}
	if sess.Admin.PodeGerenciarChamados() {
		n.AtribuidoA = parseID(r.FormValue("atribuido_a"))
		n.MaquinaID = parseID(r.FormValue("maquina_id"))
		// TI registrando em nome de alguém (ex.: pedido por telefone)
		if nome := strings.TrimSpace(r.FormValue("solicitante")); nome != "" {
			p := s.b.ReconhecerPessoa(nome)
			n.SolicitanteNome, n.SolicitanteUID = p.Nome, p.UsuarioID
			n.SolicitanteSetor = strings.TrimSpace(r.FormValue("setor"))
			if n.SolicitanteSetor == "" {
				n.SolicitanteSetor = p.Setor
			}
			if n.MaquinaID == 0 && p.UsuarioID > 0 {
				n.MaquinaID = s.b.UltimaMaquinaDoUsuario(p.UsuarioID)
			}
		}
	} else {
		n.SolicitanteNome = nomeAdmin(sess.Admin)
		n.SolicitanteEmail = sess.Admin.Email
		n.Prioridade = restringirPrioridade(n.Prioridade)
		if m := s.b.MaquinaPorIP(ipCliente(r)); m != nil {
			n.MaquinaID = m.ID
		}
	}
	if n.AtribuidoA == 0 {
		n.AtribuidoA = s.responsavelAutomatico()
	}
	id, token, err := s.b.CriarChamado(n, nomeAdmin(sess.Admin))
	if err != nil {
		redirecionar(w, r, "/chamados/novo", "", err)
		return
	}
	s.salvarAnexos(id, 0, arquivos, nomeAdmin(sess.Admin))
	s.avisarNovoChamadoPorEmail(r, id, sess.Admin.ID)
	s.b.Auditar(sess.Admin.Usuario, "abrir chamado", fmt.Sprintf("#%d %s", id, n.Titulo))
	c, _ := s.b.Chamado(id)
	if c != nil {
		s.avisarChamado(fmt.Sprintf("🆕 Chamado #%d (%s): %s — %s · %s", id, c.RotuloPrioridade(), c.Titulo, c.Solicitante(), s.baseURL(r)+"/chamados/"+strconv.FormatInt(id, 10)))
		if n.AtribuidoA > 0 && c.AtribuidoNome != "" {
			s.avisarChamado(fmt.Sprintf("👤 Chamado #%d atribuído a %s: %s", id, c.AtribuidoNome, c.Titulo))
		}
	}
	_ = token
	redirecionar(w, r, fmt.Sprintf("/chamados/%d", id), "Chamado aberto.", nil)
}

func restringirPrioridade(p string) string {
	if !contemStr(PrioridadesPublicas, p) {
		return "normal"
	}
	return p
}

// podeVer: admin/técnico veem todos; usuário comum só os que abriu.
func podeVer(sess *Sessao, c *Chamado) bool {
	return sess.Admin.PodeGerenciarChamados() || c.CriadoPorID == sess.Admin.ID
}

func (s *Servidor) chamadoDaURL(w http.ResponseWriter, r *http.Request, sess *Sessao) *Chamado {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, err := s.b.Chamado(id)
	if err != nil || !podeVer(sess, c) {
		redirecionar(w, r, "/chamados", "", fmt.Errorf("chamado não encontrado ou sem acesso"))
		return nil
	}
	return c
}

func (s *Servidor) paginaChamado(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	c := s.chamadoDaURL(w, r, sess)
	if c == nil {
		return
	}
	ger := sess.Admin.PodeGerenciarChamados()
	anexos, _ := s.b.AnexosChamado(c.ID)
	var anexosAbertura []Anexo
	for _, a := range anexos {
		if a.ComentarioID == 0 {
			anexosAbertura = append(anexosAbertura, a)
		}
	}
	d := map[string]any{
		"C": c, "Linha": s.b.LinhaDoTempo(c.ID, ger, true), "AnexosAbertura": anexosAbertura,
		"Categorias": CategoriasChamado, "Prioridades": PrioridadesChamado, "StatusLista": StatusChamado,
		"PodeGerenciar": ger, "MaxAnexoMB": maxAnexo >> 20, "MaxAnexos": maxAnexosPorEnvio,
		"LinkAcompanhar": s.baseURL(r) + "/acompanhar/" + c.TokenPublico,
	}
	if ger {
		d["Tecnicos"] = s.tecnicos()
		d["Maquinas"] = s.maquinasLeves()
		if c.MaquinaID > 0 {
			if m, err := s.b.Maquina(c.MaquinaID); err == nil {
				d["Maquina"] = m
			}
		}
	}
	s.render(w, r, "chamado", "Chamado #"+strconv.FormatInt(c.ID, 10), "chamados", sess, d)
}

func (s *Servidor) acaoComentarChamado(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	c := s.chamadoDaURL(w, r, sess)
	if c == nil {
		return
	}
	destino := fmt.Sprintf("/chamados/%d", c.ID)
	arquivos, err := lerAnexos(r)
	if err != nil {
		redirecionar(w, r, destino, "", err)
		return
	}
	interno := sess.Admin.PodeGerenciarChamados() && r.FormValue("interno") == "1"
	cid, err := s.b.AdicionarComentario(c.ID, sess.Admin.ID, "", r.FormValue("texto"), interno, len(arquivos) > 0)
	if err != nil {
		redirecionar(w, r, destino, "", err)
		return
	}
	s.salvarAnexos(c.ID, cid, arquivos, nomeAdmin(sess.Admin))
	// o próprio solicitante (perfil usuário) respondendo um chamado resolvido reabre
	if !sess.Admin.PodeGerenciarChamados() {
		s.b.ReabrirPeloSolicitante(c.ID, nomeAdmin(sess.Admin))
		s.avisarChamado(fmt.Sprintf("💬 %s respondeu o chamado #%d: %s · %s", nomeAdmin(sess.Admin), c.ID, c.Titulo, s.baseURL(r)+destino))
	}
	msg := "Comentário adicionado."
	if interno {
		msg = "Nota interna adicionada (o solicitante não vê)."
	}
	redirecionar(w, r, destino+"#fim", msg, nil)
}

func (s *Servidor) acaoAtualizarChamado(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	c := s.chamadoDaURL(w, r, sess)
	if c == nil {
		return
	}
	destino := fmt.Sprintf("/chamados/%d", c.ID)
	if !sess.Admin.PodeGerenciarChamados() {
		redirecionar(w, r, destino, "", fmt.Errorf("sem permissão para alterar o chamado"))
		return
	}
	mud, err := s.b.AtualizarChamado(c.ID, r.FormValue("status"), r.FormValue("prioridade"), r.FormValue("categoria"),
		parseID(r.FormValue("atribuido_a")), parseID(r.FormValue("maquina_id")), nomeAdmin(sess.Admin))
	if err != nil {
		redirecionar(w, r, destino, "", err)
		return
	}
	if len(mud.Itens) == 0 {
		redirecionar(w, r, destino, "Nada foi alterado.", nil)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "alterar chamado", fmt.Sprintf("#%d: %s", c.ID, strings.Join(mud.Itens, "; ")))
	link := s.baseURL(r) + destino
	if mud.NovoResponsavel != "" {
		s.avisarChamado(fmt.Sprintf("👤 Chamado #%d atribuído a %s: %s · %s", c.ID, mud.NovoResponsavel, c.Titulo, link))
		s.avisarAtribuicaoPorEmail(r, c.ID, parseID(r.FormValue("atribuido_a")), sess.Admin.ID)
	}
	if mud.Resolvido {
		s.avisarChamado(fmt.Sprintf("✅ Chamado #%d resolvido por %s: %s", c.ID, nomeAdmin(sess.Admin), c.Titulo))
	}
	redirecionar(w, r, destino, "Chamado atualizado.", nil)
}

// acaoAssumirChamado: atalho "Assumir" — atribui a quem clicou e passa para em andamento.
func (s *Servidor) acaoAssumirChamado(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	c := s.chamadoDaURL(w, r, sess)
	if c == nil {
		return
	}
	destino := fmt.Sprintf("/chamados/%d", c.ID)
	if !sess.Admin.PodeGerenciarChamados() {
		redirecionar(w, r, destino, "", fmt.Errorf("sem permissão"))
		return
	}
	status := c.Status
	if status == "aberto" {
		status = "em_andamento"
	}
	_, err := s.b.AtualizarChamado(c.ID, status, c.Prioridade, c.Categoria, sess.Admin.ID, c.MaquinaID, nomeAdmin(sess.Admin))
	redirecionar(w, r, destino, "Chamado assumido por você.", err)
}

// ---------------- auxiliares ----------------

func parseID(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func (s *Servidor) tecnicos() []Admin {
	t, _ := s.b.Tecnicos()
	return t
}

type maquinaLeve struct {
	ID       int64
	Hostname string
}

func (s *Servidor) maquinasLeves() []maquinaLeve {
	maqs, _ := s.b.Maquinas("")
	var l []maquinaLeve
	for _, m := range maqs {
		l = append(l, maquinaLeve{ID: m.ID, Hostname: m.Hostname})
	}
	return l
}
