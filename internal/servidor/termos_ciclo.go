package servidor

// Ciclo de vida de um termo (1.23, fase 3):
//   rascunho (T.I preenchendo) → aguardando (enviado ao colaborador) → assinado
//   → em_devolucao → encerrado (fase 4). Fora da linha: substituido e cancelado.
// Emitir um termo por equipamento abre o chamado "Entrega de equipamento" (D12).

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Termo struct {
	ID, Numero                       int64
	TipoID, VersaoID, AdminID        int64
	MaquinaID, AtivoID, ChamadoID    int64
	Equipamento                      string
	Situacao                         string
	Valores                          ValoresTermo
	CriadoPor, CriadoEm              string
	EnviadoPor, EnviadoEm            string
	AssinadoEm                       string
	SubstituidoPor, Substitui        int64
	CanceladoEm, CanceladoPor, Motivo string
	LiberarMaquinaMotivo             string
	LiberarMaquinaPor                string
	LembreteEm                       string
	// leitura (joins)
	TipoNome, Sigla, Pessoa, Email   string
}

func (t Termo) NumeroTexto() string { return fmt.Sprintf("%04d", t.Numero) }

func (t Termo) RotuloSituacao() string { return rotuloSituacaoTermo(t.Situacao) }

func rotuloSituacaoTermo(s string) string {
	switch s {
	case "rascunho":
		return "Rascunho (T.I preenchendo)"
	case "aguardando":
		return "Aguardando assinatura"
	case "assinado":
		return "Assinado"
	case "em_devolucao":
		return "Em devolução"
	case "encerrado":
		return "Encerrado"
	case "substituido":
		return "Substituído"
	case "cancelado":
		return "Cancelado"
	}
	return s
}

func (t Termo) CorSituacao() string {
	switch t.Situacao {
	case "assinado", "encerrado":
		return "verde"
	case "aguardando", "rascunho", "em_devolucao":
		return "amarelo"
	}
	return ""
}

// Ativo: o termo ainda conta (bloqueia outro igual para a mesma pessoa/equipamento).
func (t Termo) Ativo() bool {
	return t.Situacao == "rascunho" || t.Situacao == "aguardando" || t.Situacao == "assinado" || t.Situacao == "em_devolucao"
}

// AguardandoDias: há quantos dias está esperando a assinatura.
func (t Termo) AguardandoDias() int {
	if t.Situacao != "aguardando" {
		return 0
	}
	e, ok := parseData(t.EnviadoEm)
	if !ok {
		return 0
	}
	return int(time.Since(e).Hours() / 24)
}

const colunasTermo = `t.id, t.numero, t.tipo_id, t.versao_id, t.admin_id, COALESCE(t.maquina_id,0), COALESCE(t.ativo_id,0), COALESCE(t.chamado_id,0),
	t.equipamento, t.situacao, t.valores_json, t.criado_por, t.criado_em, t.enviado_por, t.enviado_em, t.assinado_em,
	COALESCE(t.substituido_por,0), COALESCE(t.substitui,0), t.cancelado_em, t.cancelado_por, t.motivo, t.liberar_maquina_motivo, t.liberar_maquina_por, t.lembrete_em,
	tp.nome, v.sigla, COALESCE(NULLIF(a.nome,''), a.usuario), COALESCE(NULLIF(a.email,''), a.usuario)`

const fromTermo = ` FROM termos t JOIN termo_tipos tp ON tp.id=t.tipo_id JOIN termo_versoes v ON v.id=t.versao_id JOIN admins a ON a.id=t.admin_id`

func lerTermo(sc interface{ Scan(...any) error }) (*Termo, error) {
	var t Termo
	var vj string
	err := sc.Scan(&t.ID, &t.Numero, &t.TipoID, &t.VersaoID, &t.AdminID, &t.MaquinaID, &t.AtivoID, &t.ChamadoID, &t.Equipamento, &t.Situacao, &vj,
		&t.CriadoPor, &t.CriadoEm, &t.EnviadoPor, &t.EnviadoEm, &t.AssinadoEm, &t.SubstituidoPor, &t.Substitui, &t.CanceladoEm, &t.CanceladoPor,
		&t.Motivo, &t.LiberarMaquinaMotivo, &t.LiberarMaquinaPor, &t.LembreteEm, &t.TipoNome, &t.Sigla, &t.Pessoa, &t.Email)
	if err != nil {
		return nil, err
	}
	t.Valores = novosValores()
	_ = json.Unmarshal([]byte(vj), &t.Valores)
	if t.Valores.Texto == nil {
		t.Valores.Texto = map[string]string{}
	}
	if t.Valores.Checklists == nil {
		t.Valores.Checklists = map[string]map[string]ValorChecklist{}
	}
	return &t, nil
}

