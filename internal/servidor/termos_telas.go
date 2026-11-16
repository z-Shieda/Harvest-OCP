package servidor

// Telas da emissão e assinatura (1.23, fase 3).
//   T.I:          /termos (painel) · /termos/novo · /termos/{id} · /termos/{id}/preencher · /termos/ativos
//   Colaborador:  /meus-termos · /meus-termos/{id}/assinar · /meus-termos/{id}/pdf
//   Pública:      /verificar/{codigo}

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---------------- painel ----------------

func (s *Servidor) paginaTermosPainel(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	cont := s.b.ContagemColaboradores()
	todos := s.b.Termos(FiltroTermos{Limite: 5000})
	c := s.b.ConfigTermos()
	por := map[string]int{}
	var aguardando, atrasados, rascunhos, recentes, desatualizados []*Termo
	publicadas := map[int64]int64{}
	for _, tp := range s.b.TiposTermo(false) {
		if tp.Publicada != nil {
			publicadas[tp.ID] = tp.Publicada.ID
		}
	}
	for _, t := range todos {
		por[t.Situacao]++
		switch t.Situacao {
		case "aguardando":
			aguardando = append(aguardando, t)
			if t.AguardandoDias() >= c.AtencaoDias {
				atrasados = append(atrasados, t)
			}
		case "rascunho":
			rascunhos = append(rascunhos, t)
		case "assinado":
			if len(recentes) < 10 {
				recentes = append(recentes, t)
			}
			if publicadas[t.TipoID] != 0 && publicadas[t.TipoID] != t.VersaoID {
				desatualizados = append(desatualizados, t)
			}
		}
	}
	s.render(w, r, "termos", "Termos", "termos", sess, map[string]any{
		"Contagem": cont, "Por": por, "AValidar": s.b.FichasAValidar(), "Aguardando": aguardando, "Atrasados": atrasados,
		"Rascunhos": rascunhos, "Recentes": recentes, "Desatualizados": desatualizados,
		"SemTermo": s.maquinasSemTermo(), "ErrosFila": s.b.QtdEnviosComErro(), "Config": c, "Empresas": len(s.b.Empresas(true)),
		"Tipos": s.b.TiposTermo(true), "Expirados": s.b.Colaboradores("", "convite_expirado"),
	})
}

// maquinasSemTermo: máquinas ativas (contato em 30 dias, não ignoradas) sem termo de entrega assinado.
func (s *Servidor) maquinasSemTermo() []*Maquina {
	var tipoEntrega int64
	for _, tp := range s.b.TiposTermo(true) {
		if tp.PorEquipamento() {
			tipoEntrega = tp.ID
			break
		}
	}
	if tipoEntrega == 0 {
		return nil
	}
	com := map[int64]bool{}
	if rows, err := s.b.db.Query(`SELECT maquina_id FROM termos WHERE tipo_id=? AND maquina_id IS NOT NULL AND situacao IN ('assinado','em_devolucao')`, tipoEntrega); err == nil {
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				com[id] = true
			}
		}
		rows.Close()
	}
	limite := time.Now().AddDate(0, 0, -30).UTC().Format(time.RFC3339)
	var l []*Maquina
	for _, m := range s.maquinasParaSelecao() {
		if !com[m.ID] && m.UltimoContato >= limite {
			l = append(l, m)
		}
	}
	return l
}

// ---------------- emissão ----------------

func (s *Servidor) paginaNovoTermo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	q := r.URL.Query()
	s.render(w, r, "termos_novo", "Emitir termo", "termos_novo", sess, map[string]any{
		"Tipos": s.b.TiposTermo(true), "Pessoas": s.b.Colaboradores("", "validada"), "Maquinas": s.maquinasParaSelecao(),
		"Ativos": s.b.AtivosManuais(true), "TipoSel": parseID(q.Get("tipo")), "PessoaSel": parseID(q.Get("pessoa")), "EquipSel": q.Get("equipamento"),
	})
}

