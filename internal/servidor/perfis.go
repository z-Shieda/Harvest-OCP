package servidor

// Perfis personalizados (1.24): além de Administrador, Técnico e Usuário (fixos), o admin cria
// perfis = áreas do menu + poderes. Uma conta com perfil continua com papel "usuario" (não mexe
// no inventário nem na administração); o perfil decide o que ela vê e o que pode fazer nas
// Solicitações de equipamento. Especificação: docs/SDD-1.24-SOLICITACOES.md.

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// Poderes que um perfil pode ter.
const (
	PoderVerSetor  = "ver_setor" // vê as solicitações dos setores ligados à conta
	PoderAutorizar = "autorizar" // autoriza (ou recusa) compras como gestor
	PoderComprar   = "comprar"   // Suprimentos: registra as compras
)

var poderesPerfil = []opcao{
	{PoderVerSetor, "Ver as solicitações do(s) setor(es) dele"},
	{PoderAutorizar, "Autorizar compras (gestor)"},
	{PoderComprar, "Registrar compras (Suprimentos)"},
}

const esquemaPerfis = `
CREATE TABLE IF NOT EXISTS perfis (
	id INTEGER PRIMARY KEY,
	chave TEXT NOT NULL UNIQUE,
	nome TEXT NOT NULL,
	areas TEXT NOT NULL DEFAULT '',
	poderes TEXT NOT NULL DEFAULT '',
	sistema INTEGER NOT NULL DEFAULT 0,
	criado_em TEXT NOT NULL DEFAULT ''
);`

type Perfil struct {
	ID      int64
	Chave   string
	Nome    string
	Areas   []string
	Poderes []string
	Sistema bool
	Contas  int // leitura
}

func (p Perfil) TemArea(a string) bool   { return temValor(p.Areas, a) }
func (p Perfil) TemPoder(x string) bool  { return temValor(p.Poderes, x) }
func (p Perfil) PoderesTexto() string {
	var l []string
	for _, o := range poderesPerfil {
		if p.TemPoder(o.Valor) {
			l = append(l, o.Rotulo)
		}
	}
	return strings.Join(l, " · ")
}

// cachePerfis: os perfis são lidos a cada página (menu); guardados até alguém salvar.
type cachePerfis struct {
	mu    sync.Mutex
	ok    bool
	porID map[int64]Perfil
}

func (b *Banco) invalidarPerfis() {
	b.perfis.mu.Lock()
	b.perfis.ok = false
	b.perfis.mu.Unlock()
}

func (b *Banco) migrarPerfis() {
	_, _ = b.db.Exec(esquemaPerfis)
	for _, alter := range []string{
		`ALTER TABLE admins ADD COLUMN perfil_id INTEGER`,
		`ALTER TABLE admins ADD COLUMN setores_gestor TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = b.db.Exec(alter)
	}
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM perfis`).Scan(&n)
	if n == 0 {
		for _, p := range []Perfil{
			{Chave: "gestor", Nome: "Gestor", Areas: []string{"chamados", "solicitacoes", "meus_termos"}, Poderes: []string{PoderVerSetor}},
			{Chave: "gestor_autoriza", Nome: "Gestor que autoriza", Areas: []string{"chamados", "solicitacoes", "meus_termos"}, Poderes: []string{PoderVerSetor, PoderAutorizar}},
			{Chave: "suprimentos", Nome: "Suprimentos", Areas: []string{"chamados", "solicitacoes", "meus_termos"}, Poderes: []string{PoderComprar}},
		} {
			_, _ = b.db.Exec(`INSERT INTO perfis(chave,nome,areas,poderes,sistema,criado_em) VALUES(?,?,?,?,0,?)`,
				p.Chave, p.Nome, strings.Join(p.Areas, ","), strings.Join(p.Poderes, ","), agora())
		}
	}
	b.invalidarPerfis()
}

func (b *Banco) perfisMapa() map[int64]Perfil {
	b.perfis.mu.Lock()
	defer b.perfis.mu.Unlock()
	if b.perfis.ok {
		return b.perfis.porID
	}
	m := map[int64]Perfil{}
	rows, err := b.db.Query(`SELECT id, chave, nome, areas, poderes, sistema FROM perfis`)
	if err == nil {
		for rows.Next() {
			var p Perfil
			var ar, po string
			if rows.Scan(&p.ID, &p.Chave, &p.Nome, &ar, &po, &p.Sistema) == nil {
				p.Areas, p.Poderes = dividir(ar, ","), dividir(po, ",")
				m[p.ID] = p
			}
		}
		rows.Close()
		b.perfis.porID, b.perfis.ok = m, true
	}
	return m
}

// Perfis devolve todos, com quantas contas usam cada um.
func (b *Banco) Perfis() []Perfil {
	var l []Perfil
	for _, p := range b.perfisMapa() {
		_ = b.db.QueryRow(`SELECT COUNT(*) FROM admins WHERE perfil_id=? AND papel='usuario'`, p.ID).Scan(&p.Contas)
		l = append(l, p)
	}
	sort.Slice(l, func(i, j int) bool { return strings.ToLower(l[i].Nome) < strings.ToLower(l[j].Nome) })
	return l
}

func (b *Banco) perfilDe(a Admin) (Perfil, bool) {
	if a.Papel != "usuario" || a.PerfilID == 0 {
		return Perfil{}, false
	}
	p, ok := b.perfisMapa()[a.PerfilID]
	return p, ok
}

// temPoder: Admin tem todos; Técnico tem só ver tudo da T.I (não autoriza nem compra);
// Usuário só pelo perfil.
func (b *Banco) temPoder(a Admin, poder string) bool {
	if a.EhAdmin() {
		return true
	}
	if p, ok := b.perfilDe(a); ok {
		return p.TemPoder(poder)
	}
	return false
}