func (b *Banco) Termo(id int64) (*Termo, error) {
	return lerTermo(b.db.QueryRow(`SELECT `+colunasTermo+fromTermo+` WHERE t.id=?`, id))
}

// FiltroTermos para as listas.
type FiltroTermos struct {
	Situacao  string
	AdminID   int64
	MaquinaID int64
	TipoID    int64
	Busca     string
	Limite    int
}

func (b *Banco) Termos(f FiltroTermos) []*Termo {
	q := `SELECT ` + colunasTermo + fromTermo + ` WHERE 1=1`
	var args []any
	if f.Situacao == "ativos" {
		q += ` AND t.situacao IN ('rascunho','aguardando','assinado','em_devolucao')`
	} else if f.Situacao != "" {
		q += ` AND t.situacao=?`
		args = append(args, f.Situacao)
	}
	if f.AdminID > 0 {
		q += ` AND t.admin_id=?`
		args = append(args, f.AdminID)
	}
	if f.MaquinaID > 0 {
		q += ` AND t.maquina_id=?`
		args = append(args, f.MaquinaID)
	}
	if f.TipoID > 0 {
		q += ` AND t.tipo_id=?`
		args = append(args, f.TipoID)
	}
	if f.Busca != "" {
		q += ` AND (a.nome LIKE ? OR a.usuario LIKE ? OR t.equipamento LIKE ? OR CAST(t.numero AS TEXT)=?)`
		x := "%" + f.Busca + "%"
		args = append(args, x, x, x, strings.TrimLeft(f.Busca, "0"))
	}
	if f.Limite <= 0 {
		f.Limite = 500
	}
	q += ` ORDER BY t.id DESC LIMIT ?`
	args = append(args, f.Limite)
	rows, err := b.db.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []*Termo
	for rows.Next() {
		if t, err := lerTermo(rows); err == nil {
			l = append(l, t)
		}
	}
	return l
}

func (b *Banco) gravarValores(id int64, v ValoresTermo) error {
	j, _ := json.Marshal(v)
	_, err := b.db.Exec(`UPDATE termos SET valores_json=? WHERE id=?`, string(j), id)
	return err
}

