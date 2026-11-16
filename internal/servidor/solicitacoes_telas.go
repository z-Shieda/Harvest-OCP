package servidor

// Telas e avisos das Solicitações de equipamento (1.24).

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------- avisos por e-mail ----------------

func (s *Servidor) linkSol(base string, id int64) string {
	l := "/solicitacoes/" + strconv.FormatInt(id, 10)
	if base != "" {
		return base + l
	}
	return s.linkAbsoluto(l)
}

func (s *Servidor) avisarSol(sol *Solicitacao, para []string, titulo string, linhas []string, link string) {
	var dest []string
	vistos := map[string]bool{}
	for _, p := range para {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && strings.Contains(p, "@") && !vistos[p] {
			vistos[p] = true
			dest = append(dest, p)
		}
	}
	if len(dest) == 0 {
		return
	}
	corpo := append([]string{}, linhas...)
	corpo = append(corpo, fmt.Sprintf("%s · %d × %s · setor %s", sol.Numero(), sol.Quantidade, sol.Item, sol.Setor))
	texto, html := s.emailSimples(titulo, corpo, "Abrir a solicitação", link, "Para agir é preciso entrar no Harvest com a sua conta.")
	_, _ = s.enfileirar(EnvioFila{Destino: "pessoa", Tipo: "solicitacao", Para: strings.Join(dest, ","),
		Assunto: "[" + sol.Numero() + "] " + titulo, Texto: texto, HTML: html})
}

func (s *Servidor) emailsSuprimentos() []string {
	l := s.b.emailsComPoder(PoderComprar)
	return append(l, listaConfig(s.b.Config("sol_emails_suprimentos"))...)
}

// lembretesGestor: a cada N dias, novo e-mail ao gestor que ainda não respondeu.
var ultimaVerificacaoLembrete struct {
	sync.Mutex
	em time.Time
}

func (s *Servidor) lembretesSolicitacoes() {
	ultimaVerificacaoLembrete.Lock()
	if time.Since(ultimaVerificacaoLembrete.em) < time.Hour {
		ultimaVerificacaoLembrete.Unlock()
		return
	}
	ultimaVerificacaoLembrete.em = time.Now()
	ultimaVerificacaoLembrete.Unlock()
	dias := numeroConfig(s.b.Config("sol_lembrete_dias"), 2, 0, 30)
	if dias == 0 {
		return
	}
	limite := time.Now().UTC().Add(-time.Duration(dias) * 24 * time.Hour).Format(time.RFC3339)
	for _, sol := range s.b.TodasSolicitacoes(1000) {
		if sol.Situacao != "aguardando_gestor" || sol.GestorEmail == "" {
			continue
		}
		ref := sol.TIEm
		if sol.LembreteEm > ref {
			ref = sol.LembreteEm
		}
		if ref > limite {
			continue
		}
		_, _ = s.b.db.Exec(`UPDATE solicitacoes SET lembrete_em=? WHERE id=?`, agora(), sol.ID)
		s.b.EventoSol(sol.ID, "sistema", "lembrete", "Lembrete enviado ao gestor "+sol.Gestor)
		s.avisarSol(sol, []string{sol.GestorEmail}, "Lembrete: compra aguardando a sua autorização",
			[]string{"A solicitação abaixo ainda espera a sua decisão (autorizar ou recusar)."}, s.linkSol("", sol.ID))
	}
}

// ---------------- lista ----------------