func (s *Servidor) acaoNovoTermo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	p := PedidoEmissao{TipoID: parseID(r.FormValue("tipo")), AdminID: parseID(r.FormValue("pessoa")), Equipamento: r.FormValue("equipamento")}
	t, err := s.Emitir(p, sess.Admin)
	if err != nil {
		redirecionar(w, r, fmt.Sprintf("/termos/novo?tipo=%d&pessoa=%d&equipamento=%s", p.TipoID, p.AdminID, p.Equipamento), "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "termos: emitir", fmt.Sprintf("nº %s %s para %s %s", t.NumeroTexto(), t.Sigla, t.Pessoa, t.Equipamento))
	redirecionar(w, r, fmt.Sprintf("/termos/%d/preencher", t.ID), "Termo nº "+t.NumeroTexto()+" emitido. Preencha o que cabe à T.I e envie ao colaborador.", nil)
}

// acaoEmitirLote: emite e envia um termo "por pessoa" (ex.: Anexo II) a todas as fichas validadas que ainda não têm.
func (s *Servidor) acaoEmitirLote(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	tp, err := s.b.TipoTermo(parseID(r.FormValue("tipo")))
	if err != nil || tp.PorEquipamento() {
		redirecionar(w, r, "/termos", "", errors.New("escolha um termo por pessoa"))
		return
	}
	setor := strings.TrimSpace(r.FormValue("setor"))
	emitidos, enviados := 0, 0
	var problemas []string
	for _, f := range s.b.Colaboradores("", "validada") {
		if setor != "" && !strings.EqualFold(f.Setor, setor) {
			continue
		}
		if _, _, _, _, err := s.ConferirEmissao(PedidoEmissao{TipoID: tp.ID, AdminID: f.AdminID}); err != nil {
			continue // já tem, vínculo não permitido etc.
		}
		t, err := s.Emitir(PedidoEmissao{TipoID: tp.ID, AdminID: f.AdminID}, sess.Admin)
		if err != nil {
			problemas = append(problemas, f.NomeExibicao()+": "+err.Error())
			continue
		}
		emitidos++
		if err := s.EnviarAoColaborador(t, sess.Admin, s.baseURL(r)+fmt.Sprintf("/meus-termos/%d/assinar", t.ID)); err != nil {
			problemas = append(problemas, f.NomeExibicao()+": "+err.Error())
			continue
		}
		enviados++
	}
	s.b.Auditar(sess.Admin.Usuario, "termos: emitir em lote", fmt.Sprintf("%s · setor %s · %d emitido(s), %d enviado(s)", tp.Nome, vazioTraco(setor), emitidos, enviados))
	msg := fmt.Sprintf("%s: %d termo(s) emitido(s) e %d enviado(s) para assinatura.", tp.Nome, emitidos, enviados)
	var e error
	if len(problemas) > 0 {
		e = errors.New(msg + " Problemas: " + strings.Join(problemas, " | "))
	}
	redirecionar(w, r, "/termos", msg, e)
}

// ---------------- detalhe e preenchimento (T.I) ----------------

func (s *Servidor) carregarTermo(w http.ResponseWriter, r *http.Request) (*Termo, *VersaoTermo, *TipoTermo, bool) {
	t, err := s.b.Termo(idDaURL(r))
	if err != nil {
		redirecionar(w, r, "/termos", "", errors.New("termo não encontrado"))
		return nil, nil, nil, false
	}
	v, err1 := s.b.Versao(t.VersaoID)
	tp, err2 := s.b.TipoTermo(t.TipoID)
	if err1 != nil || err2 != nil {
		redirecionar(w, r, "/termos", "", errors.New("modelo do termo não encontrado"))
		return nil, nil, nil, false
	}
	return t, v, tp, true
}

func (s *Servidor) paginaTermo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, v, tp, ok := s.carregarTermo(w, r)
	if !ok {
		return
	}
	m := s.montagemDoTermo(t, v)
	m.previa = t.Situacao == "rascunho" || t.Situacao == "aguardando"
	s.render(w, r, "termo", "Termo nº "+t.NumeroTexto(), "termos", sess, map[string]any{
		"T": t, "V": v, "Tipo": tp, "HTML": paraHTML(m.resolver(v.Corpo), s.b.titulosDocumentos()), "Assinaturas": s.b.AssinaturasDoTermo(t.ID),
		"Envios": s.b.enviosDoTermo(t.ID), "Pendencias": s.PendenciasEnvio(t, v), "Fotos": s.b.fotosDoChamado(t.ChamadoID, ""),
		"LinkAssinar": s.baseURL(r) + fmt.Sprintf("/meus-termos/%d/assinar", t.ID),
	})
}

