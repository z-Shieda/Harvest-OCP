package servidor

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const iteracoesSenha = 210000

func aleatorio(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func senhaAleatoria() string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func gerarHash(senha string) (string, error) {
	sal := make([]byte, 16)
	_, _ = rand.Read(sal)
	h, err := pbkdf2.Key(sha256.New, senha, sal, iteracoesSenha, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iteracoesSenha, hex.EncodeToString(sal), hex.EncodeToString(h)), nil
}

func conferirSenha(armazenada, senha string) bool {
	p := strings.Split(armazenada, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return false
	}
	it, err := strconv.Atoi(p[1])
	if err != nil {
		return false
	}
	sal, err1 := hex.DecodeString(p[2])
	esperado, err2 := hex.DecodeString(p[3])
	if err1 != nil || err2 != nil {
		return false
	}
	h, err := pbkdf2.Key(sha256.New, senha, sal, it, len(esperado))
	return err == nil && subtle.ConstantTimeCompare(h, esperado) == 1
}

type Admin struct {
	ID       int64
	Usuario  string
	Nome     string
	Email    string
	Papel    string // admin | tecnico | usuario
	Provedor string // local | microsoft
	CriadoEm string
	Tema     string // escuro | claro (1.13, preferência de cada conta)
	Menu     string // topo | lateral
}

func (a Admin) TemaClaro() bool   { return a.Tema == "claro" }
func (a Admin) MenuLateral() bool { return a.Menu == "lateral" }

// Permissões derivadas do papel. Mantidas como métodos para centralizar a regra
// (e facilitar quando o login passar a vir do Microsoft, que só preenche o papel).
func (a Admin) EhAdmin() bool               { return a.Papel == "admin" }
func (a Admin) PodeInventario() bool        { return a.Papel == "admin" || a.Papel == "tecnico" }
func (a Admin) PodeGerenciarChamados() bool { return a.Papel == "admin" || a.Papel == "tecnico" }

func (a Admin) RotuloPapel() string {
	switch a.Papel {
	case "admin":
		return "Administrador"
	case "tecnico":
		return "Técnico"
	case "usuario":
		return "Usuário"
	}
	return a.Papel
}

func papelValido(p string) bool {
	return p == "admin" || p == "tecnico" || p == "usuario"
}

type Sessao struct {
	Token string
	CSRF  string
	Admin Admin
}

func (b *Banco) QtdAdmins() int {
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&n)
	return n
}

func (b *Banco) CriarAdmin(usuario, senha, nome, email, papel string) error {
	usuario = strings.TrimSpace(usuario)
	if usuario == "" || len(senha) < 8 {
		return errors.New("informe o usuário e uma senha com pelo menos 8 caracteres")
	}
	if !papelValido(papel) {
		papel = "tecnico"
	}
	h, err := gerarHash(senha)
	if err != nil {
		return err
	}
	_, err = b.db.Exec(`INSERT INTO admins(usuario,senha,nome,email,papel,provedor,criado_em) VALUES(?,?,?,?,?, 'local', ?)`,
		usuario, h, strings.TrimSpace(nome), strings.TrimSpace(email), papel, agora())
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return errors.New("já existe um usuário com esse nome")
	}
	return err
}

// AtualizarPapel muda o perfil de um usuário (não deixa remover o último administrador).
func (b *Banco) AtualizarPapel(id int64, papel string) error {
	if !papelValido(papel) {
		return errors.New("perfil inválido")
	}
	if papel != "admin" {
		var admins int
		_ = b.db.QueryRow(`SELECT COUNT(*) FROM admins WHERE papel='admin'`).Scan(&admins)
		var ehAdmin int
		_ = b.db.QueryRow(`SELECT COUNT(*) FROM admins WHERE id=? AND papel='admin'`, id).Scan(&ehAdmin)
		if admins <= 1 && ehAdmin == 1 {
			return errors.New("é preciso manter pelo menos um administrador")
		}
	}
	_, err := b.db.Exec(`UPDATE admins SET papel=? WHERE id=?`, papel, id)
	return err
}

