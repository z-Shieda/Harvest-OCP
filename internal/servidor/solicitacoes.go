package servidor

// Solicitações de equipamento (1.24): pedido de compra de T.I que passa pela T.I (valida a
// especificação), pelo gestor (autoriza, logado) e pelo Suprimentos (registra a compra), e volta
// à T.I para a entrega. Sem SLA; conta nos atendimentos. Especificação: docs/SDD-1.24-SOLICITACOES.md.

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const esquemaSolicitacoes = `
CREATE TABLE IF NOT EXISTS sol_catalogo (
	id INTEGER PRIMARY KEY,
	nome TEXT NOT NULL,
	categoria TEXT NOT NULL DEFAULT '',
	especificacao TEXT NOT NULL DEFAULT '',
	ativo INTEGER NOT NULL DEFAULT 1,
	ordem INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS solicitacoes (
	id INTEGER PRIMARY KEY,
	solicitante_id INTEGER REFERENCES admins(id) ON DELETE SET NULL,
	aberto_por INTEGER REFERENCES admins(id) ON DELETE SET NULL,
	para_quem TEXT NOT NULL DEFAULT '',
	setor TEXT NOT NULL DEFAULT '',
	catalogo_id INTEGER,
	item TEXT NOT NULL,
	especificacao TEXT NOT NULL DEFAULT '',
	quantidade INTEGER NOT NULL DEFAULT 1,
	justificativa TEXT NOT NULL DEFAULT '',
	urgencia TEXT NOT NULL DEFAULT 'normal',
	situacao TEXT NOT NULL DEFAULT 'solicitada',
	ti_por TEXT NOT NULL DEFAULT '',
	ti_em TEXT NOT NULL DEFAULT '',
	ti_obs TEXT NOT NULL DEFAULT '',
	gestor_id INTEGER REFERENCES admins(id) ON DELETE SET NULL,
	dispensa_gestor TEXT NOT NULL DEFAULT '',
	autorizado_por TEXT NOT NULL DEFAULT '',
	autorizado_em TEXT NOT NULL DEFAULT '',
	autorizado_ip TEXT NOT NULL DEFAULT '',
	motivo_recusa TEXT NOT NULL DEFAULT '',
	recusado_por TEXT NOT NULL DEFAULT '',
	comprado INTEGER,
	setor_destino TEXT NOT NULL DEFAULT '',
	fornecedor TEXT NOT NULL DEFAULT '',
	valor TEXT NOT NULL DEFAULT '',
	pedido_nf TEXT NOT NULL DEFAULT '',
	previsao TEXT NOT NULL DEFAULT '',
	compra_por TEXT NOT NULL DEFAULT '',
	compra_em TEXT NOT NULL DEFAULT '',
	entregue_por TEXT NOT NULL DEFAULT '',
	entregue_em TEXT NOT NULL DEFAULT '',
	ativo_id INTEGER,
	termo_id INTEGER,
	criado_em TEXT NOT NULL,
	atualizado_em TEXT NOT NULL,
	lembrete_em TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sol_situacao ON solicitacoes(situacao);
CREATE TABLE IF NOT EXISTS sol_eventos (
	id INTEGER PRIMARY KEY,
	solicitacao_id INTEGER NOT NULL REFERENCES solicitacoes(id) ON DELETE CASCADE,
	em TEXT NOT NULL,
	autor TEXT NOT NULL DEFAULT '',
	tipo TEXT NOT NULL,
	texto TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sol_eventos ON sol_eventos(solicitacao_id);
CREATE TABLE IF NOT EXISTS sol_anexos (
	id INTEGER PRIMARY KEY,
	solicitacao_id INTEGER NOT NULL REFERENCES solicitacoes(id) ON DELETE CASCADE,
	nome TEXT NOT NULL,
	tipo TEXT NOT NULL DEFAULT '',
	tamanho INTEGER NOT NULL DEFAULT 0,
	dados BLOB NOT NULL,
	autor TEXT NOT NULL DEFAULT '',
	criado_em TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sol_anexos ON sol_anexos(solicitacao_id);
`