func (s *Servidor) paginaSolicitacoes(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	a := sess.Admin
	filtro := r.URL.Query().Get("ver")
	var l []*Solicitacao
	cont := map[string]int{}
	for _, sol := range s.b.TodasSolicitacoes(2000) {
		if !s.b.podeVerSol(a, sol) {
			continue
		}
		minha := sol.SolicitanteID == a.ID || sol.AbertoPor == a.ID
		autorizar := sol.Situacao == "aguardando_gestor" && s.b.podeAutorizar(a, sol)
		comprar := sol.Situacao == "em_compra" && s.b.temPoder(a, PoderComprar)
		ti := equipeTI(a) && (sol.Situacao == "solicitada" || sol.Situacao == "comprada")
		if minha {
			cont["minhas"]++
		}
		if autorizar {
			cont["autorizar"]++
		}
		if comprar {
			cont["comprar"]++
		}
		if ti {
			cont["ti"]++
		}
		if sol.Aberta() {
			cont["abertas"]++
		}
		cont["todas"]++
		ok := false
		switch filtro {
		case "minhas":
			ok = minha
		case "autorizar":
			ok = autorizar
		case "comprar":
			ok = comprar
		case "ti":
			ok = ti
		case "todas":
			ok = true
		default:
			ok = sol.Aberta()
		}
		if ok {
			l = append(l, sol)
		}
	}
	if filtro == "" {
		filtro = "abertas"
	}
	s.render(w, r, "solicitacoes", "Solicitações de equipamento", "solicitacoes", sess, map[string]any{
		"Lista": l, "Filtro": filtro, "Cont": cont, "TI": equipeTI(a), "Autoriza": s.b.temPoder(a, PoderAutorizar) && !a.EhAdmin(),
		"Compra": s.b.temPoder(a, PoderComprar),
	})
}

// ---------------- nova ----------------

func (s *Servidor) paginaNovaSolicitacao(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	setor := ""
	if f, err := s.b.Ficha(sess.Admin.ID); err == nil {
		setor = f.Setor
	}
	var pessoas []*Ficha
	if equipeTI(sess.Admin) {
		pessoas = s.b.Colaboradores("", "")
	}
	s.render(w, r, "solicitacao_nova", "Pedir equipamento", "solicitacoes", sess, map[string]any{
		"Catalogo": s.b.Catalogo(true), "Setor": setor, "Urgencias": urgencias, "Pessoas": pessoas,
		"MaxAnexos": maxAnexosPorEnvio, "MaxAnexoMB": maxAnexo >> 20,
	})
}

func (s *Servidor) acaoNovaSolicitacao(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	n := NovaSolicitacao{SolicitanteID: sess.Admin.ID, AbertoPor: sess.Admin.ID, CatalogoID: parseID(r.FormValue("catalogo_id")),
		ParaQuem: r.FormValue("para_quem"), Setor: r.FormValue("setor"), Item: r.FormValue("item"), Especificacao: r.FormValue("especificacao"),
		Justificativa: r.FormValue("justificativa"), Urgencia: r.FormValue("urgencia")}
	n.Quantidade, _ = strconv.Atoi(strings.TrimSpace(r.FormValue("quantidade")))
	if equipeTI(sess.Admin) {
		if em := parseID(r.FormValue("em_nome_de")); em > 0 {
			n.SolicitanteID = em
		}
	}
	arqs, err := lerAnexos(r)
	if err != nil {
		redirecionar(w, r, "/solicitacoes/nova", "", err)
		return
	}
	id, err := s.b.CriarSolicitacao(n, nomeAdmin(sess.Admin))
	if err != nil {
		redirecionar(w, r, "/solicitacoes/nova", "", err)
		return
	}
	s.b.salvarAnexosSol(id, arqs, nomeAdmin(sess.Admin))
	sol, _ := s.b.Solicitacao(id)
	s.b.Auditar(sess.Admin.Usuario, "solicitação: nova", sol.Numero()+" "+sol.Item)
	base := s.baseURL(r)
	s.avisarSol(sol, s.emailsEquipe(sess.Admin.ID), "Nova solicitação de equipamento",
		[]string{sol.Solicitante + " pediu um equipamento. A T.I precisa validar a especificação e escolher o gestor que autoriza."}, s.linkSol(base, id))
	s.avisarSol(sol, []string{sol.SolicitanteEmail}, "Recebemos a sua solicitação",
		[]string{"A T.I vai conferir o pedido e encaminhar para a autorização do gestor. Você recebe um e-mail a cada etapa."}, s.linkSol(base, id))
	redirecionar(w, r, fmt.Sprintf("/solicitacoes/%d", id), "Solicitação "+sol.Numero()+" enviada para a T.I.", nil)
}