// QtdTermosDaVersaoPublicada: termos assinados do tipo (os que uma revisão com reassinatura reemite).
func (b *Banco) QtdTermosDaVersaoPublicada(tipoID int64) int {
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM termos WHERE tipo_id=? AND situacao IN ('assinado','aguardando')`, tipoID).Scan(&n)
	return n
}

// ---------------- emissão ----------------

// PedidoEmissao: o que a T.I escolhe em /termos/novo.
type PedidoEmissao struct {
	TipoID      int64
	AdminID     int64  // colaborador
	Equipamento string // "m123" (máquina) ou "a7" (ativo manual)
}

// ConferirEmissao valida as regras (ficha validada, vínculo, um responsável por equipamento).
func (s *Servidor) ConferirEmissao(p PedidoEmissao) (*TipoTermo, *VersaoTermo, *Ficha, *Equipamento, error) {
	tp, err := s.b.TipoTermo(p.TipoID)
	if err != nil || !tp.Ativo {
		return nil, nil, nil, nil, errors.New("escolha um tipo de termo ativo")
	}
	v := tp.Publicada
	if v == nil {
		return nil, nil, nil, nil, errors.New("esse tipo de termo ainda não tem versão publicada")
	}
	f, err := s.b.Ficha(p.AdminID)
	if err != nil {
		return nil, nil, nil, nil, errors.New("escolha o colaborador")
	}
	if !f.Ativo {
		return nil, nil, nil, nil, errors.New("a conta dessa pessoa está desativada")
	}
	if !f.Validada() {
		return nil, nil, nil, nil, errors.New("a ficha de " + f.NomeExibicao() + " ainda não foi validada pela T.I (Colaboradores)")
	}
	if !tp.AceitaVinculo(f.Vinculo) {
		msg := "o vínculo " + f.RotuloVinculo() + " não pode assinar este termo"
		if f.Vinculo == "pj" {
			msg += " (para PJ use contrato de comodato)"
		}
		return nil, nil, nil, nil, errors.New(msg)
	}
	var eq *Equipamento
	if tp.PorEquipamento() {
		eq = s.equipamentoDaURL(p.Equipamento)
		if eq == nil {
			return nil, nil, nil, nil, errors.New("escolha o equipamento")
		}
		for _, t := range s.b.termosAtivosDoEquipamento(tp.ID, eq) {
			if t.AdminID == f.AdminID {
				return nil, nil, nil, nil, fmt.Errorf("já existe o termo nº %s (%s) para essa pessoa e esse equipamento", t.NumeroTexto(), t.RotuloSituacao())
			}
			return nil, nil, nil, nil, fmt.Errorf("o equipamento já está com %s (termo nº %s, %s). Faça a devolução ou cancele aquele termo antes (um responsável por equipamento)",
				t.Pessoa, t.NumeroTexto(), strings.ToLower(t.RotuloSituacao()))
		}
	} else {
		for _, t := range s.b.Termos(FiltroTermos{Situacao: "ativos", AdminID: f.AdminID, TipoID: tp.ID}) {
			return nil, nil, nil, nil, fmt.Errorf("%s já tem o termo nº %s (%s)", f.NomeExibicao(), t.NumeroTexto(), strings.ToLower(t.RotuloSituacao()))
		}
	}
	return tp, v, f, eq, nil
}

func (b *Banco) termosAtivosDoEquipamento(tipoID int64, eq *Equipamento) []*Termo {
	q := `SELECT ` + colunasTermo + fromTermo + ` WHERE t.tipo_id=? AND t.situacao IN ('rascunho','aguardando','assinado','em_devolucao') AND `
	var arg int64
	if eq.MaquinaID > 0 {
		q += `t.maquina_id=?`
		arg = eq.MaquinaID
	} else {
		q += `t.ativo_id=?`
		arg = eq.AtivoID
	}
	rows, err := b.db.Query(q, tipoID, arg)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []*Termo
	for rows.Next() {
		if t, err := lerTermo(rows); err == nil {
			l = append(l, t)
		}
	}
	return l
}

// Emitir cria o termo (rascunho) e, se o tipo pede, o chamado de entrega.
func (s *Servidor) Emitir(p PedidoEmissao, por Admin) (*Termo, error) {
	tp, v, f, eq, err := s.ConferirEmissao(p)
	if err != nil {
		return nil, err
	}
	vals := novosValores()
	s.b.valoresAuto(&vals, f, eq)
	var numero int64
	_ = s.b.db.QueryRow(`SELECT COALESCE(MAX(numero),0)+1 FROM termos`).Scan(&numero)
	vals.Texto["termo_numero"] = fmt.Sprintf("%04d", numero)
	vals.Texto["termo_versao"] = v.Sigla
	vals.Texto["termo_emitido_em"] = dataHoraBrasilia(agora())
	var maqID, ativoID int64
	nomeEq := ""
	if eq != nil {
		maqID, ativoID, nomeEq = eq.MaquinaID, eq.AtivoID, eq.Nome
	}
	j, _ := json.Marshal(vals)
	x, err := s.b.db.Exec(`INSERT INTO termos(numero,tipo_id,versao_id,admin_id,maquina_id,ativo_id,equipamento,situacao,valores_json,criado_por,criado_em)
		VALUES(?,?,?,?,?,?,?,'rascunho',?,?,?)`, numero, tp.ID, v.ID, f.AdminID, nullID(maqID), nullID(ativoID), nomeEq, string(j), por.Usuario, agora())
	if err != nil {
		return nil, err
	}
	id, _ := x.LastInsertId()
	if tp.AbreChamado {
		titulo := tp.CategoriaChamado + " — " + vazioPadrao(nomeEq, "equipamento") + " — " + f.NomeExibicao()
		cid, _, err := s.b.CriarChamado(NovoChamado{Titulo: titulo, Categoria: tp.CategoriaChamado, Prioridade: "normal", CriadoPor: por.ID, AtribuidoA: por.ID,
			MaquinaID: maqID, SolicitanteNome: f.NomeExibicao(), SolicitanteEmail: f.Email, SolicitanteSetor: f.Setor, Origem: "termo",
			Descricao: fmt.Sprintf("Termo nº %04d (%s) para %s.\nAnexe aqui as fotos do equipamento: tampa, tela ligada, teclado e base com etiqueta de série/patrimônio.\nAtenção (LGPD): nas fotos da tela ligada, não deixe aparecer dados de pacientes.", numero, v.Sigla, f.NomeExibicao())}, nomeAdmin(por))
		if err == nil {
			_, _ = s.b.db.Exec(`UPDATE termos SET chamado_id=? WHERE id=?`, cid, id)
			vals.Texto["chamado_numero"] = strconv.FormatInt(cid, 10)
			_ = s.b.gravarValores(id, vals)
			s.b.RegistrarEvento(cid, nomeAdmin(por), "termo", fmt.Sprintf("Termo nº %04d (%s) emitido para %s", numero, v.Sigla, f.NomeExibicao()))
		}
	}
	return s.b.Termo(id)
}

// sugestaoSeguranca pré-marca a checklist "seguranca" com o que o agente 1.22 detectou (D11).
func (s *Servidor) sugestaoSeguranca(t *Termo) map[string]ValorChecklist {
	m := map[string]ValorChecklist{}
	if t.MaquinaID == 0 {
		return m
	}
	g := s.b.GestaoDaMaquina(t.MaquinaID)
	sim := func(item string, ok bool, obs string) {
		v := "Não"
		if ok {
			v = "Sim"
		}
		m[item] = ValorChecklist{V: v, Obs: obs}
	}
	if g.BitLocker != "" {
		sim("Criptografia do disco ativa (BitLocker)", g.BitLocker == "ativo", "agente: "+g.BitLocker)
	}
	if g.AntivirusOK != "" {
		sim("Antivírus / proteção ativa e atualizada", g.AntivirusOK == "sim", "agente: "+g.Antivirus)
	}
	sim("Agente de inventário instalado (integrado ao Harvest)", true, "agente instalado")
	if mq, err := s.b.Maquina(t.MaquinaID); err == nil && mq.MonitorSituacao != "" {
		sim("Agente de monitoramento WorkMonitor instalado (PSI – Anexo II)", mq.MonitorSituacao == "ok" || mq.MonitorSituacao == "rodando", "agente: "+rotuloMonitor(mq.MonitorSituacao))
	}
	if g.AdminsLocais != "" || g.SegColetadoEm != "" {
		login := strings.ToLower(chaveCurta(s.loginRedeDe(t.AdminID)))
		ehAdmin := false
		for _, a := range strings.Split(g.AdminsLocais, ",") {
			if login != "" && strings.ToLower(chaveCurta(a)) == login {
				ehAdmin = true
			}
		}
		if login != "" {
			sim("Usuário sem privilégio de administrador", !ehAdmin, "admins locais: "+vazioTraco(g.AdminsLocais))
		}
	}
	if g.UltimaAtualizacao != "" {
		if u, ok := parseData(g.UltimaAtualizacao); ok {
			sim("Sistema operacional atualizado", time.Since(u) < 45*24*time.Hour, "última atualização: "+dataBrasilia(g.UltimaAtualizacao))
		}
	}
	return m
}

func (s *Servidor) loginRedeDe(adminID int64) string {
	var l string
	_ = s.b.db.QueryRow(`SELECT login_rede FROM admins WHERE id=?`, adminID).Scan(&l)
	return l
}

// fotosDoChamado conta as imagens anexadas ao chamado (antes de um instante, se informado).
func (b *Banco) fotosDoChamado(chamadoID int64, ate string) int {
	if chamadoID == 0 {
		return 0
	}
	q := `SELECT COUNT(*) FROM chamado_anexos WHERE chamado_id=? AND tipo LIKE 'image/%'`
	args := []any{chamadoID}
	if ate != "" {
		q += ` AND criado_em<=?`
		args = append(args, ate)
	}
	var n int
	_ = b.db.QueryRow(q, args...).Scan(&n)
	return n
}

// PendenciasEnvio: o que falta para enviar ao colaborador (auto + T.I + fotos).
func (s *Servidor) PendenciasEnvio(t *Termo, v *VersaoTermo) []string {
	campos := v.Campos()
	fotos := s.b.fotosDoChamado(t.ChamadoID, "")
	var l []string
	l = append(l, Pendencias(campos, t.Valores, "auto", fotos)...)
	l = append(l, Pendencias(campos, t.Valores, "ti", fotos)...)
	return l
}

// atualizarAuto recopia os campos automáticos (a T.I pode ter corrigido o cadastro depois da emissão).
func (s *Servidor) atualizarAuto(t *Termo) {
	f, _ := s.b.Ficha(t.AdminID)
	var eq *Equipamento
	if t.MaquinaID > 0 {
		eq, _ = s.b.EquipamentoMaquina(t.MaquinaID)
	} else if t.AtivoID > 0 {
		eq, _ = s.b.EquipamentoAtivo(t.AtivoID)
	}
	s.b.valoresAuto(&t.Valores, f, eq)
}

// EnviarAoColaborador: rascunho → aguardando, com aviso por e-mail.
func (s *Servidor) EnviarAoColaborador(t *Termo, por Admin, link string) error {
	if t.Situacao != "rascunho" {
		return errors.New("só um termo em rascunho pode ser enviado")
	}
	v, err := s.b.Versao(t.VersaoID)
	if err != nil {
		return err
	}
	if f, err := s.b.Ficha(t.AdminID); err != nil || !f.Validada() || !f.Ativo {
		return errors.New("a ficha da pessoa precisa estar validada e a conta ativa")
	}
	s.atualizarAuto(t)
	agoraT := agora()
	t.Valores.Texto["entregue_por"] = nomeAdmin(por) + " (" + por.Usuario + ")"
	t.Valores.Texto["entregue_em"] = dataHoraBrasilia(agoraT)
	fotos := s.b.fotosDoChamado(t.ChamadoID, "")
	for _, c := range v.Campos() {
		if c.Tipo == "fotos" && c.Quem == "ti" {
			t.Valores.Texto[c.Chave] = strconv.Itoa(fotos)
		}
	}
	if p := s.PendenciasEnvio(t, v); len(p) > 0 {
		_ = s.b.gravarValores(t.ID, t.Valores)
		return errors.New("falta: " + strings.Join(p, "; "))
	}
	if err := s.b.gravarValores(t.ID, t.Valores); err != nil {
		return err
	}
	if _, err := s.b.db.Exec(`UPDATE termos SET situacao='aguardando', enviado_por=?, enviado_em=? WHERE id=? AND situacao='rascunho'`, por.Usuario, agoraT, t.ID); err != nil {
		return err
	}
	if t.ChamadoID > 0 {
		s.b.RegistrarEvento(t.ChamadoID, nomeAdmin(por), "termo", fmt.Sprintf("Termo nº %s enviado para assinatura de %s", t.NumeroTexto(), t.Pessoa))
	}
	s.avisarTermoParaAssinar(t, link, "termo")
	return nil
}

func (s *Servidor) avisarTermoParaAssinar(t *Termo, link, tipo string) {
	titulo := "Você tem um termo para assinar"
	intro := "A T.I enviou um termo de responsabilidade para você ler e assinar no Harvest."
	if tipo == "lembrete" {
		titulo = "Lembrete: termo aguardando a sua assinatura"
		intro = "O termo abaixo ainda está esperando a sua assinatura."
	}
	if tipo == "revisao" {
		titulo = "Termo atualizado: assine a nova versão"
		intro = "O texto do termo abaixo foi revisado e a nova versão precisa da sua assinatura."
	}
	texto, corpo := s.emailSimples(titulo, []string{
		"Olá, " + primeiroNomeTexto(t.Pessoa) + ",",
		intro,
		t.TipoNome + " · nº " + t.NumeroTexto() + func() string {
			if t.Equipamento != "" {
				return " · " + t.Equipamento
			}
			return ""
		}(),
		"Para assinar você entra com o seu e-mail e senha e confirma com um código que chega neste e-mail.",
	}, "Ler e assinar", link, "")
	_, _ = s.enfileirar(EnvioFila{TermoID: t.ID, AdminID: t.AdminID, Destino: "pessoa", Tipo: tipo, Para: t.Email,
		Assunto: titulo + " — " + t.TipoNome + " nº " + t.NumeroTexto(), Texto: texto, HTML: corpo})
}

func primeiroNomeTexto(n string) string {
	if f := strings.Fields(n); len(f) > 0 {
		return f[0]
	}
	return n
}

// Cancelar: rascunho ou aguardando → cancelado (assinado só sai por devolução).
func (s *Servidor) Cancelar(t *Termo, motivo string, por Admin) error {
	motivo = limitar(motivo, 300)
	if motivo == "" {
		return errors.New("informe o motivo do cancelamento")
	}
	if t.Situacao != "rascunho" && t.Situacao != "aguardando" {
		return errors.New("só termos em rascunho ou aguardando podem ser cancelados (assinado sai pela devolução)")
	}
	_, err := s.b.db.Exec(`UPDATE termos SET situacao='cancelado', cancelado_em=?, cancelado_por=?, motivo=? WHERE id=?`, agora(), por.Usuario, motivo, t.ID)
	if err == nil && t.ChamadoID > 0 {
		s.b.RegistrarEvento(t.ChamadoID, nomeAdmin(por), "termo", "Termo nº "+t.NumeroTexto()+" cancelado: "+motivo)
	}
	return err
}

// LiberarMaquina: com "exigir assinatura na própria máquina", a T.I libera um termo específico (com motivo).
func (s *Servidor) LiberarMaquina(t *Termo, motivo string, por Admin) error {
	motivo = limitar(motivo, 300)
	if motivo == "" {
		return errors.New("informe o motivo da liberação")
	}
	_, err := s.b.db.Exec(`UPDATE termos SET liberar_maquina_motivo=?, liberar_maquina_por=? WHERE id=?`, motivo, por.Usuario, t.ID)
	return err
}

// reemitirPorRevisao: publicação com reassinatura — termos assinados/aguardando do tipo viram
// "substituido" e nasce um termo novo (mesmos valores, versão nova) aguardando a assinatura.
func (s *Servidor) reemitirPorRevisao(r *http.Request, tp *TipoTermo, v *VersaoTermo, sess *Sessao) int {
	return s.reemitir(tp, v, sess.Admin, s.baseURL(r))
}

func (s *Servidor) reemitir(tp *TipoTermo, v *VersaoTermo, por Admin, base string) int {
	n := 0
	for _, situacao := range []string{"assinado", "aguardando"} {
		for _, t := range s.b.Termos(FiltroTermos{Situacao: situacao, TipoID: tp.ID, Limite: 100000}) {
			if t.VersaoID == v.ID {
				continue
			}
			vals := t.Valores
			// limpa o que o colaborador preencheu (ele preenche de novo ao assinar a versão nova)
			for _, c := range v.Campos() {
				if c.Quem == "colaborador" {
					delete(vals.Texto, c.Chave)
					delete(vals.Checklists, c.Chave)
				}
			}
			var numero int64
			_ = s.b.db.QueryRow(`SELECT COALESCE(MAX(numero),0)+1 FROM termos`).Scan(&numero)
			vals.Texto["termo_numero"] = fmt.Sprintf("%04d", numero)
			vals.Texto["termo_versao"] = v.Sigla
			vals.Texto["termo_emitido_em"] = dataHoraBrasilia(agora())
			j, _ := json.Marshal(vals)
			x, err := s.b.db.Exec(`INSERT INTO termos(numero,tipo_id,versao_id,admin_id,maquina_id,ativo_id,equipamento,chamado_id,situacao,valores_json,criado_por,criado_em,enviado_por,enviado_em,substitui)
				VALUES(?,?,?,?,?,?,?,?,'aguardando',?,?,?,?,?,?)`, numero, tp.ID, v.ID, t.AdminID, nullID(t.MaquinaID), nullID(t.AtivoID), t.Equipamento, nullID(t.ChamadoID),
				string(j), por.Usuario, agora(), por.Usuario, agora(), t.ID)
			if err != nil {
				continue
			}
			novo, _ := x.LastInsertId()
			_, _ = s.b.db.Exec(`UPDATE termos SET situacao='substituido', substituido_por=? WHERE id=?`, novo, t.ID)
			if nt, err := s.b.Termo(novo); err == nil {
				link := "/meus-termos/" + strconv.FormatInt(novo, 10) + "/assinar"
				if base != "" {
					link = base + link
				}
				s.avisarTermoParaAssinar(nt, s.linkAbsoluto(link), "revisao")
			}
			n++
		}
	}
	return n
}

// linkAbsoluto completa um caminho com o endereço fixo do servidor (Administração), se houver.
func (s *Servidor) linkAbsoluto(caminho string) string {
	if strings.HasPrefix(caminho, "http") {
		return caminho
	}
	if fixo := strings.TrimRight(strings.TrimSpace(s.b.Config("endereco_servidor")), "/"); fixo != "" {
		return fixo + caminho
	}
	return caminho
}

// ---------------- código por e-mail ----------------

const (
	validadeCodigo      = 10 * time.Minute
	maxTentativasCodigo = 5
	maxCodigosHora      = 3
)

func hashCodigo(termoID int64, codigo string) string {
	h := sha256.Sum256([]byte(strconv.FormatInt(termoID, 10) + ":" + strings.TrimSpace(codigo)))
	return hex.EncodeToString(h[:])
}

func gerarCodigo6() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n.Int64())
}

// NovoCodigo grava um código para o termo (invalida os anteriores) e o devolve em claro.
func (b *Banco) NovoCodigo(adminID, termoID int64, ip, maquinaJSON string) (string, error) {
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM termo_codigos WHERE admin_id=? AND criado_em>=?`, adminID,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)).Scan(&n)
	if n >= maxCodigosHora {
		return "", errors.New("você já pediu 3 códigos na última hora. Aguarde um pouco e tente de novo")
	}
	_, _ = b.db.Exec(`UPDATE termo_codigos SET usado=1 WHERE admin_id=? AND termo_id=? AND usado=0`, adminID, termoID)
	c := gerarCodigo6()
	_, err := b.db.Exec(`INSERT INTO termo_codigos(admin_id,termo_id,codigo_hash,expira,criado_em,ip,maquina_json) VALUES(?,?,?,?,?,?,?)`,
		adminID, termoID, hashCodigo(termoID, c), time.Now().UTC().Add(validadeCodigo).Format(time.RFC3339), agora(), ip, maquinaJSON)
	return c, err
}