const colunasAdmin = `id, usuario, COALESCE(nome,''), COALESCE(email,''), COALESCE(papel,'admin'), COALESCE(provedor,'local'), criado_em, COALESCE(tema,'escuro'), COALESCE(menu,'topo')`

func lerAdmin(sc interface{ Scan(...any) error }) (Admin, error) {
	var a Admin
	err := sc.Scan(&a.ID, &a.Usuario, &a.Nome, &a.Email, &a.Papel, &a.Provedor, &a.CriadoEm, &a.Tema, &a.Menu)
	return a, err
}

func (b *Banco) Admins() ([]Admin, error) {
	rows, err := b.db.Query(`SELECT ` + colunasAdmin + ` FROM admins ORDER BY papel, usuario`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []Admin
	for rows.Next() {
		if a, err := lerAdmin(rows); err == nil {
			l = append(l, a)
		}
	}
	return l, rows.Err()
}

// Tecnicos devolve os usuários que podem ser responsáveis por chamados (admin ou técnico).
func (b *Banco) Tecnicos() ([]Admin, error) {
	rows, err := b.db.Query(`SELECT ` + colunasAdmin + ` FROM admins WHERE papel IN ('admin','tecnico') ORDER BY usuario`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []Admin
	for rows.Next() {
		if a, err := lerAdmin(rows); err == nil {
			l = append(l, a)
		}
	}
	return l, rows.Err()
}

func (b *Banco) ExcluirAdmin(id int64) error {
	if b.QtdAdmins() <= 1 {
		return errors.New("é preciso manter pelo menos um administrador")
	}
	_, err := b.db.Exec(`DELETE FROM admins WHERE id=?`, id)
	return err
}

// VerificarSenha confere a senha de um administrador (usado para confirmar ações sensíveis).
func (b *Banco) VerificarSenha(id int64, senha string) bool {
	var h string
	if b.db.QueryRow(`SELECT senha FROM admins WHERE id=?`, id).Scan(&h) != nil {
		return false
	}
	return conferirSenha(h, senha)
}

func (b *Banco) AlterarSenha(id int64, atual, nova string) error {
	var h string
	if err := b.db.QueryRow(`SELECT senha FROM admins WHERE id=?`, id).Scan(&h); err != nil {
		return err
	}
	if !conferirSenha(h, atual) {
		return errors.New("senha atual incorreta")
	}
	if len(nova) < 8 {
		return errors.New("a nova senha deve ter pelo menos 8 caracteres")
	}
	nh, err := gerarHash(nova)
	if err != nil {
		return err
	}
	_, err = b.db.Exec(`UPDATE admins SET senha=? WHERE id=?`, nh, id)
	if err == nil {
		_, _ = b.db.Exec(`DELETE FROM sessoes WHERE admin_id=?`, id)
	}
	return err
}

func (b *Banco) Entrar(usuario, senha string) (*Sessao, error) {
	var a Admin
	var h string
	err := b.db.QueryRow(`SELECT `+colunasAdmin+`, senha FROM admins WHERE usuario=?`, strings.TrimSpace(usuario)).
		Scan(&a.ID, &a.Usuario, &a.Nome, &a.Email, &a.Papel, &a.Provedor, &a.CriadoEm, &a.Tema, &a.Menu, &h)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !conferirSenha(h, senha)) {
		return nil, errors.New("usuário ou senha inválidos")
	}
	if err != nil {
		return nil, err
	}
	s := &Sessao{Token: aleatorio(32), CSRF: aleatorio(16), Admin: a}
	expira := time.Now().UTC().Add(12 * time.Hour).Format(time.RFC3339)
	_, _ = b.db.Exec(`DELETE FROM sessoes WHERE expira < ?`, agora())
	_, err = b.db.Exec(`INSERT INTO sessoes(token,admin_id,csrf,expira) VALUES(?,?,?,?)`, s.Token, a.ID, s.CSRF, expira)
	return s, err
}

func (b *Banco) SessaoPorToken(token string) (*Sessao, error) {
	s := &Sessao{Token: token}
	var expira string
	err := b.db.QueryRow(`SELECT s.csrf, s.expira, a.id, a.usuario, COALESCE(a.nome,''), COALESCE(a.email,''), COALESCE(a.papel,'admin'), COALESCE(a.provedor,'local'), a.criado_em,
		COALESCE(a.tema,'escuro'), COALESCE(a.menu,'topo')
		FROM sessoes s JOIN admins a ON a.id=s.admin_id WHERE s.token=?`, token).
		Scan(&s.CSRF, &expira, &s.Admin.ID, &s.Admin.Usuario, &s.Admin.Nome, &s.Admin.Email, &s.Admin.Papel, &s.Admin.Provedor, &s.Admin.CriadoEm, &s.Admin.Tema, &s.Admin.Menu)
	if err != nil {
		return nil, err
	}
	if expira < agora() {
		_, _ = b.db.Exec(`DELETE FROM sessoes WHERE token=?`, token)
		return nil, errors.New("sessão expirada")
	}
	return s, nil
}

func (b *Banco) Sair(token string) {
	_, _ = b.db.Exec(`DELETE FROM sessoes WHERE token=?`, token)
}

// limitador simples de tentativas de login por IP
type limitador struct {
	mu    sync.Mutex
	falha map[string][]time.Time
}

func (l *limitador) bloqueado(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim := time.Now().Add(-10 * time.Minute)
	var r []time.Time
	for _, t := range l.falha[ip] {
		if t.After(lim) {
			r = append(r, t)
		}
	}
	l.falha[ip] = r
	return len(r) >= 5
}

func (l *limitador) registrarFalha(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.falha[ip] = append(l.falha[ip], time.Now())
}

func NovoToken() string { return aleatorio(20) }
func NovaSenha() string { return senhaAleatoria() }

// RedefinirSenha gera uma senha nova para o administrador (ou cria-o, se não existir).
func (b *Banco) RedefinirSenha(usuario string) (string, error) {
	nova := senhaAleatoria()
	h, err := gerarHash(nova)
	if err != nil {
		return "", err
	}
	x, err := b.db.Exec(`UPDATE admins SET senha=? WHERE usuario=?`, h, usuario)
	if err != nil {
		return "", err
	}
	if n, _ := x.RowsAffected(); n == 0 {
		return nova, b.CriarAdmin(usuario, nova, "", "", "admin")
	}
	_, _ = b.db.Exec(`DELETE FROM sessoes WHERE admin_id=(SELECT id FROM admins WHERE usuario=?)`, usuario)
	return nova, nil
}

// AtualizarDadosAdmin troca nome e e-mail de uma conta (usado nos avisos por e-mail).
func (b *Banco) AtualizarDadosAdmin(id int64, nome, email string) error {
	nome, email = strings.TrimSpace(nome), strings.TrimSpace(email)
	if email != "" && (!strings.Contains(email, "@") || strings.ContainsAny(email, " \r\n<>,;")) {
		return errors.New("e-mail inválido")
	}
	if len([]rune(nome)) > 120 || len(email) > 160 {
		return errors.New("nome ou e-mail muito longo")
	}
	_, err := b.db.Exec(`UPDATE admins SET nome=?, email=? WHERE id=?`, nome, email, id)
	return err
}

// SalvarAparencia grava o tema e a posição do menu da conta.
func (b *Banco) SalvarAparencia(id int64, tema, menu string) error {
	if tema != "claro" {
		tema = "escuro"
	}
	if menu != "lateral" {
		menu = "topo"
	}
	_, err := b.db.Exec(`UPDATE admins SET tema=?, menu=? WHERE id=?`, tema, menu, id)
	return err
}
