package servidor

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestFluxoSolicitacao(t *testing.T) {
	b := bancoTeste(t)
	s, err := Novo(b, "teste")
	if err != nil {
		t.Fatal(err)
	}
	h := s.Rotas()
	_ = b.CriarAdmin("admin", "senha-admin-1", "Daniel", "ti@ocupacional.com.br", "admin")
	_ = b.CriarAdmin("tec", "senha-tec-12", "Técnico", "tec@ocupacional.com.br", "tecnico")
	_ = b.CriarAdmin("ana", "senha-ana-123", "Ana", "ana@ocupacional.com.br", "usuario")
	_ = b.CriarAdmin("wilian", "senha-wil-123", "Wilian", "wilian@ocupacional.com.br", "usuario")
	_ = b.CriarAdmin("outro", "senha-out-123", "Outro gestor", "outro@ocupacional.com.br", "usuario")
	_ = b.CriarAdmin("sup", "senha-sup-123", "Suprimentos", "suprimentos@ocupacional.com.br", "usuario")
	_ = b.CriarAdmin("colega", "senha-col-123", "Colega", "colega@ocupacional.com.br", "usuario")
	entrar := func(u, sn string) *cliente {
		c := &cliente{t: t, h: h}
		c.req("POST", "/login", url.Values{"usuario": {u}, "senha": {sn}})
		if c.cookie == "" {
			t.Fatalf("login %s", u)
		}
		return c
	}
	csrf := func(c *cliente) string { x, _ := b.SessaoPorToken(c.cookie); return x.CSRF }
	idDe := func(u string) int64 {
		var id int64
		_ = b.db.QueryRow(`SELECT id FROM admins WHERE usuario=?`, u).Scan(&id)
		return id
	}
	perfil := func(chave string) int64 {
		var id int64
		_ = b.db.QueryRow(`SELECT id FROM perfis WHERE chave=?`, chave).Scan(&id)
		return id
	}
	adm := entrar("admin", "senha-admin-1")
	// perfis: Wilian e "outro" autorizam (Financeiro / RH); sup é Suprimentos; colega é gestor só de ver (Financeiro)
	adm.req("POST", fmt.Sprintf("/admin/colaboradores/%d/perfil", idDe("wilian")), url.Values{"csrf": {csrf(adm)}, "perfil_id": {fmt.Sprint(perfil("gestor_autoriza"))}, "setores_texto": {"Financeiro"}})
	adm.req("POST", fmt.Sprintf("/admin/colaboradores/%d/perfil", idDe("outro")), url.Values{"csrf": {csrf(adm)}, "perfil_id": {fmt.Sprint(perfil("gestor_autoriza"))}, "setores_texto": {"RH"}})
	adm.req("POST", "/admin/1/papel", url.Values{"csrf": {csrf(adm)}}) // sem efeito; garante que a rota existe
	w := adm.req("POST", fmt.Sprintf("/admin/%d/papel", idDe("sup")), url.Values{"csrf": {csrf(adm)}, "papel": {fmt.Sprintf("perfil:%d", perfil("suprimentos"))}})
	semErro(t, w, "perfil suprimentos")
	adm.req("POST", fmt.Sprintf("/admin/colaboradores/%d/perfil", idDe("colega")), url.Values{"csrf": {csrf(adm)}, "perfil_id": {fmt.Sprint(perfil("gestor"))}, "setores_texto": {"Financeiro"}})
	if g := b.GestorDoSetor("financeiro"); g != idDe("wilian") {
		t.Fatalf("gestor sugerido do Financeiro: %d", g)
	}
	paginaOK(t, adm, "/admin")
	paginaOK(t, adm, "/admin/solicitacoes")

	ana, wil, outro, sup, colega, tec := entrar("ana", "senha-ana-123"), entrar("wilian", "senha-wil-123"), entrar("outro", "senha-out-123"),
		entrar("sup", "senha-sup-123"), entrar("colega", "senha-col-123"), entrar("tec", "senha-tec-12")

	// menu: Suprimentos vê Solicitações; não vê inventário
	if pg := paginaOK(t, sup, "/solicitacoes"); strings.Contains(pg, `href="/maquinas"`) {
		t.Fatal("Suprimentos não deveria ver o inventário")
	}
	// Ana pede um item do catálogo
	paginaOK(t, ana, "/solicitacoes/nova")
	cat := b.Catalogo(true)
	var teclado int64
	for _, c := range cat {
		if c.Nome == "Teclado" {
			teclado = c.ID
		}
	}
	semErro(t, ana.req("POST", "/solicitacoes/nova", url.Values{"csrf": {csrf(ana)}, "catalogo_id": {fmt.Sprint(teclado)}, "quantidade": {"2"},
		"urgencia": {"normal"}, "setor": {"Financeiro"}, "justificativa": {"teclados com teclas falhando"}}), "pedir")
	sol := b.TodasSolicitacoes(10)[0]
	if sol.Item != "Teclado" || sol.Especificacao == "" || sol.Situacao != "solicitada" {
		t.Fatalf("solicitação: %+v", sol)
	}
	url1 := fmt.Sprintf("/solicitacoes/%d", sol.ID)
	// visibilidade: colega (ver_setor Financeiro) vê; Suprimentos ainda não; outro gestor não
	paginaOK(t, colega, url1)
	if w := sup.req("GET", url1, nil); w.Code != 303 {
		t.Fatal("Suprimentos não deveria ver antes da autorização")
	}
	if w := outro.req("GET", url1, nil); w.Code != 303 {
		t.Fatal("gestor de outro setor não deveria ver")
	}
	// só a T.I valida
	if w := wil.req("POST", url1+"/validar", url.Values{"csrf": {csrf(wil)}, "especificacao": {"x"}, "quantidade": {"2"}, "gestor_id": {fmt.Sprint(idDe("wilian"))}}); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("gestor não valida")
	}
	if pg := paginaOK(t, tec, url1); !strings.Contains(pg, "Sugerido pelo setor Financeiro") {
		t.Fatal("T.I deveria ver o gestor sugerido")
	}
	semErro(t, tec.req("POST", url1+"/validar", url.Values{"csrf": {csrf(tec)}, "acao": {"validar"}, "especificacao": {"Teclado ABNT2 USB"}, "quantidade": {"2"}, "gestor_id": {fmt.Sprint(idDe("wilian"))}}), "validar")
	// e-mail ao gestor, não ao Suprimentos
	if !temEnvio(b, "wilian@ocupacional.com.br") || temEnvio(b, "suprimentos@ocupacional.com.br") {
		t.Fatal("o gestor (e só ele) deveria ser avisado")
	}
	// outro gestor não autoriza; Wilian autoriza logado
	if w := outro.req("POST", url1+"/autorizar", url.Values{"csrf": {csrf(outro)}, "decisao": {"autorizar"}}); w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("outro gestor não pode autorizar")
	}
	if pg := paginaOK(t, wil, url1); !strings.Contains(pg, "Autorizar a compra") {
		t.Fatal("Wilian deveria ver o botão de autorizar")
	}
	semErro(t, wil.req("POST", url1+"/autorizar", url.Values{"csrf": {csrf(wil)}, "decisao": {"autorizar"}}), "autorizar")
	paginaOK(t, sup, url1) // (com HTML_SAIDA, grava a tela do Suprimentos)
	if !temEnvio(b, "suprimentos@ocupacional.com.br") {
		t.Fatal("o Suprimentos deveria ser avisado depois da autorização")
	}
	// Suprimentos registra: setor obrigatório
	paginaOK(t, sup, url1)
	if w := sup.multipart(url1+"/compra", map[string]string{"csrf": csrf(sup), "comprado": "sim", "setor_destino": ""}, 0); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("setor de destino é obrigatório")
	}
	semErro(t, sup.multipart(url1+"/compra", map[string]string{"csrf": csrf(sup), "comprado": "sim", "setor_destino": "Financeiro",
		"fornecedor": "Kabum", "valor": "199,90", "pedido_nf": "NF 123", "previsao": "2026-11-30"}, 1), "compra")
	sol, _ = b.Solicitacao(sol.ID)
	if sol.Situacao != "comprada" || sol.Valor != "199.90" || len(b.AnexosSol(sol.ID)) != 1 {
		t.Fatalf("compra: %s %s", sol.Situacao, sol.Valor)
	}
	// Suprimentos não entrega; T.I entrega e cadastra o ativo com NF e valor
	if w := sup.req("POST", url1+"/entregar", url.Values{"csrf": {csrf(sup)}}); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("Suprimentos não registra entrega")
	}
	semErro(t, tec.req("POST", url1+"/entregar", url.Values{"csrf": {csrf(tec)}, "criar_ativo": {"1"}, "tipo": {"outro"}, "fabricante": {"Logitech"}, "modelo": {"K120"}, "serial": {"SN1"}}), "entregar")
	sol, _ = b.Solicitacao(sol.ID)
	a, err := b.AtivoManual(sol.AtivoID)
	if sol.Situacao != "entregue" || err != nil || a.NFNumero != "NF 123" || a.Valor != "199.90" {
		t.Fatalf("entrega/ativo: %s %+v %v", sol.Situacao, a, err)
	}
	// conta nos números (Início e Relatórios)
	if r := b.ResumoSolicitacoes(sol.CriadoEmTempo()); r.CriadasPeriodo != 1 || r.EntreguesPeriodo != 1 {
		t.Fatalf("resumo: %+v", r)
	}
	paginaOK(t, adm, "/")
	paginaOK(t, adm, "/chamados/relatorios")
	paginaOK(t, ana, url1)

	// recusa pelo gestor volta para a T.I e o solicitante
	semErro(t, ana.req("POST", "/solicitacoes/nova", url.Values{"csrf": {csrf(ana)}, "item": {"Cadeira gamer"}, "quantidade": {"1"}, "setor": {"Financeiro"}, "justificativa": {"quero uma cadeira melhor"}}), "pedir 2")
	sol2 := b.TodasSolicitacoes(10)[0]
	u2 := fmt.Sprintf("/solicitacoes/%d", sol2.ID)
	semErro(t, tec.req("POST", u2+"/validar", url.Values{"csrf": {csrf(tec)}, "acao": {"validar"}, "especificacao": {"Cadeira"}, "quantidade": {"1"}, "gestor_id": {fmt.Sprint(idDe("wilian"))}}), "validar 2")
	semErro(t, wil.req("POST", u2+"/autorizar", url.Values{"csrf": {csrf(wil)}, "decisao": {"recusar"}, "motivo": {"fora do orçamento"}}), "recusar")
	if sol2, _ = b.Solicitacao(sol2.ID); sol2.Situacao != "recusada" {
		t.Fatalf("recusa: %s", sol2.Situacao)
	}
	// técnico não dispensa o gestor; admin dispensa com motivo
	semErro(t, ana.req("POST", "/solicitacoes/nova", url.Values{"csrf": {csrf(ana)}, "item": {"Mouse"}, "quantidade": {"1"}, "setor": {"Financeiro"}, "justificativa": {"mouse quebrou hoje"}}), "pedir 3")
	sol3 := b.TodasSolicitacoes(10)[0]
	u3 := fmt.Sprintf("/solicitacoes/%d", sol3.ID)
	if w := tec.req("POST", u3+"/validar", url.Values{"csrf": {csrf(tec)}, "acao": {"validar"}, "especificacao": {"Mouse"}, "quantidade": {"1"}, "dispensa": {"urgente"}}); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("técnico não pode dispensar o gestor")
	}
	semErro(t, adm.req("POST", u3+"/validar", url.Values{"csrf": {csrf(adm)}, "acao": {"validar"}, "especificacao": {"Mouse"}, "quantidade": {"1"}, "dispensa": {"item barato e urgente"}}), "dispensar")
	if sol3, _ = b.Solicitacao(sol3.ID); sol3.Situacao != "em_compra" {
		t.Fatalf("dispensa: %s", sol3.Situacao)
	}
	// solicitante cancela antes da compra
	semErro(t, ana.req("POST", u3+"/cancelar", url.Values{"csrf": {csrf(ana)}}), "cancelar")
	// perfil novo criado pelo admin, com área e poder
	semErro(t, adm.req("POST", "/admin/perfis", url.Values{"csrf": {csrf(adm)}, "nome": {"Diretoria"}, "poderes": {PoderAutorizar, PoderVerSetor}}), "novo perfil")
	var dir Perfil
	for _, p := range b.Perfis() {
		if p.Nome == "Diretoria" {
			dir = p
		}
	}
	if dir.ID == 0 || !dir.TemPoder(PoderAutorizar) {
		t.Fatal("perfil Diretoria não criado")
	}
}

func temEnvio(b *Banco, email string) bool {
	for _, e := range b.EnviosFila(200, 0) {
		if strings.Contains(e.Para, email) {
			return true
		}
	}
	return false
}
