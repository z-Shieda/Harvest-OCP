package servidor

// Lixeira (1.16). Excluir uma máquina não apaga mais na hora: ela vai para a lixeira com uma cópia
// de tudo (cadastro, placas de rede, histórico de logins e os chamados ligados a ela) e pode ser
// restaurada. Enquanto estiver na lixeira, os envios do agente dela são aceitos e ignorados (só
// contados), para ela não "voltar sozinha". Excluir permanentemente apaga a cópia.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type copiaMaquina struct {
	Maquina    map[string]any   `json:"maquina"`
	Interfaces []map[string]any `json:"interfaces"`
	Logins     []map[string]any `json:"logins"`
	Ramais     []map[string]any `json:"ramais"`
	Chamados   []int64          `json:"chamados"`
}

func linhasComoMapa(tx *sql.Tx, q string, args ...any) ([]map[string]any, error) {
	rows, err := tx.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var r []map[string]any
	for rows.Next() {
		v := make([]any, len(cols))
		p := make([]any, len(cols))
		for i := range v {
			p[i] = &v[i]
		}
		if err := rows.Scan(p...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, c := range cols {
			if b, ok := v[i].([]byte); ok {
				v[i] = string(b)
			}
			m[c] = v[i]
		}
		r = append(r, m)
	}
	return r, rows.Err()
}

func colunasDe(tx *sql.Tx, tabela string) map[string]bool {
	r := map[string]bool{}
	rows, err := tx.Query(`SELECT name FROM pragma_table_info(?)`, tabela)
	if err != nil {
		return r
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			r[n] = true
		}
	}
	return r
}

func inserirMapa(tx *sql.Tx, tabela string, m map[string]any, validas map[string]bool) error {
	var cols, marcas []string
	var vals []any
	for c, v := range m {
		if !validas[c] {
			continue // coluna que não existe mais nesta versão
		}
		cols = append(cols, c)
		marcas = append(marcas, "?")
		vals = append(vals, v)
	}
	_, err := tx.Exec(`INSERT INTO `+tabela+`(`+strings.Join(cols, ",")+`) VALUES(`+strings.Join(marcas, ",")+`)`, vals...)
	return err
}

// MoverMaquinaParaLixeira guarda a cópia e tira a máquina do inventário.
func (b *Banco) MoverMaquinaParaLixeira(id int64, por string) error {
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	maq, err := linhasComoMapa(tx, `SELECT * FROM maquinas WHERE id=?`, id)
	if err != nil {
		return err
	}
	if len(maq) == 0 {
		return fmt.Errorf("máquina não encontrada")
	}
	c := copiaMaquina{Maquina: maq[0]}
	if c.Interfaces, err = linhasComoMapa(tx, `SELECT * FROM interfaces WHERE maquina_id=?`, id); err != nil {
		return err
	}
	if c.Logins, err = linhasComoMapa(tx, `SELECT * FROM logins WHERE maquina_id=?`, id); err != nil {
		return err
	}
	if c.Ramais, err = linhasComoMapa(tx, `SELECT * FROM ramais WHERE maquina_id=?`, id); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id FROM chamados WHERE maquina_id=?`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int64
		if rows.Scan(&cid) == nil {
			c.Chamados = append(c.Chamados, cid)
		}
	}
	rows.Close()
	dados, err := json.Marshal(c)
	if err != nil {
		return err
	}
	texto := func(k string) string { s, _ := c.Maquina[k].(string); return s }
	if _, err := tx.Exec(`INSERT OR REPLACE INTO lixeira_maquinas(id,hostname,agent_id,ultimo_usuario,excluida_em,excluida_por,dados)
		VALUES(?,?,?,?,?,?,?)`, id, texto("hostname"), texto("agent_id"), texto("ultimo_usuario"), agora(), por, string(dados)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM maquinas WHERE id=?`, id); err != nil { // interfaces/logins/ramais vão junto (cascade)
		return err
	}
	return tx.Commit()
}