// ---------------- detalhe e ações ----------------

func (s *Servidor) carregarSol(w http.ResponseWriter, r *http.Request, sess *Sessao) (*Solicitacao, bool) {
	sol, err := s.b.Solicitacao(idDaURL(r))
	if err != nil || !s.b.podeVerSol(sess.Admin, sol) {
		redirecionar(w, r, "/solicitacoes", "", errors.New("solicitação não encontrada"))
		return nil, false
	}
	return sol, true
}

func (s *Servidor) paginaSolicitacao(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	sol, ok := s.carregarSol(w, r, sess)
	if !ok {
		return
	}
	a := sess.Admin
	sugerido := sol.GestorID
	if sugerido == 0 {
		sugerido = s.b.GestorDoSetor(sol.Setor)
	}
	setores := s.b.SetoresFixos()
	s.render(w, r, "solicitacao", sol.Numero(), "solicitacoes", sess, map[string]any{
		"S": sol, "Eventos": s.b.EventosSol(sol.ID), "Anexos": s.b.AnexosSol(sol.ID),
		"TI": equipeTI(a), "EhAdmin": a.EhAdmin(), "PodeAutorizar": s.b.podeAutorizar(a, sol),
		"PodeComprar": (a.EhAdmin() || s.b.temPoder(a, PoderComprar)) && (sol.Situacao == "em_compra" || sol.Situacao == "comprada" || sol.Situacao == "nao_comprada"),
		"PodeCancelar": (sol.SolicitanteID == a.ID || sol.AbertoPor == a.ID || equipeTI(a)) && (sol.Situacao == "solicitada" || sol.Situacao == "aguardando_gestor" || sol.Situacao == "em_compra"),
		"Gestores": s.b.GestoresQueAutorizam(), "Sugerido": sugerido, "Setores": setores, "TermosAtivo": s.b.ConfigTermos().Ativo,
		"TiposEquip": tiposEquip, "MaxAnexos": maxAnexosPorEnvio, "MaxAnexoMB": maxAnexo >> 20,
		"Etapas": []string{"Pedido", "T.I", "Gestor", "Suprimentos", "Entrega"},
	})
}

func (s *Servidor) acaoValidarSol(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	sol, ok := s.carregarSol(w, r, sess)
	if !ok {
		return
	}
	volta := fmt.Sprintf("/solicitacoes/%d", sol.ID)
	if !equipeTI(sess.Admin) {
		redirecionar(w, r, volta, "", errors.New("só a T.I valida solicitações"))
		return
	}
	if r.FormValue("acao") == "recusar" {
		if err := s.b.RecusarSol(sol, sess.Admin, r.FormValue("motivo")); err != nil {
			redirecionar(w, r, volta, "", err)
			return
		}
		s.b.Auditar(sess.Admin.Usuario, "solicitação: recusada pela T.I", sol.Numero())
		s.avisarSol(sol, []string{sol.SolicitanteEmail}, "Solicitação recusada pela T.I", []string{"Motivo: " + r.FormValue("motivo")}, s.linkSol(s.baseURL(r), sol.ID))
		redirecionar(w, r, volta, "Solicitação recusada; o solicitante foi avisado.", nil)
		return
	}
	qtd, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("quantidade")))
	err := s.b.ValidarSol(sol, sess.Admin, r.FormValue("especificacao"), r.FormValue("obs"), qtd, parseID(r.FormValue("gestor_id")), r.FormValue("dispensa"))
	if err != nil {
		redirecionar(w, r, volta, "", err)
		return
	}
	sol, _ = s.b.Solicitacao(sol.ID)
	s.b.Auditar(sess.Admin.Usuario, "solicitação: validada", sol.Numero()+" gestor "+vazioTraco(sol.Gestor))
	link := s.linkSol(s.baseURL(r), sol.ID)
	msg := "Validada. "
	if sol.Situacao == "em_compra" {
		s.avisarSol(sol, s.emailsSuprimentos(), "Compra liberada pela T.I", []string{"A T.I validou esta compra e dispensou a autorização do gestor (" + sol.DispensaGestor + ")."}, link)
		msg += "Enviada direto ao Suprimentos."
	} else {
		s.avisarSol(sol, []string{sol.GestorEmail}, "Compra aguardando a sua autorização",
			[]string{"A T.I validou o pedido abaixo e precisa da sua autorização para a compra.", "Especificação: " + sol.Especificacao, "Justificativa: " + sol.Justificativa}, link)
		msg += sol.Gestor + " foi avisado para autorizar."
	}
	s.avisarSol(sol, []string{sol.SolicitanteEmail}, "Sua solicitação foi validada pela T.I", []string{"Agora ela segue para a autorização da compra."}, link)
	redirecionar(w, r, volta, msg, nil)
}

