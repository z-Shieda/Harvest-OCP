package servidor

// Telas do editor de termos (1.23, fase 2): Termos → Modelos e Termos → Documentos.

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (s *Servidor) paginaModelos(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	s.render(w, r, "termos_modelos", "Modelos de termo", "termos_modelos", sess, map[string]any{
		"Tipos": s.b.TiposTermo(false), "Documentos": s.b.Documentos(), "Vinculos": opcoesVinculo(),
	})
}

func tipoDoForm(r *http.Request) TipoTermo {
	return TipoTermo{
		Nome: r.FormValue("nome"), Abrangencia: r.FormValue("abrangencia"), Vinculos: r.Form["vinculos"],
		AbreChamado: r.FormValue("abre_chamado") == "1", CategoriaChamado: r.FormValue("categoria_chamado"),
		TemDevolucao: r.FormValue("tem_devolucao") == "1", ExigeCodigo: r.FormValue("exige_codigo") == "1",
		ExigePropriaMaquina: r.FormValue("exige_propria_maquina") == "1", EnviaRH: r.FormValue("envia_rh") == "1",
		Ativo: r.FormValue("ativo") == "1",
	}
}

func (s *Servidor) acaoNovoModelo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	_ = r.ParseForm()
	t := tipoDoForm(r)
	t.Ativo, t.ExigeCodigo, t.EnviaRH = true, true, true
	id, err := s.b.SalvarTipo(t)
	if err != nil {
		redirecionar(w, r, "/admin/termos/modelos", "", err)
		return
	}
	v, err := s.b.AbrirRascunho(id, sess.Admin.Usuario)
	if err == nil {
		_, _ = s.b.db.Exec(`UPDATE termo_versoes SET sigla=?, corpo=?, campos_def=?, aceites_def=? WHERE id=?`, "v1.0",
			"# "+strings.ToUpper(t.Nome)+"\n\n| **Nome** | {colaborador_nome} | **CPF** | {colaborador_cpf} |\n\nEscreva aqui o texto do termo.\n\n{aceites}\n\n## REGISTRO DE EVIDÊNCIAS DO ACEITE\n\n{evidencias}\n",
			"colaborador_nome | Nome | auto | auto\ncolaborador_cpf | CPF | auto | auto\n", "Li e aceito este termo.\n", v.ID)
	}
	s.b.Auditar(sess.Admin.Usuario, "termos: criar modelo", t.Nome)
	redirecionar(w, r, fmt.Sprintf("/admin/termos/modelos/%d", id), "Modelo criado. Escreva o texto, gere a prévia e publique.", err)
}

func (s *Servidor) paginaModelo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, err := s.b.TipoTermo(idDaURL(r))
	if err != nil {
		redirecionar(w, r, "/admin/termos/modelos", "", errors.New("modelo não encontrado"))
		return
	}
	editar := t.Rascunho
	if editar == nil {
		editar = t.Publicada
	}
	var avisos []string
	var erroModelo string
	if editar != nil {
		if a, err := ConferirModelo(editar.Corpo, editar.CamposDef, editar.AceitesDef, s.b.chavesDocumentos()); err != nil {
			erroModelo = err.Error()
		} else {
			avisos = a
		}
	}
	var catalogo []opcao
	for k, v := range camposAuto {
		catalogo = append(catalogo, opcao{k, v})
	}
	ordenarOpcoes(catalogo)
	s.render(w, r, "termos_modelo", t.Nome, "termos_modelos", sess, map[string]any{
		"T": t, "V": editar, "Versoes": s.b.VersoesDoTipo(t.ID), "Vinculos": opcoesVinculo(), "Avisos": avisos, "ErroModelo": erroModelo,
		"Catalogo": catalogo, "Pessoas": s.b.Colaboradores("", ""), "Maquinas": s.maquinasParaSelecao(), "Ativos": s.b.AtivosManuais(true),
		"Documentos": s.b.Documentos(), "Categorias": CategoriasChamado,
		"QtdAssinados": s.b.QtdTermosDaVersaoPublicada(t.ID),
	})
}

func ordenarOpcoes(l []opcao) {
	for i := 1; i < len(l); i++ {
		for j := i; j > 0 && l[j].Valor < l[j-1].Valor; j-- {
			l[j], l[j-1] = l[j-1], l[j]
		}
	}
}

