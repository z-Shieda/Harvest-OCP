package servidor

// Perfis e permissões (1.23): o administrador escolhe quais áreas o Técnico e o Usuário veem.
// O administrador vê tudo. Uma área desligada some do menu e o endereço é bloqueado; os dados
// continuam guardados. Para o perfil Usuário as áreas de consulta (Inventário, Ramais, AD,
// Relatórios) são só leitura: qualquer alteração (POST) continua exigindo Técnico ou Admin.

import (
	"errors"
	"fmt"
	"strings"
	"net/http"
)

// AreaMenu: uma área que pode ser mostrada ou escondida por perfil.
type AreaMenu struct {
	Chave       string
	Nome        string
	Descricao   string
	SoEquipe    bool // não pode ser liberada ao perfil Usuário
	PadraoTec   bool
	PadraoUsu   bool
	escritaTodos bool // POST liberado a qualquer perfil com acesso (ex.: abrir chamado)
}

var areasMenu = []AreaMenu{
	{Chave: "chamados", Nome: "Chamados", Descricao: "abrir e acompanhar chamados", PadraoTec: true, PadraoUsu: true, escritaTodos: true},
	{Chave: "solicitacoes", Nome: "Solicitações de equipamento", Descricao: "pedir compras de T.I e acompanhar", PadraoTec: true, PadraoUsu: true, escritaTodos: true},
	{Chave: "relatorios", Nome: "Relatórios", Descricao: "indicadores e SLA dos chamados", PadraoTec: true},
	{Chave: "inventario", Nome: "Inventário", Descricao: "máquinas, usuários de rede, duplicados, notificações", PadraoTec: true},
	{Chave: "ramais", Nome: "Ramais", Descricao: "lista de ramais (MicroSIP)", PadraoTec: true},
	{Chave: "ad", Nome: "Active Directory", Descricao: "comparativo AD × máquinas com o agente", PadraoTec: true},
	{Chave: "termos", Nome: "Termos (gestão)", Descricao: "emitir termos, colaboradores, ativos, documentos", SoEquipe: true, PadraoTec: true},
	{Chave: "meus_termos", Nome: "Meus termos e Minha ficha", Descricao: "assinar os próprios termos", PadraoTec: true, PadraoUsu: true, escritaTodos: true},
}

func areaPorChave(k string) (AreaMenu, bool) {
	for _, a := range areasMenu {
		if a.Chave == k {
			return a, true
		}
	}
	return AreaMenu{}, false
}

// podeArea: o perfil desta conta enxerga a área?
func (b *Banco) podeArea(a Admin, area string) bool {
	if a.EhAdmin() {
		return true
	}
	ar, ok := areaPorChave(area)
	if !ok {
		return false
	}
	switch a.Papel {
	case "tecnico":
		switch b.Config("perm_" + area + "_tecnico") {
		case "1":
			return true
		case "0":
			return false
		}
		return ar.PadraoTec
	case "usuario":
		if ar.SoEquipe {
			return false
		}
		if p, ok := b.perfilDe(a); ok { // 1.24: perfil personalizado decide
			return p.TemArea(area)
		}
		switch b.Config("perm_" + area + "_usuario") {
		case "1":
			return true
		case "0":
			return false
		}
		return ar.PadraoUsu
	}
	return false
}

// LinhaPermissao: uma linha da tela Perfis e permissões.
type LinhaPermissao struct {
	AreaMenu
	Tecnico, Usuario bool
	Perfis           []CelulaPerfil // uma por perfil personalizado, na ordem de b.Perfis()
}

type CelulaPerfil struct {
	PerfilID int64
	Ligada   bool
}

func (b *Banco) Permissoes() []LinhaPermissao {
	var l []LinhaPermissao
	perfis := b.Perfis()
	for _, a := range areasMenu {
		lp := LinhaPermissao{AreaMenu: a,
			Tecnico: b.podeArea(Admin{Papel: "tecnico"}, a.Chave),
			Usuario: b.podeArea(Admin{Papel: "usuario"}, a.Chave)}
		for _, p := range perfis {
			lp.Perfis = append(lp.Perfis, CelulaPerfil{PerfilID: p.ID, Ligada: !a.SoEquipe && p.TemArea(a.Chave)})
		}
		l = append(l, lp)
	}
	return l
}

func (b *Banco) SalvarPermissoes(get func(string) string) {
	um := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	for _, a := range areasMenu {
		_ = b.DefinirConfig("perm_"+a.Chave+"_tecnico", um(get("tec_"+a.Chave) == "1"))
		if !a.SoEquipe {
			_ = b.DefinirConfig("perm_"+a.Chave+"_usuario", um(get("usu_"+a.Chave) == "1"))
		}
	}
	for _, p := range b.Perfis() {
		var areas []string
		for _, a := range areasMenu {
			if !a.SoEquipe && get(fmt.Sprintf("p%d_%s", p.ID, a.Chave)) == "1" {
				areas = append(areas, a.Chave)
			}
		}
		b.salvarAreasPerfil(p.ID, areas)
	}
	b.invalidarPerfis()
}