func (s *Servidor) acaoAutorizarSol(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	sol, ok := s.carregarSol(w, r, sess)
	if !ok {
		return
	}
	volta := fmt.Sprintf("/solicitacoes/%d", sol.ID)
	autorizar := r.FormValue("decisao") == "autorizar"
	if err := s.b.AutorizarSol(sol, sess.Admin, autorizar, r.FormValue("motivo"), ipCliente(r)); err != nil {
		redirecionar(w, r, volta, "", err)
		return
	}
	link := s.linkSol(s.baseURL(r), sol.ID)
	if autorizar {
		s.b.Auditar(sess.Admin.Usuario, "solicitação: compra autorizada", sol.Numero())
		s.avisarSol(sol, s.emailsSuprimentos(), "Compra autorizada — pode comprar",
			[]string{"A T.I validou e " + nomeAdmin(sess.Admin) + " autorizou a compra abaixo.", "Especificação: " + sol.Especificacao,
				"Ao comprar (ou se não for comprar), registre no Harvest e informe para qual setor foi."}, link)
		s.avisarSol(sol, []string{sol.SolicitanteEmail}, "Compra autorizada", []string{nomeAdmin(sess.Admin) + " autorizou a compra. Agora está com o Suprimentos."}, link)
		redirecionar(w, r, volta, "Compra autorizada; o Suprimentos foi avisado.", nil)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "solicitação: compra recusada pelo gestor", sol.Numero())
	s.avisarSol(sol, append(s.emailsEquipe(0), sol.SolicitanteEmail), "Compra não autorizada", []string{nomeAdmin(sess.Admin) + " não autorizou a compra. Motivo: " + r.FormValue("motivo")}, link)
	redirecionar(w, r, volta, "Compra recusada; a T.I e o solicitante foram avisados.", nil)
}