func (b *Banco) migrarSolicitacoes() {
	_, _ = b.db.Exec(esquemaSolicitacoes)
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM sol_catalogo`).Scan(&n)
	if n == 0 {
		for i, c := range [][3]string{
			{"Notebook", "Computadores", "Core i5 (12ª ger. ou mais nova) ou Ryzen 5, 16 GB de RAM, SSD 512 GB, Windows 11 Pro, garantia de 3 anos"},
			{"Desktop", "Computadores", "Core i5 (12ª ger. ou mais nova), 16 GB de RAM, SSD 512 GB, Windows 11 Pro"},
			{"Monitor", "Periféricos", "24\", Full HD, entradas HDMI e DisplayPort, ajuste de altura"},
			{"Teclado", "Periféricos", "ABNT2, USB, com fio"},
			{"Mouse", "Periféricos", "USB, com fio, óptico"},
			{"Headset", "Periféricos", "USB, com microfone e cancelamento de ruído"},
			{"Webcam", "Periféricos", "Full HD 1080p, USB, com microfone"},
			{"Dock / hub USB-C", "Acessórios", "USB-C com HDMI, rede (RJ45) e 3 USB"},
			{"Cabo / adaptador", "Acessórios", "Descreva o tipo na justificativa (HDMI, DisplayPort, USB-C…)"},
			{"Celular corporativo", "Telefonia", "Android, 128 GB, dual chip"},
		} {
			_, _ = b.db.Exec(`INSERT INTO sol_catalogo(nome,categoria,especificacao,ativo,ordem) VALUES(?,?,?,1,?)`, c[0], c[1], c[2], i+1)
		}
	}
}

// ---------------- catálogo ----------------

type ItemCatalogo struct {
	ID                             int64
	Nome, Categoria, Especificacao string
	Ativo                          bool
	Ordem                          int
}

func (b *Banco) Catalogo(soAtivos bool) []ItemCatalogo {
	q := `SELECT id, nome, categoria, especificacao, ativo, ordem FROM sol_catalogo`
	if soAtivos {
		q += ` WHERE ativo=1`
	}
	rows, err := b.db.Query(q + ` ORDER BY categoria, ordem, nome`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []ItemCatalogo
	for rows.Next() {
		var c ItemCatalogo
		if rows.Scan(&c.ID, &c.Nome, &c.Categoria, &c.Especificacao, &c.Ativo, &c.Ordem) == nil {
			l = append(l, c)
		}
	}
	return l
}

func (b *Banco) ItemCatalogo(id int64) (*ItemCatalogo, error) {
	var c ItemCatalogo
	err := b.db.QueryRow(`SELECT id, nome, categoria, especificacao, ativo, ordem FROM sol_catalogo WHERE id=?`, id).
		Scan(&c.ID, &c.Nome, &c.Categoria, &c.Especificacao, &c.Ativo, &c.Ordem)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (b *Banco) SalvarItemCatalogo(c ItemCatalogo) error {
	c.Nome = limitar(c.Nome, 80)
	if c.Nome == "" {
		return errors.New("informe o nome do item")
	}
	c.Categoria, c.Especificacao = limitar(c.Categoria, 60), limitar(c.Especificacao, 600)
	if c.ID > 0 {
		_, err := b.db.Exec(`UPDATE sol_catalogo SET nome=?, categoria=?, especificacao=?, ativo=? WHERE id=?`, c.Nome, c.Categoria, c.Especificacao, c.Ativo, c.ID)
		return err
	}
	_, err := b.db.Exec(`INSERT INTO sol_catalogo(nome,categoria,especificacao,ativo,ordem) VALUES(?,?,?,1,(SELECT COALESCE(MAX(ordem),0)+1 FROM sol_catalogo))`,
		c.Nome, c.Categoria, c.Especificacao)
	return err
}

// ---------------- solicitação ----------------

type Solicitacao struct {
	ID                                         int64
	SolicitanteID, AbertoPor                   int64
	ParaQuem, Setor                            string
	CatalogoID                                 int64
	Item, Especificacao                        string
	Quantidade                                 int
	Justificativa, Urgencia, Situacao          string
	TIPor, TIEm, TIObs                         string
	GestorID                                   int64
	DispensaGestor                             string
	AutorizadoPor, AutorizadoEm, AutorizadoIP  string
	MotivoRecusa, RecusadoPor                  string
	Comprado                                   sql.NullBool
	SetorDestino, Fornecedor, Valor, PedidoNF  string
	Previsao, CompraPor, CompraEm              string
	EntreguePor, EntregueEm                    string
	AtivoID, TermoID                           int64
	CriadoEm, AtualizadoEm, LembreteEm         string
	// leitura
	Solicitante, SolicitanteEmail, Gestor, GestorEmail string
}

func (s Solicitacao) Numero() string { return fmt.Sprintf("SOL-%04d", s.ID) }

func (s Solicitacao) RotuloSituacao() string { return rotuloSituacaoSol(s.Situacao) }

func rotuloSituacaoSol(x string) string {
	switch x {
	case "solicitada":
		return "Aguardando a T.I"
	case "aguardando_gestor":
		return "Aguardando o gestor"
	case "em_compra":
		return "Com o Suprimentos"
	case "comprada":
		return "Comprada"
	case "nao_comprada":
		return "Não comprada"
	case "entregue":
		return "Entregue"
	case "recusada":
		return "Recusada"
	case "cancelada":
		return "Cancelada"
	}
	return x
}

func (s Solicitacao) CorSituacao() string {
	switch s.Situacao {
	case "entregue", "comprada":
		return "verde"
	case "recusada", "nao_comprada":
		return "vermelho"
	case "cancelada":
		return ""
	}
	return "amarelo"
}

func (s Solicitacao) RotuloUrgencia() string {
	switch s.Urgencia {
	case "alta":
		return "Alta"
	case "baixa":
		return "Baixa"
	}
	return "Normal"
}

// Aberta: ainda há o que fazer (conta como atendimento em andamento).
func (s Solicitacao) Aberta() bool {
	switch s.Situacao {
	case "solicitada", "aguardando_gestor", "em_compra", "comprada":
		return true
	}
	return false
}

func (s Solicitacao) ValorFormatado() string {
	if s.Valor == "" {
		return ""
	}
	return formatarReais(s.Valor)
}

// Etapa (1–5) da linha de progresso.
func (s Solicitacao) Etapa() int {
	switch s.Situacao {
	case "solicitada":
		return 1
	case "aguardando_gestor":
		return 2
	case "em_compra":
		return 3
	case "comprada", "nao_comprada":
		return 4
	case "entregue":
		return 5
	}
	return 0
}

const colunasSol = `s.id, COALESCE(s.solicitante_id,0), COALESCE(s.aberto_por,0), s.para_quem, s.setor, COALESCE(s.catalogo_id,0), s.item, s.especificacao,
	s.quantidade, s.justificativa, s.urgencia, s.situacao, s.ti_por, s.ti_em, s.ti_obs, COALESCE(s.gestor_id,0), s.dispensa_gestor,
	s.autorizado_por, s.autorizado_em, s.autorizado_ip, s.motivo_recusa, s.recusado_por, s.comprado, s.setor_destino, s.fornecedor, s.valor,
	s.pedido_nf, s.previsao, s.compra_por, s.compra_em, s.entregue_por, s.entregue_em, COALESCE(s.ativo_id,0), COALESCE(s.termo_id,0),
	s.criado_em, s.atualizado_em, s.lembrete_em,
	COALESCE(NULLIF(a.nome,''), a.usuario, ''), COALESCE(NULLIF(a.email,''), ''), COALESCE(NULLIF(g.nome,''), g.usuario, ''), COALESCE(g.email,'')`

const fromSol = ` FROM solicitacoes s LEFT JOIN admins a ON a.id=s.solicitante_id LEFT JOIN admins g ON g.id=s.gestor_id`

func lerSolicitacao(sc interface{ Scan(...any) error }) (*Solicitacao, error) {
	var s Solicitacao
	err := sc.Scan(&s.ID, &s.SolicitanteID, &s.AbertoPor, &s.ParaQuem, &s.Setor, &s.CatalogoID, &s.Item, &s.Especificacao,
		&s.Quantidade, &s.Justificativa, &s.Urgencia, &s.Situacao, &s.TIPor, &s.TIEm, &s.TIObs, &s.GestorID, &s.DispensaGestor,
		&s.AutorizadoPor, &s.AutorizadoEm, &s.AutorizadoIP, &s.MotivoRecusa, &s.RecusadoPor, &s.Comprado, &s.SetorDestino, &s.Fornecedor, &s.Valor,
		&s.PedidoNF, &s.Previsao, &s.CompraPor, &s.CompraEm, &s.EntreguePor, &s.EntregueEm, &s.AtivoID, &s.TermoID,
		&s.CriadoEm, &s.AtualizadoEm, &s.LembreteEm, &s.Solicitante, &s.SolicitanteEmail, &s.Gestor, &s.GestorEmail)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (b *Banco) Solicitacao(id int64) (*Solicitacao, error) {
	return lerSolicitacao(b.db.QueryRow(`SELECT `+colunasSol+fromSol+` WHERE s.id=?`, id))
}

// TodasSolicitacoes (o filtro de visibilidade fica em podeVerSol).
func (b *Banco) TodasSolicitacoes(limite int) []*Solicitacao {
	rows, err := b.db.Query(`SELECT `+colunasSol+fromSol+` ORDER BY s.id DESC LIMIT ?`, limite)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []*Solicitacao
	for rows.Next() {
		if s, err := lerSolicitacao(rows); err == nil {
			l = append(l, s)
		}
	}
	return l
}

func (b *Banco) EventoSol(id int64, autor, tipo, texto string) {
	_, _ = b.db.Exec(`INSERT INTO sol_eventos(solicitacao_id,em,autor,tipo,texto) VALUES(?,?,?,?,?)`, id, agora(), autor, tipo, texto)
	_, _ = b.db.Exec(`UPDATE solicitacoes SET atualizado_em=? WHERE id=?`, agora(), id)
}

type EventoSol struct {
	Em, Autor, Tipo, Texto string
}

func (b *Banco) EventosSol(id int64) []EventoSol {
	rows, err := b.db.Query(`SELECT em, autor, tipo, texto FROM sol_eventos WHERE solicitacao_id=? ORDER BY id`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []EventoSol
	for rows.Next() {
		var e EventoSol
		if rows.Scan(&e.Em, &e.Autor, &e.Tipo, &e.Texto) == nil {
			l = append(l, e)
		}
	}
	return l
}

// AnexoSol reaproveita o tipo Anexo dos chamados (mesmo visual).
func (b *Banco) AnexosSol(id int64) []Anexo {
	rows, err := b.db.Query(`SELECT id, solicitacao_id, nome, tipo, tamanho, autor, criado_em FROM sol_anexos WHERE solicitacao_id=? ORDER BY id`, id)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []Anexo
	for rows.Next() {
		var a Anexo
		if rows.Scan(&a.ID, &a.ChamadoID, &a.Nome, &a.Tipo, &a.Tamanho, &a.Autor, &a.CriadoEm) == nil {
			l = append(l, a)
		}
	}
	return l
}

func (b *Banco) AnexoSol(solID, anexoID int64) (*Anexo, []byte, error) {
	var a Anexo
	var dados []byte
	err := b.db.QueryRow(`SELECT id, solicitacao_id, nome, tipo, tamanho, autor, criado_em, dados FROM sol_anexos WHERE id=? AND solicitacao_id=?`, anexoID, solID).
		Scan(&a.ID, &a.ChamadoID, &a.Nome, &a.Tipo, &a.Tamanho, &a.Autor, &a.CriadoEm, &dados)
	if err != nil {
		return nil, nil, err
	}
	return &a, dados, nil
}

func (b *Banco) salvarAnexosSol(id int64, arqs []arquivoEnviado, autor string) {
	for _, a := range arqs {
		if _, err := b.db.Exec(`INSERT INTO sol_anexos(solicitacao_id,nome,tipo,tamanho,dados,autor,criado_em) VALUES(?,?,?,?,?,?,?)`,
			id, a.nome, a.tipo, len(a.dados), a.dados, autor, agora()); err == nil {
			b.EventoSol(id, autor, "anexo", a.nome)
		}
	}
}

// ---------------- regras ----------------

// equipeTI: Admin e Técnico analisam e entregam.
func equipeTI(a Admin) bool { return a.PodeGerenciarChamados() }

// podeVerSol: quem enxerga a solicitação.
func (b *Banco) podeVerSol(a Admin, s *Solicitacao) bool {
	if equipeTI(a) || s.SolicitanteID == a.ID || s.AbertoPor == a.ID || s.GestorID == a.ID {
		return true
	}
	if b.temPoder(a, PoderComprar) && s.Situacao != "solicitada" && s.Situacao != "aguardando_gestor" && s.Situacao != "recusada" && s.Situacao != "cancelada" {
		return true
	}
	if b.temPoder(a, PoderVerSetor) {
		for _, x := range b.SetoresGestor(a.ID) {
			if strings.EqualFold(x, s.Setor) || strings.EqualFold(x, s.SetorDestino) {
				return true
			}
		}
	}
	return false
}

var urgencias = []opcao{{"baixa", "Baixa"}, {"normal", "Normal"}, {"alta", "Alta"}}

// NovaSolicitacao: o que vem do formulário.
type NovaSolicitacao struct {
	SolicitanteID, AbertoPor, CatalogoID int64
	ParaQuem, Setor, Item, Especificacao string
	Quantidade                           int
	Justificativa, Urgencia              string
}

func (b *Banco) CriarSolicitacao(n NovaSolicitacao, autor string) (int64, error) {
	if n.CatalogoID > 0 {
		c, err := b.ItemCatalogo(n.CatalogoID)
		if err != nil || !c.Ativo {
			return 0, errors.New("item do catálogo inválido")
		}
		n.Item = c.Nome
		if strings.TrimSpace(n.Especificacao) == "" {
			n.Especificacao = c.Especificacao
		}
	}
	n.Item = limitar(n.Item, 120)
	if n.Item == "" {
		return 0, errors.New("escolha um item do catálogo ou descreva o que precisa")
	}
	n.Justificativa = limitar(n.Justificativa, 2000)
	if len([]rune(n.Justificativa)) < 10 {
		return 0, errors.New("conte em poucas palavras para que é (justificativa)")
	}
	if n.Quantidade < 1 || n.Quantidade > 500 {
		return 0, errors.New("quantidade inválida")
	}
	if n.Urgencia != "baixa" && n.Urgencia != "alta" {
		n.Urgencia = "normal"
	}
	n.Setor = limitar(n.Setor, 80)
	if n.Setor == "" {
		return 0, errors.New("informe o setor")
	}
	t := agora()
	x, err := b.db.Exec(`INSERT INTO solicitacoes(solicitante_id,aberto_por,para_quem,setor,catalogo_id,item,especificacao,quantidade,justificativa,urgencia,situacao,criado_em,atualizado_em)
		VALUES(?,?,?,?,?,?,?,?,?,?,'solicitada',?,?)`, nullID(n.SolicitanteID), nullID(n.AbertoPor), limitar(n.ParaQuem, 120), n.Setor, nullID(n.CatalogoID),
		n.Item, limitar(n.Especificacao, 1000), n.Quantidade, n.Justificativa, n.Urgencia, t, t)
	if err != nil {
		return 0, err
	}
	id, _ := x.LastInsertId()
	b.EventoSol(id, autor, "criada", fmt.Sprintf("%d × %s (urgência %s)", n.Quantidade, n.Item, strings.ToLower(Solicitacao{Urgencia: n.Urgencia}.RotuloUrgencia())))
	return id, nil
}

// Validar: a T.I confere a especificação e escolhe o gestor (ou, só o Admin, dispensa com motivo).
func (b *Banco) ValidarSol(s *Solicitacao, por Admin, espec, obs string, qtd int, gestorID int64, dispensa string) error {
	if s.Situacao != "solicitada" {
		return errors.New("esta solicitação não está aguardando a T.I")
	}
	if qtd < 1 || qtd > 500 {
		return errors.New("quantidade inválida")
	}
	espec, obs, dispensa = limitar(espec, 1000), limitar(obs, 1000), limitar(dispensa, 300)
	if strings.TrimSpace(espec) == "" {
		return errors.New("informe a especificação que o Suprimentos deve comprar")
	}
	situacao := "aguardando_gestor"
	if dispensa != "" {
		if !por.EhAdmin() {
			return errors.New("só um administrador dispensa a autorização do gestor")
		}
		situacao, gestorID = "em_compra", 0
	} else {
		g, err := b.AdminPorID(gestorID)
		if err != nil || !b.temPoder(*g, PoderAutorizar) {
			return errors.New("escolha um gestor que pode autorizar compras (Administração → Perfis e permissões)")
		}
	}
	_, err := b.db.Exec(`UPDATE solicitacoes SET situacao=?, especificacao=?, quantidade=?, ti_obs=?, ti_por=?, ti_em=?, gestor_id=?, dispensa_gestor=?, atualizado_em=? WHERE id=? AND situacao='solicitada'`,
		situacao, espec, qtd, obs, nomeAdmin(por), agora(), nullID(gestorID), dispensa, agora(), s.ID)
	if err != nil {
		return err
	}
	if dispensa != "" {
		b.EventoSol(s.ID, nomeAdmin(por), "validada", "T.I validou e dispensou a autorização do gestor: "+dispensa)
	} else {
		g, _ := b.AdminPorID(gestorID)
		b.EventoSol(s.ID, nomeAdmin(por), "validada", "T.I validou a especificação; aguardando a autorização de "+nomeAdmin(*g))
	}
	if obs != "" {
		b.EventoSol(s.ID, nomeAdmin(por), "comentario", obs)
	}
	return nil
}

func (b *Banco) RecusarSol(s *Solicitacao, por Admin, motivo string) error {
	motivo = limitar(motivo, 500)
	if motivo == "" {
		return errors.New("informe o motivo")
	}
	if s.Situacao != "solicitada" && s.Situacao != "aguardando_gestor" {
		return errors.New("esta solicitação não pode mais ser recusada")
	}
	_, err := b.db.Exec(`UPDATE solicitacoes SET situacao='recusada', motivo_recusa=?, recusado_por=?, atualizado_em=? WHERE id=?`, motivo, nomeAdmin(por), agora(), s.ID)
	if err == nil {
		b.EventoSol(s.ID, nomeAdmin(por), "recusada", motivo)
	}
	return err
}

// podeAutorizar: o gestor escolhido (com o poder) ou um administrador.
func (b *Banco) podeAutorizar(a Admin, s *Solicitacao) bool {
	if s.Situacao != "aguardando_gestor" {
		return false
	}
	return a.EhAdmin() || (s.GestorID == a.ID && b.temPoder(a, PoderAutorizar))
}

func (b *Banco) AutorizarSol(s *Solicitacao, por Admin, autorizar bool, motivo, ip string) error {
	if !b.podeAutorizar(por, s) {
		return errors.New("só o gestor escolhido para esta solicitação pode autorizar")
	}
	if !autorizar {
		return b.RecusarSol(s, por, motivo)
	}
	_, err := b.db.Exec(`UPDATE solicitacoes SET situacao='em_compra', autorizado_por=?, autorizado_em=?, autorizado_ip=?, atualizado_em=? WHERE id=? AND situacao='aguardando_gestor'`,
		nomeAdmin(por)+" ("+por.Usuario+")", agora(), ip, agora(), s.ID)
	if err == nil {
		txt := "Compra autorizada"
		if motivo = limitar(motivo, 500); motivo != "" {
			txt += ": " + motivo
		}
		b.EventoSol(s.ID, nomeAdmin(por), "autorizada", txt)
	}
	return err
}

// DadosCompra: o que o Suprimentos registra (só comprado/não e setor são obrigatórios).
type DadosCompra struct {
	Comprado                                           bool
	SetorDestino, Fornecedor, Valor, PedidoNF, Previsao string
}

func (b *Banco) RegistrarCompra(s *Solicitacao, por Admin, d DadosCompra) error {
	if !(por.EhAdmin() || b.temPoder(por, PoderComprar)) {
		return errors.New("só o Suprimentos registra compras")
	}
	if s.Situacao != "em_compra" && s.Situacao != "comprada" && s.Situacao != "nao_comprada" {
		return errors.New("esta solicitação não está com o Suprimentos")
	}
	d.SetorDestino = limitar(d.SetorDestino, 80)
	if d.SetorDestino == "" {
		return errors.New("informe para qual setor foi")
	}
	v, err := normalizarValor(d.Valor)
	if err != nil {
		return err
	}
	if d.Previsao != "" && !reDataHora.MatchString(d.Previsao) {
		return errors.New("previsão de entrega inválida")
	}
	situacao := "nao_comprada"
	if d.Comprado {
		situacao = "comprada"
	}
	_, err = b.db.Exec(`UPDATE solicitacoes SET situacao=?, comprado=?, setor_destino=?, fornecedor=?, valor=?, pedido_nf=?, previsao=?, compra_por=?, compra_em=?, atualizado_em=? WHERE id=?`,
		situacao, d.Comprado, d.SetorDestino, limitar(d.Fornecedor, 120), v, limitar(d.PedidoNF, 60), d.Previsao, nomeAdmin(por), agora(), agora(), s.ID)
	if err != nil {
		return err
	}
	txt := "Não comprada"
	if d.Comprado {
		txt = "Comprada"
	}
	txt += " · destino: " + d.SetorDestino
	if d.Fornecedor != "" {
		txt += " · fornecedor: " + limitar(d.Fornecedor, 120)
	}
	if v != "" {
		txt += " · valor: " + formatarReais(v)
	}
	if d.PedidoNF != "" {
		txt += " · pedido/NF: " + d.PedidoNF
	}
	if d.Previsao != "" {
		txt += " · previsão: " + formatarDataHoraCampo(d.Previsao)
	}
	b.EventoSol(s.ID, nomeAdmin(por), "compra", txt)
	return nil
}

func (b *Banco) EntregarSol(s *Solicitacao, por Admin, ativoID int64, obs string) error {
	if s.Situacao != "comprada" {
		return errors.New("só uma solicitação comprada pode ser entregue")
	}
	_, err := b.db.Exec(`UPDATE solicitacoes SET situacao='entregue', entregue_por=?, entregue_em=?, ativo_id=?, atualizado_em=? WHERE id=?`,
		nomeAdmin(por), agora(), nullID(ativoID), agora(), s.ID)
	if err == nil {
		txt := "Equipamento entregue ao solicitante"
		if obs = limitar(obs, 500); obs != "" {
			txt += ": " + obs
		}
		b.EventoSol(s.ID, nomeAdmin(por), "entregue", txt)
	}
	return err
}

func (b *Banco) CancelarSol(s *Solicitacao, por Admin, motivo string) error {
	if s.SolicitanteID != por.ID && s.AbertoPor != por.ID && !equipeTI(por) {
		return errors.New("só quem pediu pode cancelar")
	}
	if s.Situacao != "solicitada" && s.Situacao != "aguardando_gestor" && s.Situacao != "em_compra" {
		return errors.New("depois de comprada, a solicitação não pode ser cancelada")
	}
	_, err := b.db.Exec(`UPDATE solicitacoes SET situacao='cancelada', atualizado_em=? WHERE id=?`, agora(), s.ID)
	if err == nil {
		b.EventoSol(s.ID, nomeAdmin(por), "cancelada", vazioPadrao(limitar(motivo, 300), "cancelada pelo solicitante"))
	}
	return err
}

// ResumoSolicitacoes: números para o Início e os Relatórios (sem SLA).
type ResumoSol struct {
	Abertas, AguardandoTI, AguardandoGestor, ComSuprimentos, Compradas int
	EntreguesPeriodo, CriadasPeriodo                                   int
}

func (b *Banco) ResumoSolicitacoes(desde time.Time) ResumoSol {
	var r ResumoSol
	rows, err := b.db.Query(`SELECT situacao, criado_em, entregue_em FROM solicitacoes`)
	if err != nil {
		return r
	}
	defer rows.Close()
	d := desde.UTC().Format(time.RFC3339)
	for rows.Next() {
		var sit, cr, en string
		if rows.Scan(&sit, &cr, &en) != nil {
			continue
		}
		switch sit {
		case "solicitada":
			r.AguardandoTI++
		case "aguardando_gestor":
			r.AguardandoGestor++
		case "em_compra":
			r.ComSuprimentos++
		case "comprada":
			r.Compradas++
		}
		if cr >= d {
			r.CriadasPeriodo++
		}
		if en != "" && en >= d {
			r.EntreguesPeriodo++
		}
	}
	r.Abertas = r.AguardandoTI + r.AguardandoGestor + r.ComSuprimentos + r.Compradas
	return r
}

// pendentesPara: quantas esperam uma ação desta pessoa (badge do menu).
func (b *Banco) pendentesSolPara(a Admin) int {
	n := 0
	for _, s := range b.TodasSolicitacoes(500) {
		switch {
		case equipeTI(a) && (s.Situacao == "solicitada" || s.Situacao == "comprada"):
			n++
		case s.Situacao == "aguardando_gestor" && s.GestorID == a.ID && b.temPoder(a, PoderAutorizar):
			n++
		case s.Situacao == "em_compra" && b.temPoder(a, PoderComprar) && !a.EhAdmin():
			n++
		}
	}
	return n
}

func atoi64(s string) int64 { n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64); return n }

// CriadoEmTempo: instante da criação (para os resumos).
func (s Solicitacao) CriadoEmTempo() time.Time {
	t, _ := parseData(s.CriadoEm)
	return t
}