var errCodigo = errors.New("código incorreto")

// ConferirCodigo valida o código (10 min, até 5 tentativas). Devolve o JSON da identificação da máquina.
func (b *Banco) ConferirCodigo(adminID, termoID int64, codigo string) (string, error) {
	var id int64
	var h, expira, maq string
	var tent int
	err := b.db.QueryRow(`SELECT id, codigo_hash, expira, tentativas, maquina_json FROM termo_codigos WHERE admin_id=? AND termo_id=? AND usado=0 ORDER BY id DESC LIMIT 1`,
		adminID, termoID).Scan(&id, &h, &expira, &tent, &maq)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("peça um código primeiro (botão \"Enviar código\")")
	}
	if err != nil {
		return "", err
	}
	if expira < agora() {
		_, _ = b.db.Exec(`UPDATE termo_codigos SET usado=1 WHERE id=?`, id)
		return "", errors.New("o código expirou (vale 10 minutos). Peça um novo")
	}
	if tent >= maxTentativasCodigo {
		_, _ = b.db.Exec(`UPDATE termo_codigos SET usado=1 WHERE id=?`, id)
		return "", errors.New("muitas tentativas erradas. Peça um novo código")
	}
	if subtle.ConstantTimeCompare([]byte(h), []byte(hashCodigo(termoID, codigo))) != 1 {
		_, _ = b.db.Exec(`UPDATE termo_codigos SET tentativas=tentativas+1 WHERE id=?`, id)
		restam := maxTentativasCodigo - tent - 1
		if restam <= 0 {
			_, _ = b.db.Exec(`UPDATE termo_codigos SET usado=1 WHERE id=?`, id)
			return "", errors.New("código incorreto. Esse código foi bloqueado: peça um novo")
		}
		return "", fmt.Errorf("%w (restam %d tentativas)", errCodigo, restam)
	}
	_, _ = b.db.Exec(`UPDATE termo_codigos SET usado=1 WHERE id=?`, id)
	return maq, nil
}