var errSemArea = errors.New("você não tem acesso a essa área (Administração → Perfis e permissões)")

// permite protege uma rota pela área. Leitura (GET) basta ter a área; alterar exige Técnico ou
// Admin, exceto nas áreas em que todo mundo escreve (chamados, meus termos).
func (s *Servidor) permite(area string, h func(http.ResponseWriter, *http.Request, *Sessao)) func(http.ResponseWriter, *http.Request, *Sessao) {
	return func(w http.ResponseWriter, r *http.Request, sess *Sessao) {
		if !s.b.podeArea(sess.Admin, area) {
			destino := s.inicioDe(sess.Admin)
			if destino == r.URL.Path {
				destino = "/conta"
			}
			redirecionar(w, r, destino, "", errSemArea)
			return
		}
		ar, _ := areaPorChave(area)
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !ar.escritaTodos && !sess.Admin.PodeInventario() {
			volta := r.Referer()
			if volta == "" {
				volta = s.inicioDe(sess.Admin)
			}
			redirecionar(w, r, volta, "", errors.New("seu perfil só consulta esta área; alterações são feitas pela T.I"))
			return
		}
		h(w, r, sess)
	}
}

// inicioDe: a primeira tela que a conta pode abrir.
func (s *Servidor) inicioDe(a Admin) string {
	if a.PodeGerenciarChamados() {
		return "/"
	}
	for _, x := range []struct{ area, url string }{
		{"chamados", "/chamados"}, {"solicitacoes", "/solicitacoes"}, {"meus_termos", "/meus-termos"}, {"inventario", "/inventario"},
		{"ramais", "/ramais"}, {"relatorios", "/chamados/relatorios"}, {"ad", "/ad"},
	} {
		if s.b.podeArea(a, x.area) {
			return x.url
		}
	}
	return "/conta"
}

func (s *Servidor) acaoPermissoes(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	s.b.SalvarPermissoes(r.FormValue)
	resumo := ""
	for _, p := range s.b.Permissoes() {
		resumo += p.Chave + "=" + map[bool]string{true: "T", false: "-"}[p.Tecnico] + map[bool]string{true: "U", false: "-"}[p.Usuario] + " "
	}
	s.b.Auditar(sess.Admin.Usuario, "perfis e permissões", resumo)
	redirecionar(w, r, "/admin#permissoes", "Permissões salvas. Valem no próximo clique de cada pessoa.", nil)
}

// ---------------- perfis personalizados (telas) ----------------

func (s *Servidor) acaoPerfil(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	_ = r.ParseForm()
	id := parseID(r.FormValue("id"))
	if r.FormValue("excluir") == "1" {
		err := s.b.ExcluirPerfil(id)
		if err == nil {
			s.b.Auditar(sess.Admin.Usuario, "perfis: excluir", fmt.Sprint("#", id))
		}
		redirecionar(w, r, "/admin#perfis", "Perfil excluído.", err)
		return
	}
	novo, err := s.b.SalvarPerfil(id, r.FormValue("nome"), r.Form["poderes"])
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "perfis: salvar", fmt.Sprintf("#%d %s %v", novo, r.FormValue("nome"), r.Form["poderes"]))
	}
	redirecionar(w, r, "/admin#perfis", "Perfil salvo. Ligue as áreas dele em Perfis e permissões.", err)
}

// acaoPerfilConta: na ficha do colaborador, o admin escolhe o perfil e os setores de gestor.
func (s *Servidor) acaoPerfilConta(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	_ = r.ParseForm()
	id := idDaURL(r)
	setores := r.Form["setores"]
	for _, x := range strings.Split(r.FormValue("setores_texto"), ",") {
		setores = append(setores, x)
	}
	err := s.b.DefinirPerfilConta(id, parseID(r.FormValue("perfil_id")), setores)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "perfis: perfil da conta", fmt.Sprintf("conta #%d perfil #%s setores %v", id, r.FormValue("perfil_id"), setores))
		_, _ = s.b.db.Exec(`DELETE FROM sessoes WHERE admin_id=?`, id) // o menu muda no próximo login
	}
	volta := r.FormValue("voltar")
	if !strings.HasPrefix(volta, "/") || strings.HasPrefix(volta, "//") {
		volta = fmt.Sprintf("/admin/colaboradores/%d#perfil", id)
	}
	redirecionar(w, r, volta, "Perfil da conta salvo (a pessoa precisa entrar de novo).", err)
}
