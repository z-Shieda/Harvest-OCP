package servidor

// Configurações (1.13): aparência de cada conta (tema claro/escuro, menu no topo ou na lateral)
// e, para o administrador, a lista de setores que aparece para escolha nos formulários.

import (
	"net/http"
	"sort"
	"strings"
)

// SetoresFixos devolve a lista definida em Configurações (vazia = setor em texto livre).
func (b *Banco) SetoresFixos() []string {
	var r []string
	vistos := map[string]bool{}
	for _, l := range strings.Split(b.Config("setores_lista"), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || vistos[strings.ToLower(l)] {
			continue
		}
		vistos[strings.ToLower(l)] = true
		r = append(r, l)
	}
	return r
}

// setoresEmUso: setores já digitados no sistema (para importar para a lista).
func (b *Banco) setoresEmUso() []string {
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

// setorValido: com lista definida, só aceita setores dela (vazio é aceito).
func (b *Banco) setorValido(setor string) bool {
	fixos := b.SetoresFixos()
	if setor == "" || len(fixos) == 0 {
		return true
	}
	for _, f := range fixos {
		if strings.EqualFold(f, setor) {
			return true
		}
	}
	return false
}

func (s *Servidor) paginaConfiguracoes(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	d := map[string]any{}
	if sess.Admin.EhAdmin() {
		fixos := s.b.SetoresFixos()
		naLista := map[string]bool{}
		for _, f := range fixos {
			naLista[strings.ToLower(f)] = true
		}
		var foraDaLista []string
		for _, u := range s.b.setoresEmUso() {
			if !naLista[strings.ToLower(u)] {
				foraDaLista = append(foraDaLista, u)
			}
		}
		d["Setores"] = fixos
		d["SetoresTexto"] = strings.Join(fixos, "\n")
		d["ForaDaLista"] = foraDaLista
		d["Monitor"] = s.b.ConfigMonitor()
		d["MarcaPadrao"] = padraoMarca()
		d["MarcaNome"] = s.b.Config("marca_nome")
	}
	s.render(w, r, "configuracoes", "Configurações", "config", sess, d)
}

func (s *Servidor) acaoAparencia(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	err := s.b.SalvarAparencia(sess.Admin.ID, r.FormValue("tema"), r.FormValue("menu"))
	redirecionar(w, r, "/configuracoes", "Aparência salva.", err)
}

func (s *Servidor) acaoSetores(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	texto := r.FormValue("setores")
	if r.FormValue("importar") != "" {
		texto += "\n" + strings.Join(s.b.setoresEmUso(), "\n")
	}
	var lista []string
	vistos := map[string]bool{}
	for _, l := range strings.Split(texto, "\n") {
		l = strings.Join(strings.Fields(l), " ")
		if len(l) > 60 {
			l = l[:60]
		}
		if l == "" || vistos[strings.ToLower(l)] {
			continue
		}
		vistos[strings.ToLower(l)] = true
		lista = append(lista, l)
	}
	if r.FormValue("ordenar") != "" || r.FormValue("importar") != "" {
		sort.Slice(lista, func(i, j int) bool { return strings.ToLower(lista[i]) < strings.ToLower(lista[j]) })
	}
	err := s.b.DefinirConfig("setores_lista", strings.Join(lista, "\n"))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "lista de setores", strings.Join(lista, ", "))
	}
	msg := "Lista de setores salva. Os formulários passam a mostrar só esses setores."
	if len(lista) == 0 {
		msg = "Lista de setores vazia: os formulários voltam a aceitar setor em texto livre."
	}
	redirecionar(w, r, "/configuracoes#setores", msg, err)
}