func (s *Servidor) acaoCompraSol(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	sol, ok := s.carregarSol(w, r, sess)
	if !ok {
		return
	}
	volta := fmt.Sprintf("/solicitacoes/%d", sol.ID)
	d := DadosCompra{Comprado: r.FormValue("comprado") == "sim", SetorDestino: r.FormValue("setor_destino"), Fornecedor: r.FormValue("fornecedor"),
		Valor: r.FormValue("valor"), PedidoNF: r.FormValue("pedido_nf"), Previsao: r.FormValue("previsao")}
	if r.FormValue("comprado") == "" {
		redirecionar(w, r, volta, "", errors.New("marque se foi comprado ou não"))
		return
	}
	arqs, err := lerAnexos(r)
	if err != nil {
		redirecionar(w, r, volta, "", err)
		return
	}
	if err := s.b.RegistrarCompra(sol, sess.Admin, d); err != nil {
		redirecionar(w, r, volta, "", err)
		return
	}
	s.b.salvarAnexosSol(sol.ID, arqs, nomeAdmin(sess.Admin))
	sol, _ = s.b.Solicitacao(sol.ID)
	s.b.Auditar(sess.Admin.Usuario, "solicitação: compra registrada", sol.Numero()+" "+sol.RotuloSituacao())
	link := s.linkSol(s.baseURL(r), sol.ID)
	titulo := "Equipamento comprado"
	linha := "O Suprimentos registrou a compra (destino: " + sol.SetorDestino + ")."
	if !d.Comprado {
		titulo, linha = "Equipamento não comprado", "O Suprimentos registrou que esta compra não será feita (destino informado: "+sol.SetorDestino+")."
	}
	if sol.Previsao != "" {
		linha += " Previsão de entrega: " + formatarDataHoraCampo(sol.Previsao) + "."
	}
	s.avisarSol(sol, s.emailsEquipe(0), titulo, []string{linha}, link)
	s.avisarSol(sol, []string{sol.SolicitanteEmail}, titulo, []string{linha}, link)
	redirecionar(w, r, volta, "Compra registrada; a T.I e o solicitante foram avisados.", nil)
}