// maquinasParaSelecao: máquinas do inventário (sem as ignoradas) para listas de escolha.
func (s *Servidor) maquinasParaSelecao() []*Maquina {
	l, _ := s.b.Maquinas("")
	return contaveis(l)
}

func (s *Servidor) acaoRegrasModelo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	_ = r.ParseForm()
	t := tipoDoForm(r)
	t.ID = idDaURL(r)
	_, err := s.b.SalvarTipo(t)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "termos: regras do modelo", fmt.Sprintf("#%d %s", t.ID, t.Nome))
	}
	redirecionar(w, r, fmt.Sprintf("/admin/termos/modelos/%d", t.ID), "Regras salvas.", err)
}

func (s *Servidor) acaoRascunhoModelo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	voltar := fmt.Sprintf("/admin/termos/modelos/%d#editor", id)
	if r.FormValue("descartar") == "1" {
		if v := s.b.rascunhoDoTipo(id); v != nil {
			_ = s.b.DescartarRascunho(v.ID)
			s.b.Auditar(sess.Admin.Usuario, "termos: descartar rascunho", v.Sigla)
		}
		redirecionar(w, r, voltar, "Rascunho descartado.", nil)
		return
	}
	v, err := s.b.AbrirRascunho(id, sess.Admin.Usuario)
	if err != nil {
		redirecionar(w, r, voltar, "", err)
		return
	}
	if r.FormValue("abrir") == "1" {
		redirecionar(w, r, voltar, "Rascunho aberto a partir da versão publicada.", nil)
		return
	}
	avisos, err := s.b.SalvarRascunho(v.ID, r.FormValue("sigla"), r.FormValue("corpo"), r.FormValue("campos"), r.FormValue("aceites"),
		r.FormValue("uso_interno"), r.FormValue("exige_reassinatura") == "1")
	if err != nil {
		redirecionar(w, r, voltar, "", fmt.Errorf("rascunho NÃO salvo: %v", err))
		return
	}
	msg := "Rascunho salvo."
	if len(avisos) > 0 {
		msg += " Atenção: " + strings.Join(avisos, "; ") + "."
	}
	redirecionar(w, r, voltar, msg, nil)
}