// codigoVerificacao: derivado do hash, para conferir em /verificar (12 caracteres em 3 grupos).
func codigoVerificacao(hashHex string) string {
	c := strings.ToUpper(hashHex)
	if len(c) < 12 {
		return c
	}
	return c[0:4] + "-" + c[4:8] + "-" + c[8:12]
}

// ---------------- assinatura (gravada uma vez) ----------------

type Assinatura struct {
	ID, TermoID                    int64
	Etapa                          string
	Aceites                        []bool
	AssinadoEm, IP, UserAgent      string
	MaquinaID                      int64
	Maquina, Grau, Candidatas      string
	Metodo, EmailCodigo            string
	TextoFinal, Hash, Codigo       string
	TemPDF                         bool
}

func (a Assinatura) RotuloGrau() string { return rotuloGrau(a.Grau) }

func rotuloGrau(g string) string {
	switch g {
	case "confirmada":
		return "confirmada"
	case "provavel":
		return "provável"
	}
	return "não identificada"
}

func (b *Banco) AssinaturasDoTermo(termoID int64) []Assinatura {
	rows, err := b.db.Query(`SELECT id, termo_id, etapa, aceites_json, assinado_em, ip, user_agent, COALESCE(maquina_assinatura_id,0), maquina_assinatura,
		maquina_grau, maquinas_candidatas, metodo, email_codigo, hash_sha256, codigo_verificacao, pdf IS NOT NULL FROM termo_assinaturas WHERE termo_id=? ORDER BY id`, termoID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []Assinatura
	for rows.Next() {
		var a Assinatura
		var aj string
		if rows.Scan(&a.ID, &a.TermoID, &a.Etapa, &aj, &a.AssinadoEm, &a.IP, &a.UserAgent, &a.MaquinaID, &a.Maquina, &a.Grau, &a.Candidatas,
			&a.Metodo, &a.EmailCodigo, &a.Hash, &a.Codigo, &a.TemPDF) == nil {
			_ = json.Unmarshal([]byte(aj), &a.Aceites)
			l = append(l, a)
		}
	}
	return l
}

func (b *Banco) PDFDaAssinatura(termoID int64) ([]byte, string, error) {
	var p []byte
	var cod string
	err := b.db.QueryRow(`SELECT pdf, codigo_verificacao FROM termo_assinaturas WHERE termo_id=? AND pdf IS NOT NULL ORDER BY id DESC LIMIT 1`, termoID).Scan(&p, &cod)
	return p, cod, err
}

// VerificacaoPublica: o que /verificar mostra (só valida, data e versão — nada pessoal).
type VerificacaoPublica struct {
	Valido     bool
	Codigo     string
	AssinadoEm string
	Tipo       string
	Sigla      string
	Numero     string
	Situacao   string
	Hash       string
}

func (b *Banco) Verificar(codigo string) VerificacaoPublica {
	codigo = strings.ToUpper(strings.TrimSpace(codigo))
	v := VerificacaoPublica{Codigo: codigo}
	var numero int64
	var situacao string
	err := b.db.QueryRow(`SELECT s.assinado_em, tp.nome, vs.sigla, t.numero, t.situacao, s.hash_sha256 FROM termo_assinaturas s
		JOIN termos t ON t.id=s.termo_id JOIN termo_tipos tp ON tp.id=t.tipo_id JOIN termo_versoes vs ON vs.id=t.versao_id
		WHERE s.codigo_verificacao=?`, codigo).Scan(&v.AssinadoEm, &v.Tipo, &v.Sigla, &numero, &situacao, &v.Hash)
	if err != nil {
		return v
	}
	v.Valido = true
	v.Numero = fmt.Sprintf("%04d", numero)
	v.Situacao = rotuloSituacaoTermo(situacao)
	v.AssinadoEm = dataHoraBrasilia(v.AssinadoEm)
	return v
}