func (s *Servidor) acaoEntregarSol(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	sol, ok := s.carregarSol(w, r, sess)
	if !ok {
		return
	}
	volta := fmt.Sprintf("/solicitacoes/%d", sol.ID)
	if !equipeTI(sess.Admin) {
		redirecionar(w, r, volta, "", errors.New("só a T.I registra a entrega"))
		return
	}
	var ativoID int64
	if r.FormValue("criar_ativo") == "1" {
		nf := sol.PedidoNF
		id, err := s.b.SalvarAtivoManual(AtivoManual{Tipo: r.FormValue("tipo"), Fabricante: r.FormValue("fabricante"), Modelo: r.FormValue("modelo"),
			Serial: r.FormValue("serial"), Patrimonio: r.FormValue("patrimonio"), Condicao: "novo", NFNumero: nf, Valor: sol.Valor,
			Obs: "Comprado pela solicitação " + sol.Numero() + vazioPadrao(prefixo(" · fornecedor ", sol.Fornecedor), "")}, sess.Admin.Usuario)
		if err != nil {
			redirecionar(w, r, volta, "", fmt.Errorf("ativo não cadastrado: %v", err))
			return
		}
		ativoID = id
	}
	if err := s.b.EntregarSol(sol, sess.Admin, ativoID, r.FormValue("obs")); err != nil {
		redirecionar(w, r, volta, "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "solicitação: entregue", sol.Numero())
	s.avisarSol(sol, []string{sol.SolicitanteEmail}, "Equipamento entregue", []string{"A T.I registrou a entrega do equipamento. Qualquer problema, abra um chamado."}, s.linkSol(s.baseURL(r), sol.ID))
	if r.FormValue("emitir_termo") == "1" && s.b.ConfigTermos().Ativo {
		eq := ""
		if ativoID > 0 {
			eq = fmt.Sprintf("a%d", ativoID)
		}
		redirecionar(w, r, fmt.Sprintf("/termos/novo?pessoa=%d&equipamento=%s", sol.SolicitanteID, eq), "Entrega registrada. Agora emita o Termo de Entrega.", nil)
		return
	}
	redirecionar(w, r, volta, "Entrega registrada; o solicitante foi avisado.", nil)
}

func (s *Servidor) acaoComentarSol(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	sol, ok := s.carregarSol(w, r, sess)
	if !ok {
		return
	}
	volta := fmt.Sprintf("/solicitacoes/%d", sol.ID)
	arqs, err := lerAnexos(r)
	if err != nil {
		redirecionar(w, r, volta, "", err)
		return
	}
	txt := limitar(r.FormValue("texto"), 3000)
	if txt == "" && len(arqs) == 0 {
		redirecionar(w, r, volta, "", errors.New("escreva um comentário ou anexe um arquivo"))
		return
	}
	if txt != "" {
		s.b.EventoSol(sol.ID, nomeAdmin(sess.Admin), "comentario", txt)
	}
	s.b.salvarAnexosSol(sol.ID, arqs, nomeAdmin(sess.Admin))
	redirecionar(w, r, volta, "Comentário registrado.", nil)
}

func (s *Servidor) acaoCancelarSol(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	sol, ok := s.carregarSol(w, r, sess)
	if !ok {
		return
	}
	volta := fmt.Sprintf("/solicitacoes/%d", sol.ID)
	if err := s.b.CancelarSol(sol, sess.Admin, r.FormValue("motivo")); err != nil {
		redirecionar(w, r, volta, "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "solicitação: cancelada", sol.Numero())
	var para []string
	if sol.Situacao == "aguardando_gestor" {
		para = append(para, sol.GestorEmail)
	}
	if sol.Situacao == "em_compra" {
		para = append(para, s.emailsSuprimentos()...)
	}
	s.avisarSol(sol, append(para, s.emailsEquipe(sess.Admin.ID)...), "Solicitação cancelada", []string{"O solicitante cancelou o pedido abaixo."}, s.linkSol(s.baseURL(r), sol.ID))
	redirecionar(w, r, volta, "Solicitação cancelada.", nil)
}

func (s *Servidor) baixarAnexoSol(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	sol, ok := s.carregarSol(w, r, sess)
	if !ok {
		return
	}
	a, dados, err := s.b.AnexoSol(sol.ID, parseID(r.PathValue("aid")))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	servirAnexo(w, r, a, dados)
}

// ---------------- administração ----------------

func (s *Servidor) paginaAdminSolicitacoes(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	var editar *ItemCatalogo
	if id := parseID(r.URL.Query().Get("editar")); id > 0 {
		editar, _ = s.b.ItemCatalogo(id)
	}
	s.render(w, r, "solicitacoes_admin", "Configurar solicitações", "solicitacoes_admin", sess, map[string]any{
		"Catalogo": s.b.Catalogo(false), "Editar": editar, "EmailsSup": strings.Join(listaConfig(s.b.Config("sol_emails_suprimentos")), "\n"),
		"Lembrete": numeroConfig(s.b.Config("sol_lembrete_dias"), 2, 0, 30), "Suprimentos": s.b.emailsComPoder(PoderComprar),
		"Gestores": s.b.GestoresQueAutorizam(),
	})
}

func (s *Servidor) acaoAdminSolicitacoes(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	if r.FormValue("acao") == "config" {
		emails := listaConfig(r.FormValue("emails_suprimentos"))
		for _, e := range emails {
			if !emailValido(e) {
				redirecionar(w, r, "/admin/solicitacoes", "", fmt.Errorf("e-mail inválido: %s", e))
				return
			}
		}
		_ = s.b.DefinirConfig("sol_emails_suprimentos", strings.Join(emails, ", "))
		_ = s.b.DefinirConfig("sol_lembrete_dias", strconv.Itoa(numeroConfig(r.FormValue("lembrete_dias"), 2, 0, 30)))
		s.b.Auditar(sess.Admin.Usuario, "solicitações: configurar", strings.Join(emails, ","))
		redirecionar(w, r, "/admin/solicitacoes", "Configuração salva.", nil)
		return
	}
	c := ItemCatalogo{ID: parseID(r.FormValue("id")), Nome: r.FormValue("nome"), Categoria: r.FormValue("categoria"),
		Especificacao: r.FormValue("especificacao"), Ativo: r.FormValue("ativo") == "1" || r.FormValue("id") == ""}
	err := s.b.SalvarItemCatalogo(c)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "solicitações: catálogo", c.Nome)
	}
	redirecionar(w, r, "/admin/solicitacoes#catalogo", "Item do catálogo salvo.", err)
}