func (s *Servidor) paginaPreencher(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, v, tp, ok := s.carregarTermo(w, r)
	if !ok {
		return
	}
	if t.Situacao != "rascunho" {
		redirecionar(w, r, fmt.Sprintf("/termos/%d", t.ID), "", errors.New("o termo já foi enviado ao colaborador"))
		return
	}
	s.atualizarAuto(t)
	// checklist de segurança pré-marcada pelo agente (só onde a T.I ainda não marcou)
	sug := s.sugestaoSeguranca(t)
	if _, ja := t.Valores.Checklists["seguranca"]; !ja && len(sug) > 0 {
		t.Valores.Checklists["seguranca"] = sug
	}
	var anexos []Anexo
	if t.ChamadoID > 0 {
		anexos, _ = s.b.AnexosChamado(t.ChamadoID)
	}
	s.render(w, r, "termos_preencher", "Preencher termo nº "+t.NumeroTexto(), "termos", sess, map[string]any{
		"T": t, "V": v, "Tipo": tp, "Campos": v.CamposDe("ti"), "Sugestao": sug, "Pendencias": s.PendenciasEnvio(t, v),
		"Anexos": anexos, "Fotos": s.b.fotosDoChamado(t.ChamadoID, ""), "Valores": t.Valores,
		"MaxAnexos": maxAnexosPorEnvio, "MaxAnexoMB": maxAnexo >> 20,
	})
}

func (s *Servidor) acaoPreencher(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, v, _, ok := s.carregarTermo(w, r)
	if !ok {
		return
	}
	voltar := fmt.Sprintf("/termos/%d/preencher", t.ID)
	if t.Situacao != "rascunho" {
		redirecionar(w, r, fmt.Sprintf("/termos/%d", t.ID), "", errors.New("o termo já foi enviado"))
		return
	}
	if err := LerValoresForm(v.Campos(), "ti", &t.Valores, r.FormValue); err != nil {
		redirecionar(w, r, voltar, "", err)
		return
	}
	s.atualizarAuto(t)
	if err := s.b.gravarValores(t.ID, t.Valores); err != nil {
		redirecionar(w, r, voltar, "", err)
		return
	}
	// fotos enviadas junto vão para o chamado
	if t.ChamadoID > 0 {
		arqs, err := lerAnexos(r)
		if err != nil {
			redirecionar(w, r, voltar, "Os campos foram salvos, mas as fotos não.", err)
			return
		}
		s.salvarAnexos(t.ChamadoID, 0, arqs, nomeAdmin(sess.Admin))
	}
	if r.FormValue("acao") == "enviar" {
		if err := s.EnviarAoColaborador(t, sess.Admin, s.baseURL(r)+fmt.Sprintf("/meus-termos/%d/assinar", t.ID)); err != nil {
			redirecionar(w, r, voltar, "Os dados foram salvos, mas o termo NÃO foi enviado.", err)
			return
		}
		s.b.Auditar(sess.Admin.Usuario, "termos: enviar ao colaborador", "nº "+t.NumeroTexto()+" para "+t.Pessoa)
		redirecionar(w, r, fmt.Sprintf("/termos/%d", t.ID), "Termo enviado: "+t.Pessoa+" recebeu o aviso por e-mail e o termo aparece em Meus termos.", nil)
		return
	}
	redirecionar(w, r, voltar, "Preenchimento salvo.", nil)
}

func (s *Servidor) acaoCancelarTermo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, _, _, ok := s.carregarTermo(w, r)
	if !ok {
		return
	}
	err := s.Cancelar(t, r.FormValue("motivo"), sess.Admin)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "termos: cancelar", "nº "+t.NumeroTexto()+": "+r.FormValue("motivo"))
	}
	redirecionar(w, r, fmt.Sprintf("/termos/%d", t.ID), "Termo cancelado.", err)
}

func (s *Servidor) acaoLiberarMaquina(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, _, _, ok := s.carregarTermo(w, r)
	if !ok {
		return
	}
	err := s.LiberarMaquina(t, r.FormValue("motivo"), sess.Admin)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "termos: liberar assinatura fora da própria máquina", "nº "+t.NumeroTexto()+": "+r.FormValue("motivo"))
	}
	redirecionar(w, r, fmt.Sprintf("/termos/%d", t.ID), "Liberado: a pessoa pode assinar de outra máquina.", err)
}

func (s *Servidor) acaoReenviarAviso(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, _, _, ok := s.carregarTermo(w, r)
	if !ok {
		return
	}
	if t.Situacao != "aguardando" {
		redirecionar(w, r, fmt.Sprintf("/termos/%d", t.ID), "", errors.New("o termo não está aguardando assinatura"))
		return
	}
	s.avisarTermoParaAssinar(t, s.baseURL(r)+fmt.Sprintf("/meus-termos/%d/assinar", t.ID), "lembrete")
	_, _ = s.b.db.Exec(`UPDATE termos SET lembrete_em=? WHERE id=?`, agora(), t.ID)
	s.b.Auditar(sess.Admin.Usuario, "termos: reenviar aviso", "nº "+t.NumeroTexto())
	redirecionar(w, r, fmt.Sprintf("/termos/%d", t.ID), "Aviso reenviado para "+t.Email+".", nil)
}