// SalvarPerfil cria (id 0) ou altera nome e poderes.
func (b *Banco) SalvarPerfil(id int64, nome string, poderes []string) (int64, error) {
	nome = strings.Join(strings.Fields(nome), " ")
	if nome == "" || len([]rune(nome)) > 60 {
		return 0, errors.New("informe o nome do perfil (até 60 letras)")
	}
	switch strings.ToLower(nome) {
	case "administrador", "técnico", "tecnico", "usuário", "usuario":
		return 0, errors.New("esse nome é de um perfil fixo; escolha outro")
	}
	var ps []string
	for _, o := range poderesPerfil {
		if temValor(poderes, o.Valor) {
			ps = append(ps, o.Valor)
		}
	}
	defer b.invalidarPerfis()
	if id > 0 {
		_, err := b.db.Exec(`UPDATE perfis SET nome=?, poderes=? WHERE id=?`, nome, strings.Join(ps, ","), id)
		return id, err
	}
	x, err := b.db.Exec(`INSERT INTO perfis(chave,nome,areas,poderes,criado_em) VALUES(?,?,?,?,?)`,
		"perfil"+aleatorio(4), nome, "chamados,solicitacoes,meus_termos", strings.Join(ps, ","), agora())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return 0, errors.New("já existe um perfil com esse nome")
		}
		return 0, err
	}
	return x.LastInsertId()
}

func (b *Banco) ExcluirPerfil(id int64) error {
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM admins WHERE perfil_id=?`, id).Scan(&n)
	if n > 0 {
		return errors.New("há contas usando este perfil; troque o perfil delas antes")
	}
	_, err := b.db.Exec(`DELETE FROM perfis WHERE id=?`, id)
	b.invalidarPerfis()
	return err
}

func (b *Banco) salvarAreasPerfil(id int64, areas []string) {
	_, _ = b.db.Exec(`UPDATE perfis SET areas=? WHERE id=?`, strings.Join(areas, ","), id)
}

// DefinirPerfilConta liga a conta (papel usuário) a um perfil (0 = Usuário comum) e define os
// setores pelos quais ela responde como gestor.
func (b *Banco) DefinirPerfilConta(adminID, perfilID int64, setores []string) error {
	var papel string
	if err := b.db.QueryRow(`SELECT papel FROM admins WHERE id=?`, adminID).Scan(&papel); err != nil {
		return err
	}
	if perfilID > 0 {
		if _, ok := b.perfisMapa()[perfilID]; !ok {
			return errors.New("perfil inválido")
		}
		if papel != "usuario" {
			return errors.New("perfis personalizados valem para contas com papel Usuário (Admin e Técnico já veem tudo)")
		}
	}
	var ss []string
	vistos := map[string]bool{}
	for _, x := range setores {
		x = strings.TrimSpace(x)
		if x != "" && !vistos[strings.ToLower(x)] {
			vistos[strings.ToLower(x)] = true
			ss = append(ss, x)
		}
	}
	_, err := b.db.Exec(`UPDATE admins SET perfil_id=?, setores_gestor=? WHERE id=?`, nullID(perfilID), strings.Join(ss, ","), adminID)
	return err
}

// SetoresGestor devolve os setores pelos quais a conta responde.
func (b *Banco) SetoresGestor(adminID int64) []string {
	var s string
	_ = b.db.QueryRow(`SELECT setores_gestor FROM admins WHERE id=?`, adminID).Scan(&s)
	return dividir(s, ",")
}

// GestoresQueAutorizam: contas ativas que podem autorizar compras (Admins e perfis com o poder).
func (b *Banco) GestoresQueAutorizam() []Admin {
	rows, err := b.db.Query(`SELECT ` + colunasAdmin + ` FROM admins WHERE COALESCE(ativo,1)=1 ORDER BY COALESCE(NULLIF(nome,''),usuario) COLLATE NOCASE`)
	if err != nil {
		return nil
	}
	var todos []Admin
	for rows.Next() {
		if a, err := lerAdmin(rows); err == nil {
			todos = append(todos, a)
		}
	}
	rows.Close()
	var l []Admin
	for _, a := range todos {
		if a.Papel != "admin" && b.temPoder(a, PoderAutorizar) {
			l = append(l, a)
		}
	}
	for _, a := range todos { // admins por último
		if a.Papel == "admin" {
			l = append(l, a)
		}
	}
	return l
}

// GestorDoSetor: primeira conta com o poder de autorizar ligada ao setor.
func (b *Banco) GestorDoSetor(setor string) int64 {
	setor = strings.TrimSpace(setor)
	if setor == "" {
		return 0
	}
	for _, a := range b.GestoresQueAutorizam() {
		if a.Papel == "admin" {
			continue
		}
		for _, x := range b.SetoresGestor(a.ID) {
			if strings.EqualFold(x, setor) {
				return a.ID
			}
		}
	}
	return 0
}

// contasComPoder: e-mails das contas ativas com o poder (ex.: Suprimentos).
func (b *Banco) emailsComPoder(poder string) []string {
	rows, err := b.db.Query(`SELECT ` + colunasAdmin + ` FROM admins WHERE COALESCE(ativo,1)=1 AND papel='usuario' AND perfil_id IS NOT NULL`)
	if err != nil {
		return nil
	}
	var as []Admin
	for rows.Next() {
		if a, err := lerAdmin(rows); err == nil {
			as = append(as, a)
		}
	}
	rows.Close()
	var l []string
	for _, a := range as {
		if b.temPoder(a, poder) && strings.Contains(a.Email, "@") {
			l = append(l, strings.ToLower(a.Email))
		}
	}
	return l
}