// previaModelo gera o PDF de uma versão com uma pessoa e um equipamento reais (sem gravar nada).
func (s *Servidor) previaModelo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, err := s.b.TipoTermo(idDaURL(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	v := t.Rascunho
	if vid := parseID(r.URL.Query().Get("versao")); vid > 0 {
		if x, err := s.b.Versao(vid); err == nil && x.TipoID == t.ID {
			v = x
		}
	}
	if v == nil {
		v = t.Publicada
	}
	if v == nil {
		http.Error(w, "o modelo não tem texto", http.StatusBadRequest)
		return
	}
	if _, err := ConferirModelo(v.Corpo, v.CamposDef, v.AceitesDef, s.b.chavesDocumentos()); err != nil {
		http.Error(w, "o modelo tem erro: "+err.Error(), http.StatusBadRequest)
		return
	}
	vals := novosValores()
	var f *Ficha
	if pid := parseID(r.URL.Query().Get("pessoa")); pid > 0 {
		f, _ = s.b.Ficha(pid)
	}
	eq := s.equipamentoDaURL(r.URL.Query().Get("equipamento"))
	s.b.valoresAuto(&vals, f, eq)
	vals.Texto["termo_numero"] = "0000"
	vals.Texto["termo_versao"] = v.Sigla
	vals.Texto["termo_emitido_em"] = dataHoraBrasilia(agora())
	vals.Texto["entregue_por"] = nomeAdmin(sess.Admin)
	m := novaMontagem(v.CamposDef, v.AceitesDef, vals)
	m.previa = true
	m.devolucao = r.URL.Query().Get("devolucao") == "1"
	bs := m.resolver(v.Corpo)
	if v.Situacao == "rascunho" {
		s.b.MarcarPrevia(v.ID)
	}
	mc := s.marca()
	p := paraPDF(bs, CabecalhoPDF{Logo: s.logoPDF(), NomeMarca: mc.Nome, CorSec: hexPDF(mc.CorSec), CorPri: hexPDF(mc.CorPri),
		Direita: "Prévia · " + v.Sigla, Rodape: "Prévia gerada por " + sess.Admin.Usuario, Titulo: t.Nome, Previa: true})
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="previa-`+strings.ReplaceAll(v.Sigla, `"`, "")+`.pdf"`)
	_, _ = w.Write(p)
}

// equipamentoDaURL: "m123" = máquina 123; "a7" = ativo manual 7.
func (s *Servidor) equipamentoDaURL(v string) *Equipamento {
	if len(v) < 2 {
		return nil
	}
	id, _ := strconv.ParseInt(v[1:], 10, 64)
	switch v[0] {
	case 'm':
		e, _ := s.b.EquipamentoMaquina(id)
		return e
	case 'a':
		e, _ := s.b.EquipamentoAtivo(id)
		return e
	}
	return nil
}

func (s *Servidor) acaoPublicarModelo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	t, err := s.b.TipoTermo(idDaURL(r))
	if err != nil || t.Rascunho == nil {
		redirecionar(w, r, "/admin/termos/modelos", "", errors.New("não há rascunho para publicar"))
		return
	}
	voltar := fmt.Sprintf("/admin/termos/modelos/%d", t.ID)
	v, err := s.b.PublicarVersao(t.Rascunho.ID, sess.Admin.Usuario)
	if err != nil {
		redirecionar(w, r, voltar+"#editor", "", err)
		return
	}
	detalhe := v.Sigla + " (correção, sem reassinatura)"
	msg := "Versão " + v.Sigla + " publicada. Os próximos termos já saem com ela; os assinados continuam valendo."
	if v.ExigeReassinatura {
		n := s.reemitirPorRevisao(r, t, v, sess)
		detalhe = fmt.Sprintf("%s (exige reassinatura: %d termo(s) reemitido(s))", v.Sigla, n)
		msg = fmt.Sprintf("Versão %s publicada com reassinatura: %d termo(s) voltaram para \"aguardando\" e as pessoas foram avisadas.", v.Sigla, n)
	}
	s.b.Auditar(sess.Admin.Usuario, "termos: publicar versão", t.Nome+" "+detalhe)
	redirecionar(w, r, voltar, msg, nil)
}

func (s *Servidor) paginaVersao(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	v, err := s.b.Versao(idDaURL(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	t, _ := s.b.TipoTermo(v.TipoID)
	m := novaMontagem(v.CamposDef, v.AceitesDef, novosValores())
	m.previa, m.devolucao = true, true
	s.render(w, r, "termos_versao", t.Nome+" · "+v.Sigla, "termos_modelos", sess, map[string]any{
		"T": t, "V": v, "Campos": v.Campos(), "HTML": paraHTML(m.resolver(v.Corpo), s.b.titulosDocumentos()),
	})
}

// ---------------- documentos de referência ----------------

func (s *Servidor) paginaDocumentos(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	var editar *DocumentoRef
	if k := r.URL.Query().Get("editar"); k != "" && sess.Admin.EhAdmin() {
		editar, _ = s.b.Documento(k)
	}
	s.render(w, r, "termos_documentos", "Documentos de referência", "termos_documentos", sess, map[string]any{
		"Lista": s.b.Documentos(), "Editar": editar, "Novo": r.URL.Query().Get("novo") == "1",
	})
}

func (s *Servidor) acaoDocumento(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	chave := strings.ToLower(strings.TrimSpace(r.FormValue("chave")))
	err := s.b.SalvarDocumento(chave, r.FormValue("titulo"), r.FormValue("versao"), r.FormValue("corpo"), sess.Admin.Usuario)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "termos: salvar documento", chave+" "+r.FormValue("versao"))
	}
	redirecionar(w, r, "/admin/termos/documentos", "Documento salvo.", err)
}

// lerDocumento: qualquer pessoa logada lê os documentos de referência (a PSI é lida antes do aceite).
func (s *Servidor) lerDocumento(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	d, err := s.b.Documento(r.PathValue("chave"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	m := novaMontagem("", "", novosValores())
	m.previa = true
	s.render(w, r, "termos_documento", d.Titulo, "minha_ficha", sess, map[string]any{
		"Doc": d, "HTML": paraHTML(m.resolver(d.Corpo), nil),
	})
}