func (b *Banco) enviosDoTermo(termoID int64) []EnvioFila {
	var l []EnvioFila
	for _, e := range b.EnviosFila(200, 0) {
		if e.TermoID == termoID {
			l = append(l, e)
		}
	}
	return l
}

// baixarPDFTermo: PDF assinado (gravado na assinatura) ou, antes dela, uma prévia.
func (s *Servidor) baixarPDFTermo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, err := s.b.Termo(idDaURL(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if t.AdminID != sess.Admin.ID && !sess.Admin.PodeInventario() {
		http.Error(w, "sem permissão", http.StatusForbidden)
		return
	}
	v, err := s.b.Versao(t.VersaoID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, _, err := s.b.PDFDaAssinatura(t.ID)
	if err != nil || len(p) == 0 {
		m := s.montagemDoTermo(t, v)
		m.previa = true
		p = s.pdfDoTermo(t, v, m, true)
	}
	disp := "inline"
	if r.URL.Query().Get("baixar") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", disp+`; filename="`+nomeArquivoTermo(t, v.Sigla)+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(p)
}

// ---------------- colaborador ----------------

func (s *Servidor) paginaMeusTermos(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	var pend, outros []*Termo
	for _, t := range s.b.Termos(FiltroTermos{AdminID: sess.Admin.ID}) {
		switch t.Situacao {
		case "aguardando":
			pend = append(pend, t)
		case "rascunho", "cancelado":
		default:
			outros = append(outros, t)
		}
	}
	s.render(w, r, "meus_termos", "Meus termos", "meus_termos", sess, map[string]any{"Pendentes": pend, "Outros": outros})
}

func (s *Servidor) paginaAssinar(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, v, tp, ok := s.carregarTermoDoColaborador(w, r, sess)
	if !ok {
		return
	}
	m := s.montagemDoTermo(t, v)
	m.previa = t.Situacao == "aguardando"
	var codigoPendente bool
	var expira string
	_ = s.b.db.QueryRow(`SELECT expira FROM termo_codigos WHERE admin_id=? AND termo_id=? AND usado=0 AND expira>=? ORDER BY id DESC LIMIT 1`,
		sess.Admin.ID, t.ID, agora()).Scan(&expira)
	codigoPendente = expira != ""
	s.render(w, r, "termos_assinar", t.TipoNome, "meus_termos", sess, map[string]any{
		"T": t, "V": v, "Tipo": tp, "HTML": paraHTML(m.resolver(v.Corpo), s.b.titulosDocumentos()), "Campos": v.CamposDe("colaborador"),
		"Aceites": v.Aceites(), "Valores": t.Valores, "CodigoPendente": codigoPendente, "CodigoExpira": expira,
		"Docs": s.b.titulosDocumentos(), "Assinaturas": s.b.AssinaturasDoTermo(t.ID), "Chamado": t.ChamadoID,
	})
}

func (s *Servidor) carregarTermoDoColaborador(w http.ResponseWriter, r *http.Request, sess *Sessao) (*Termo, *VersaoTermo, *TipoTermo, bool) {
	t, err := s.b.Termo(idDaURL(r))
	if err != nil || (t.AdminID != sess.Admin.ID) || t.Situacao == "rascunho" {
		redirecionar(w, r, "/meus-termos", "", errors.New("termo não encontrado"))
		return nil, nil, nil, false
	}
	v, err1 := s.b.Versao(t.VersaoID)
	tp, err2 := s.b.TipoTermo(t.TipoID)
	if err1 != nil || err2 != nil {
		redirecionar(w, r, "/meus-termos", "", errors.New("modelo do termo não encontrado"))
		return nil, nil, nil, false
	}
	return t, v, tp, true
}

func (s *Servidor) acaoAssinar(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, _, _, ok := s.carregarTermoDoColaborador(w, r, sess)
	if !ok {
		return
	}
	voltar := fmt.Sprintf("/meus-termos/%d/assinar", t.ID)
	ip := ipCliente(r)
	if r.FormValue("acao") == "codigo" {
		m, err := s.PedirCodigo(t, sess, ip, r.FormValue)
		if err != nil {
			redirecionar(w, r, voltar+"#assinar", "", err)
			return
		}
		redirecionar(w, r, voltar+"#assinar", "Código enviado para "+t.Email+" (vale 10 minutos). Máquina identificada: "+m.Texto()+".", nil)
		return
	}
	if s.lim.bloqueado(ip) {
		redirecionar(w, r, voltar+"#assinar", "", errors.New("muitas tentativas; aguarde 10 minutos"))
		return
	}
	err := s.Assinar(t, sess, r.FormValue("codigo"), ip, r.UserAgent(), s.baseURL(r), r.FormValue)
	if err != nil {
		if errors.Is(err, errCodigo) {
			s.lim.registrarFalha(ip)
		}
		redirecionar(w, r, voltar+"#assinar", "", err)
		return
	}
	redirecionar(w, r, "/meus-termos", "Termo assinado. A cópia em PDF foi enviada para o seu e-mail.", nil)
}

// ---------------- verificação pública ----------------

func (s *Servidor) paginaVerificar(w http.ResponseWriter, r *http.Request) {
	cod := r.PathValue("codigo")
	if cod == "" {
		cod = r.URL.Query().Get("codigo")
	}
	d := map[string]any{"Codigo": cod}
	if cod != "" {
		ip := ipCliente(r)
		if s.lim.bloqueado(ip) {
			d["Erro"] = "muitas consultas; aguarde 10 minutos"
		} else {
			v := s.b.Verificar(cod)
			if !v.Valido {
				s.lim.registrarFalha(ip)
			}
			d["V"] = v
		}
	}
	s.render(w, r, "verificar", "Verificar termo", "", nil, d)
}

// ---------------- gestão do ativo ----------------

func (s *Servidor) acaoGestaoMaquina(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	g := GestaoMaquina{Tipo: r.FormValue("tipo_equip"), Condicao: r.FormValue("condicao"), NFNumero: r.FormValue("nf_numero"),
		NFData: r.FormValue("nf_data"), Valor: r.FormValue("valor_referencia")}
	err := s.b.SalvarGestaoMaquina(id, g)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "gestão do ativo", fmt.Sprintf("máquina #%d: %s/%s NF %s valor %s", id, g.Tipo, g.Condicao, g.NFNumero, g.Valor))
	}
	redirecionar(w, r, fmt.Sprintf("/maquinas/%d#gestao", id), "Gestão do ativo salva.", err)
}

func (s *Servidor) paginaAtivos(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	var editar *AtivoManual
	if id := parseID(r.URL.Query().Get("editar")); id > 0 {
		editar, _ = s.b.AtivoManual(id)
	}
	s.render(w, r, "termos_ativos", "Ativos sem agente", "termos_ativos", sess, map[string]any{
		"Lista": s.b.AtivosManuais(false), "Editar": editar, "Tipos": tiposEquip, "Condicoes": condicoesEquip,
	})
}

func (s *Servidor) acaoAtivo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	a := AtivoManual{ID: parseID(r.FormValue("id")), Tipo: r.FormValue("tipo"), Fabricante: r.FormValue("fabricante"), Modelo: r.FormValue("modelo"),
		Serial: r.FormValue("serial"), Patrimonio: r.FormValue("patrimonio"), IMEI: r.FormValue("imei"), Linha: r.FormValue("linha"),
		Condicao: r.FormValue("condicao"), NFNumero: r.FormValue("nf_numero"), NFData: r.FormValue("nf_data"), Valor: r.FormValue("valor_referencia"),
		Obs: r.FormValue("observacoes"), Ativo: r.FormValue("ativo") == "1" || r.FormValue("id") == ""}
	id, err := s.b.SalvarAtivoManual(a, sess.Admin.Usuario)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "termos: salvar ativo manual", fmt.Sprintf("#%d %s", id, a.Tipo))
	}
	redirecionar(w, r, "/termos/ativos", "Ativo salvo.", err)
}

// dadosTermoMaquina: o que a ficha da máquina mostra sobre gestão, segurança e termo.
func (s *Servidor) dadosTermoMaquina(id int64) map[string]any {
	d := map[string]any{"Gestao": s.b.GestaoDaMaquina(id), "TiposEquip": tiposEquip, "Condicoes": condicoesEquip,
		"TermosAtivo": s.b.ConfigTermos().Ativo}
	var termos []*Termo
	for _, t := range s.b.Termos(FiltroTermos{MaquinaID: id, Limite: 20}) {
		if t.Situacao != "cancelado" {
			termos = append(termos, t)
		}
	}
	d["TermosMaquina"] = termos
	var entrega int64
	for _, tp := range s.b.TiposTermo(true) {
		if tp.PorEquipamento() {
			entrega = tp.ID
			break
		}
	}
	d["TipoEntrega"] = entrega
	return d
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
