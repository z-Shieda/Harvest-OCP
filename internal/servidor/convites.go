package servidor

// Contas por convite (1.23, D2): a T.I cadastra o e-mail corporativo, o Harvest cria uma conta
// com perfil Usuário (sem senha) e manda um link de uso único para a pessoa definir a senha.
// O banco guarda só o hash SHA-256 do token; o convite vale ConviteDias (padrão 7).

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

func hashConvite(token string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(h[:])
}

var errConviteInvalido = errors.New("este convite não vale mais (já foi usado, expirou ou foi substituído). Peça um novo à T.I")

// CriarConvite cria a conta (ou renova o convite de uma conta que ainda não definiu a senha) e
// devolve o token em claro — só para montar o link; ele não fica gravado.
func (b *Banco) CriarConvite(email, nome, por string, dias int, dominios []string) (adminID int64, token string, renovado bool, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !emailValido(email) {
		return 0, "", false, errors.New("e-mail inválido")
	}
	if !dominioAceito(email, dominios) {
		return 0, "", false, errors.New("só e-mails corporativos (" + strings.Join(dominios, ", ") + ") podem receber convite")
	}
	nome = strings.Join(strings.Fields(nome), " ")
	if len([]rune(nome)) > 120 {
		return 0, "", false, errors.New("nome muito longo")
	}
	token = aleatorio(32)
	expira := prazoConvite(dias)

	var id int64
	var temSenha bool
	err = b.db.QueryRow(`SELECT id, senha<>'' FROM admins WHERE usuario=? OR lower(email)=?`, email, email).Scan(&id, &temSenha)
	switch {
	case err == nil && temSenha:
		return id, "", false, errors.New("já existe uma conta com esse e-mail e ela já tem senha — a pessoa entra com o e-mail e a senha dela")
	case err == nil:
		// renova o convite (o link antigo deixa de valer)
		b.cancelarConvitesNaFila(id)
		q := `UPDATE admins SET convite_token_hash=?, convite_expira=?, convidado_por=?, ativo=1`
		args := []any{hashConvite(token), expira, por}
		if nome != "" {
			q += `, nome=?`
			args = append(args, nome)
		}
		_, err = b.db.Exec(q+` WHERE id=?`, append(args, id)...)
		return id, token, true, err
	case !errors.Is(err, sql.ErrNoRows):
		return 0, "", false, err
	}
	x, err := b.db.Exec(`INSERT INTO admins(usuario,senha,nome,email,papel,provedor,criado_em,convite_token_hash,convite_expira,convidado_por,ativo)
		VALUES(?, '', ?, ?, 'usuario', 'local', ?, ?, ?, ?, 1)`, email, nome, email, agora(), hashConvite(token), expira, por)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return 0, "", false, errors.New("já existe uma conta com esse usuário")
		}
		return 0, "", false, err
	}
	id, err = x.LastInsertId()
	return id, token, false, err
}

// ContaDoConvite devolve a conta de um convite ainda válido.
func (b *Banco) ContaDoConvite(token string) (*Ficha, error) {
	if len(strings.TrimSpace(token)) < 32 {
		return nil, errConviteInvalido
	}
	var id int64
	err := b.db.QueryRow(`SELECT id FROM admins WHERE convite_token_hash=? AND senha='' AND ativo=1 AND convite_expira >= ?`,
		hashConvite(token), agora()).Scan(&id)
	if err != nil {
		return nil, errConviteInvalido
	}
	return b.Ficha(id)
}

// AceitarConvite define a senha e encerra o convite (uso único).
func (b *Banco) AceitarConvite(token, senha string) (int64, error) {
	if len(senha) < 8 {
		return 0, errors.New("a senha deve ter pelo menos 8 caracteres")
	}
	if len(senha) > 200 {
		return 0, errors.New("senha longa demais")
	}
	f, err := b.ContaDoConvite(token)
	if err != nil {
		return 0, err
	}
	if senhaFraca(senha, f.Email) {
		return 0, errors.New("escolha uma senha menos óbvia (não use o seu e-mail, nome de usuário ou sequências como 12345678)")
	}
	h, err := gerarHash(senha)
	if err != nil {
		return 0, err
	}
	// a condição repete as regras: dois envios ao mesmo tempo não usam o mesmo convite
	x, err := b.db.Exec(`UPDATE admins SET senha=?, convite_token_hash='', convite_expira='' WHERE id=? AND convite_token_hash=? AND senha='' AND convite_expira >= ?`,
		h, f.AdminID, hashConvite(token), agora())
	if err != nil {
		return 0, err
	}
	if n, _ := x.RowsAffected(); n != 1 {
		return 0, errConviteInvalido
	}
	b.cancelarConvitesNaFila(f.AdminID)
	return f.AdminID, nil
}

func senhaFraca(senha, email string) bool {
	l := strings.ToLower(senha)
	if l == strings.ToLower(email) || l == chaveCurta(email) {
		return true
	}
	for _, ruim := range []string{"12345678", "123456789", "1234567890", "senha123", "password", "ocupacional", "qwertyui", "abcdefgh"} {
		if l == ruim {
			return true
		}
	}
	return strings.Count(l, l[:1]) == len(l) // "aaaaaaaa"
}

// CancelarConvite invalida o link pendente (a conta continua, sem senha).
func (b *Banco) CancelarConvite(adminID int64) error {
	_, err := b.db.Exec(`UPDATE admins SET convite_token_hash='', convite_expira=? WHERE id=? AND senha=''`, agora(), adminID)
	b.cancelarConvitesNaFila(adminID)
	return err
}

// SessaoPara abre uma sessão para a conta (usado logo após definir a senha pelo convite).
func (b *Banco) SessaoPara(adminID int64) (*Sessao, error) {
	a, err := b.AdminPorID(adminID)
	if err != nil {
		return nil, err
	}
	s := &Sessao{Token: aleatorio(32), CSRF: aleatorio(16), Admin: *a}
	expira := time.Now().UTC().Add(12 * time.Hour).Format(time.RFC3339)
	_, err = b.db.Exec(`INSERT INTO sessoes(token,admin_id,csrf,expira) VALUES(?,?,?,?)`, s.Token, a.ID, s.CSRF, expira)
	return s, err
}
