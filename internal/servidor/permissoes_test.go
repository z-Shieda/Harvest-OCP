package servidor

import (
	"net/url"
	"strings"
	"testing"
)

func TestPermissoesPorPerfil(t *testing.T) {
	b := bancoTeste(t)
	s, err := Novo(b, "teste")
	if err != nil {
		t.Fatal(err)
	}
	h := s.Rotas()
	_ = b.CriarAdmin("admin", "senha-admin-1", "Admin", "", "admin")
	_ = b.CriarAdmin("tec", "senha-tec-12", "Técnico", "", "tecnico")
	_ = b.CriarAdmin("usu", "senha-usu-12", "Usuário", "", "usuario")
	entrar := func(u, sn string) *cliente {
		c := &cliente{t: t, h: h}
		c.req("POST", "/login", url.Values{"usuario": {u}, "senha": {sn}})
		return c
	}
	adm, tec, usu := entrar("admin", "senha-admin-1"), entrar("tec", "senha-tec-12"), entrar("usu", "senha-usu-12")

	// padrão: técnico vê o inventário; usuário não
	if w := tec.req("GET", "/maquinas", nil); w.Code != 200 {
		t.Fatalf("técnico deveria ver máquinas: %d", w.Code)
	}
	if w := usu.req("GET", "/maquinas", nil); w.Code != 303 {
		t.Fatal("usuário não deveria ver máquinas por padrão")
	}
	if pg := usu.req("GET", "/chamados", nil).Body.String(); strings.Contains(pg, `href="/maquinas"`) || !strings.Contains(pg, `href="/chamados"`) {
		t.Fatal("menu do usuário deveria ter Chamados e não Máquinas")
	}
	// a tela de permissões aparece na Administração
	if pg := adm.req("GET", "/admin", nil).Body.String(); !strings.Contains(pg, "Perfis e permissões") || !strings.Contains(pg, `name="usu_ramais"`) {
		t.Fatal("tela de permissões ausente")
	}
	cs, _ := b.SessaoPorToken(adm.cookie)
	// libera Ramais ao usuário e tira o Inventário do técnico
	adm.req("POST", "/admin/permissoes", url.Values{"csrf": {cs.CSRF}, "tec_chamados": {"1"}, "tec_ramais": {"1"}, "usu_chamados": {"1"}, "usu_ramais": {"1"}})
	if w := usu.req("GET", "/ramais", nil); w.Code != 200 {
		t.Fatalf("usuário deveria ver ramais: %d", w.Code)
	}
	if !strings.Contains(usu.req("GET", "/chamados", nil).Body.String(), `href="/ramais"`) {
		t.Fatal("ramais deveria aparecer no menu do usuário")
	}
	us, _ := b.SessaoPorToken(usu.cookie)
	if w := usu.req("POST", "/ramais/100/ajustar", url.Values{"csrf": {us.CSRF}, "nome": {"x"}}); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("usuário só consulta ramais")
	}
	if w := tec.req("GET", "/maquinas", nil); w.Code != 303 {
		t.Fatal("técnico sem inventário não deveria ver máquinas")
	}
	if w := adm.req("GET", "/maquinas", nil); w.Code != 200 {
		t.Fatal("administrador vê tudo")
	}
	// termos (gestão) nunca vai para o usuário
	if b.podeArea(Admin{Papel: "usuario"}, "termos") {
		t.Fatal("gestão de termos é só da equipe")
	}
	// sem chamados nem nada: o usuário cai na conta, sem laço de redirecionamento
	adm.req("POST", "/admin/permissoes", url.Values{"csrf": {cs.CSRF}})
	if loc := usu.req("GET", "/", nil).Header().Get("Location"); loc != "/conta" {
		t.Fatalf("sem áreas o usuário deveria ir para a conta: %s", loc)
	}
}
