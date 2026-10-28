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
	CriadoEm string
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

func (b *Banco) CriarAdmin(usuario, senha string) error {
	usuario = strings.TrimSpace(usuario)
	if usuario == "" || len(senha) < 8 {
		return errors.New("informe o usuário e uma senha com pelo menos 8 caracteres")
	}
	h, err := gerarHash(senha)
	if err != nil {
		return err
	}
	_, err = b.db.Exec(`INSERT INTO admins(usuario,senha,criado_em) VALUES(?,?,?)`, usuario, h, agora())
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return errors.New("já existe um administrador com esse nome")
	}
	return err
}

func (b *Banco) Admins() ([]Admin, error) {
	rows, err := b.db.Query(`SELECT id, usuario, criado_em FROM admins ORDER BY usuario`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []Admin
	for rows.Next() {
		var a Admin
		if rows.Scan(&a.ID, &a.Usuario, &a.CriadoEm) == nil {
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
	err := b.db.QueryRow(`SELECT id, usuario, senha FROM admins WHERE usuario=?`, strings.TrimSpace(usuario)).Scan(&a.ID, &a.Usuario, &h)
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
	err := b.db.QueryRow(`SELECT s.csrf, s.expira, a.id, a.usuario FROM sessoes s JOIN admins a ON a.id=s.admin_id WHERE s.token=?`, token).
		Scan(&s.CSRF, &expira, &s.Admin.ID, &s.Admin.Usuario)
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
		return nova, b.CriarAdmin(usuario, nova)
	}
	_, _ = b.db.Exec(`DELETE FROM sessoes WHERE admin_id=(SELECT id FROM admins WHERE usuario=?)`, usuario)
	return nova, nil
}