// RestaurarMaquina volta a máquina da lixeira, com o mesmo número (#id) se ainda estiver livre.
func (b *Banco) RestaurarMaquina(id int64) (int64, error) {
	var dados string
	if err := b.db.QueryRow(`SELECT dados FROM lixeira_maquinas WHERE id=?`, id).Scan(&dados); err != nil {
		return 0, fmt.Errorf("máquina não está na lixeira")
	}
	var c copiaMaquina
	if err := json.Unmarshal([]byte(dados), &c); err != nil {
		return 0, err
	}
	tx, err := b.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if ag, _ := c.Maquina["agent_id"].(string); ag != "" {
		var outro int64
		if tx.QueryRow(`SELECT id FROM maquinas WHERE agent_id=?`, ag).Scan(&outro) == nil {
			return 0, fmt.Errorf("o agente desta máquina já se cadastrou de novo (máquina #%d) — use Mesclar ou exclua a cópia da lixeira", outro)
		}
	}
	var ocupado int
	_ = tx.QueryRow(`SELECT COUNT(*) FROM maquinas WHERE id=?`, id).Scan(&ocupado)
	if ocupado > 0 {
		delete(c.Maquina, "id")
	}
	if err := inserirMapa(tx, "maquinas", c.Maquina, colunasDe(tx, "maquinas")); err != nil {
		return 0, err
	}
	novo := id
	if ocupado > 0 {
		_ = tx.QueryRow(`SELECT last_insert_rowid()`).Scan(&novo)
	}
	colsIf, colsLog, colsRam := colunasDe(tx, "interfaces"), colunasDe(tx, "logins"), colunasDe(tx, "ramais")
	for _, m := range c.Interfaces {
		m["maquina_id"] = novo
		_ = inserirMapa(tx, "interfaces", m, colsIf)
	}
	for _, m := range c.Logins {
		m["maquina_id"] = novo
		_ = inserirMapa(tx, "logins", m, colsLog) // ignora login de usuário que não existe mais
	}
	for _, m := range c.Ramais {
		m["maquina_id"] = novo
		delete(m, "id")
		_ = inserirMapa(tx, "ramais", m, colsRam)
	}
	for _, cid := range c.Chamados {
		_, _ = tx.Exec(`UPDATE chamados SET maquina_id=? WHERE id=? AND maquina_id IS NULL`, novo, cid)
	}
	if _, err := tx.Exec(`DELETE FROM lixeira_maquinas WHERE id=?`, id); err != nil {
		return 0, err
	}
	return novo, tx.Commit()
}

func (b *Banco) ApagarMaquinaDefinitivo(id int64) error {
	_, err := b.db.Exec(`DELETE FROM lixeira_maquinas WHERE id=?`, id)
	return err
}

// NaLixeira: o agente pertence a uma máquina excluída? (registra o envio ignorado)
func (b *Banco) NaLixeira(agentID string) bool {
	if strings.TrimSpace(agentID) == "" {
		return false
	}
	x, err := b.db.Exec(`UPDATE lixeira_maquinas SET envios_depois=envios_depois+1, ultimo_envio=? WHERE agent_id=?`, agora(), agentID)
	if err != nil {
		return false
	}
	n, _ := x.RowsAffected()
	return n > 0
}

type MaquinaExcluida struct {
	ID                                   int64
	Hostname, AgentID, UltimoUsuario     string
	ExcluidaEm, ExcluidaPor, UltimoEnvio string
	EnviosDepois                         int
}

func (e MaquinaExcluida) AindaEnviando() bool {
	return e.EnviosDepois > 0
}

func (b *Banco) MaquinasExcluidas() []MaquinaExcluida {
	rows, err := b.db.Query(`SELECT id, hostname, agent_id, ultimo_usuario, excluida_em, excluida_por, ultimo_envio, envios_depois
		FROM lixeira_maquinas ORDER BY excluida_em DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []MaquinaExcluida
	for rows.Next() {
		var x MaquinaExcluida
		if rows.Scan(&x.ID, &x.Hostname, &x.AgentID, &x.UltimoUsuario, &x.ExcluidaEm, &x.ExcluidaPor, &x.UltimoEnvio, &x.EnviosDepois) == nil {
			l = append(l, x)
		}
	}
	return l
}

func (b *Banco) QtdLixeira() int {
	var n, m int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM lixeira_maquinas`).Scan(&n)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM ramais WHERE excluido_em<>''`).Scan(&m)
	return n + m
}

// ---------- páginas ----------

func (s *Servidor) paginaLixeira(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	s.render(w, r, "lixeira", "Lixeira", "lixeira", sess, map[string]any{
		"Maquinas": s.b.MaquinasExcluidas(), "Ramais": s.b.RamaisExcluidos(), "EhAdmin": sess.Admin.EhAdmin(),
	})
}

func (s *Servidor) acaoRestaurarMaquina(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var host string
	_ = s.b.db.QueryRow(`SELECT hostname FROM lixeira_maquinas WHERE id=?`, id).Scan(&host)
	novo, err := s.b.RestaurarMaquina(id)
	if err != nil {
		redirecionar(w, r, "/lixeira", "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "restaurar máquina", fmt.Sprintf("#%d %s", novo, host))
	redirecionar(w, r, fmt.Sprintf("/maquinas/%d", novo), host+" restaurada.", nil)
}

func (s *Servidor) acaoApagarMaquina(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var host string
	_ = s.b.db.QueryRow(`SELECT hostname FROM lixeira_maquinas WHERE id=?`, id).Scan(&host)
	err := s.b.ApagarMaquinaDefinitivo(id)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "excluir máquina PERMANENTEMENTE", fmt.Sprintf("#%d %s", id, host))
	}
	redirecionar(w, r, "/lixeira", host+" excluída permanentemente.", err)
}
