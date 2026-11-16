package servidor

// Tokens anteriores (1.11.2): quando o servidor é reinstalado ele pode ganhar um token novo e as
// máquinas antigas passam a ser recusadas ("401 token inválido") — e, sem enviar, nunca recebem
// o auto-update. O servidor agora anota quem está sendo recusado e o administrador pode
// "aceitar" aquele token com um clique. Agentes 1.11.2+ que usam um token aceito recebem o
// token atual na resposta e trocam sozinhos.

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func tokenIgual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (b *Banco) tokensAceitos() []string {
	var r []string
	for _, t := range strings.Split(b.Config("tokens_aceitos"), "\n") {
		if t = strings.TrimSpace(t); t != "" {
			r = append(r, t)
		}
	}
	return r
}

// validarTokenAgente: ok = pode enviar; antigo = usou um token anterior aceito.
func (b *Banco) validarTokenAgente(t string) (ok, antigo bool) {
	if tokenIgual(t, b.Config("token_agente")) {
		return true, false
	}
	for _, a := range b.tokensAceitos() {
		if tokenIgual(t, a) {
			return true, true
		}
	}
	return false, false
}

// registrarRecusa anota uma tentativa com token inválido (para o admin poder aceitar).
func (b *Banco) registrarRecusa(token, ip string, corpo io.Reader) {
	if len(token) < 8 || len(token) > 200 {
		return
	}
	var cab struct {
		Hostname     string `json:"hostname"`
		AgentVersion string `json:"agent_version"`
	}
	if corpo != nil {
		_ = json.NewDecoder(io.LimitReader(corpo, 8<<20)).Decode(&cab)
	}
	if len(cab.Hostname) > 100 {
		cab.Hostname = cab.Hostname[:100]
	}
	t := agora()
	_, _ = b.db.Exec(`INSERT INTO tokens_recusados(token,ip,hostname,versao,tentativas,primeiro,ultimo) VALUES(?,?,?,?,1,?,?)
		ON CONFLICT(token) DO UPDATE SET ip=excluded.ip, hostname=CASE WHEN excluded.hostname<>'' THEN excluded.hostname ELSE hostname END,
		versao=CASE WHEN excluded.versao<>'' THEN excluded.versao ELSE versao END, tentativas=tentativas+1, ultimo=excluded.ultimo`,
		token, ip, cab.Hostname, cab.AgentVersion, t, t)
	// guarda no máximo 200 tokens diferentes
	_, _ = b.db.Exec(`DELETE FROM tokens_recusados WHERE token NOT IN (SELECT token FROM tokens_recusados ORDER BY ultimo DESC LIMIT 200)`)
}

type TokenRecusado struct {
	ID         int64
	Prefixo    string
	IP         string
	Hostnames  string
	Versao     string
	Tentativas int
	Ultimo     string
}

func prefixoToken(t string) string {
	if len(t) <= 4 {
		return t
	}
	return t[:4] + "…"
}

func (b *Banco) TokensRecusados() []TokenRecusado {
	limite := time.Now().AddDate(0, 0, -30).UTC().Format(time.RFC3339)
	rows, err := b.db.Query(`SELECT rowid, token, ip, hostname, versao, tentativas, ultimo FROM tokens_recusados WHERE ultimo>=? ORDER BY ultimo DESC LIMIT 50`, limite)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var r []TokenRecusado
	for rows.Next() {
		var x TokenRecusado
		var tok string
		if rows.Scan(&x.ID, &tok, &x.IP, &x.Hostnames, &x.Versao, &x.Tentativas, &x.Ultimo) == nil {
			x.Prefixo = prefixoToken(tok)
			r = append(r, x)
		}
	}
	return r
}

func (s *Servidor) acaoAceitarToken(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	var tok string
	if err := s.b.db.QueryRow(`SELECT token FROM tokens_recusados WHERE rowid=?`, id).Scan(&tok); err != nil {
		redirecionar(w, r, "/admin#token", "", err)
		return
	}
	lista := s.b.tokensAceitos()
	lista = append(lista, tok)
	err := s.b.DefinirConfig("tokens_aceitos", strings.Join(lista, "\n"))
	if err == nil {
		_, _ = s.b.db.Exec(`DELETE FROM tokens_recusados WHERE token=?`, tok)
		s.b.Auditar(sess.Admin.Usuario, "aceitar token anterior de agente", prefixoToken(tok))
	}
	redirecionar(w, r, "/admin#token", "Token "+prefixoToken(tok)+" aceito. As máquinas que usam esse token voltam a enviar no próximo contato (e recebem o auto-update).", err)
}

func (s *Servidor) acaoRemoverTokenAceito(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	i, _ := strconv.Atoi(r.FormValue("i"))
	lista := s.b.tokensAceitos()
	if i < 0 || i >= len(lista) {
		redirecionar(w, r, "/admin#token", "", nil)
		return
	}
	tok := lista[i]
	lista = append(lista[:i], lista[i+1:]...)
	err := s.b.DefinirConfig("tokens_aceitos", strings.Join(lista, "\n"))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "remover token anterior de agente", prefixoToken(tok))
	}
	redirecionar(w, r, "/admin#token", "Token "+prefixoToken(tok)+" removido.", err)
}

// contagem de máquinas que ainda usam cada token aceito (agentes antigos não trocam sozinhos)
func (b *Banco) TokensAceitosResumo() []map[string]any {
	var r []map[string]any
	for i, t := range b.tokensAceitos() {
		r = append(r, map[string]any{"I": i, "Prefixo": prefixoToken(t)})
	}
	return r
}
