package servidor

// Ramais (1.11): o agente lê o MicroSIP de cada perfil do Windows e manda as contas SIP
// (número do ramal, nome, servidor — nunca a senha) e o histórico de chamadas. Aqui o ramal
// é associado à pessoa: "Daniel Costa - Ramal 2010 - Setor TI".

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"inventario/internal/modelo"
)

const ramalDiasHistorico = 90 // histórico guardado no servidor

// GravarRamais registra as contas do MicroSIP vistas nesta máquina e o histórico de chamadas.
// Contas que sumiram da máquina ficam inativas (não são apagadas, para manter o histórico).
func (b *Banco) GravarRamais(maquinaID int64, lista []modelo.MicroSIP, versaoAgente string) error {
	// agentes antigos não sabem coletar o MicroSIP: não mexe no que já existe
	if versaoMenor(versaoAgente, "1.11.0") {
		return nil
	}
	t := agora()
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE ramais SET ativo=0 WHERE maquina_id=?`, maquinaID); err != nil {
		return err
	}
	for _, m := range lista {
		for _, c := range m.Contas {
			ramal := strings.TrimSpace(c.Usuario)
			if ramal == "" {
				ramal = strings.TrimSpace(c.AuthID)
			}
			if ramal == "" {
				continue
			}
			servidor := c.Servidor
			if servidor == "" {
				servidor = c.Dominio
			}
			if _, err := tx.Exec(`INSERT INTO ramais(maquina_id,perfil,ramal,auth_id,nome_sip,rotulo,servidor_sip,versao,caminho,erro,ativo,primeiro_visto,ultimo_visto)
				VALUES(?,?,?,?,?,?,?,?,?,?,1,?,?)
				ON CONFLICT(maquina_id,perfil,ramal) DO UPDATE SET auth_id=excluded.auth_id, nome_sip=excluded.nome_sip,
				rotulo=excluded.rotulo, servidor_sip=excluded.servidor_sip, versao=excluded.versao, caminho=excluded.caminho,
				erro=excluded.erro, ativo=1, ultimo_visto=excluded.ultimo_visto`,
				maquinaID, m.Perfil, ramal, c.AuthID, c.Nome, c.Rotulo, servidor, m.Versao, m.Caminho, m.Erro, t, t); err != nil {
				return err
			}
		}
		// o histórico do MicroSIP é do perfil, não da conta: atribui à conta principal do perfil
		if len(m.Contas) == 0 || len(m.Chamadas) == 0 {
			continue
		}
		ramal := strings.TrimSpace(m.Contas[0].Usuario)
		if ramal == "" {
			ramal = strings.TrimSpace(m.Contas[0].AuthID)
		}
		if ramal == "" {
			continue
		}
		for _, ch := range m.Chamadas {
			if ch.ID == "" {
				ch.ID = fmt.Sprintf("%d-%s", ch.Inicio, ch.Numero)
			}
			if _, err := tx.Exec(`INSERT INTO ramal_chamadas(ramal,maquina_id,call_id,numero,nome,tipo,inicio,duracao,info)
				VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(ramal,call_id) DO UPDATE SET duracao=excluded.duracao, tipo=excluded.tipo, nome=excluded.nome`,
				ramal, maquinaID, ch.ID, ch.Numero, ch.Nome, ch.Tipo, ch.Inicio, ch.Duracao, ch.Info); err != nil {
				return err
			}
		}
	}
	limite := time.Now().AddDate(0, 0, -ramalDiasHistorico).Unix()
	if _, err := tx.Exec(`DELETE FROM ramal_chamadas WHERE inicio < ?`, limite); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------- leitura ----------

type RamalMaquina struct {
	LinhaID     int64 // ramais.id (para excluir só esta ocorrência)
	MaquinaID   int64
	Hostname    string
	Perfil      string
	Servidor    string
	Versao      string
	Erro        string
	Ativo       bool
	UltimoVisto string
}

type Ramal struct {
	Numero      string
	Nome        string // nome já resolvido
	Setor       string
	OrigemNome  string // de onde veio o nome (para a tela)
	NomeAjuste  string
	SetorAjust  string
	NomeSIP     string
	Perfil      string
	Ativo       bool
	Maquinas    []RamalMaquina
	UltimoVisto string
	Duplicado   bool // o mesmo número ativo em mais de um lugar
	Feitas      int  // últimos 30 dias
	Recebidas   int
	Perdidas    int
}

// Rotulo devolve "Daniel Costa - Ramal 2010 - Setor TI".
func (r Ramal) Rotulo() string {
	p := []string{}
	if r.Nome != "" {
		p = append(p, r.Nome)
	}
	p = append(p, "Ramal "+r.Numero)
	if r.Setor != "" {
		p = append(p, "Setor "+r.Setor)
	}
	return strings.Join(p, " - ")
}

func (r Ramal) Total() int { return r.Feitas + r.Recebidas + r.Perdidas }

// nomeDoLogin transforma "daniel.costa" em "Daniel Costa".
func nomeDoLogin(login string) string {
	if i := strings.LastIndexByte(login, '\\'); i >= 0 {
		login = login[i+1:]
	}
	if i := strings.IndexByte(login, '@'); i > 0 {
		login = login[:i]
	}
	// perfis duplicados do Windows: "daniel.costa.OCUPACIONAL" / "daniel.costa.000"
	partes := strings.FieldsFunc(login, func(r rune) bool { return r == '.' || r == '_' || r == '-' || r == ' ' })
	var r []string
	for i, p := range partes {
		if i > 0 && (strings.ToUpper(p) == p && len(p) > 3 || strings.Trim(p, "0123456789") == "") {
			continue // sufixo de domínio ou número
		}
		rs := []rune(strings.ToLower(p))
		if len(rs) == 0 {
			continue
		}
		pequena := map[string]bool{"da": true, "de": true, "do": true, "das": true, "dos": true, "e": true}
		if i == 0 || !pequena[string(rs)] {
			rs[0] = unicode.ToUpper(rs[0])
		}
		r = append(r, string(rs))
	}
	return strings.Join(r, " ")
}

func soDigitos(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && c != '+' && c != ' ' {
			return false
		}
	}
	return true
}

type pessoaInfo struct{ nome, setor string }

// pessoasPorLogin: login (completo e curto) → nome/setor do cadastro de usuários.
func (b *Banco) pessoasPorLogin() map[string]pessoaInfo {
	r := map[string]pessoaInfo{}
	rows, err := b.db.Query(`SELECT u.login, u.nome, u.setor FROM usuarios u
		UNION ALL SELECT a.apelido, u.nome, u.setor FROM usuario_apelidos a JOIN usuarios u ON u.id=a.usuario_id`)
	if err != nil {
		return r
	}
	defer rows.Close()
	for rows.Next() {
		var login string
		var p pessoaInfo
		if rows.Scan(&login, &p.nome, &p.setor) != nil {
			continue
		}
		chaves := []string{strings.ToLower(login)}
		curto := login
		if i := strings.LastIndexByte(curto, '\\'); i >= 0 {
			curto = curto[i+1:]
		}
		if i := strings.IndexByte(curto, '@'); i > 0 {
			curto = curto[:i]
		}
		chaves = append(chaves, strings.ToLower(curto))
		for _, k := range chaves {
			if atual, ok := r[k]; !ok || (atual.nome == "" && p.nome != "") || (atual.setor == "" && p.setor != "") {
				if ok {
					if p.nome == "" {
						p.nome = atual.nome
					}
					if p.setor == "" {
						p.setor = atual.setor
					}
				}
				r[k] = p
			}
		}
	}
	return r
}

// perfilSemSufixo: "daniel.costa.OCUPACIONAL" → "daniel.costa"
func perfilSemSufixo(p string) string {
	partes := strings.Split(p, ".")
	for len(partes) > 1 {
		u := partes[len(partes)-1]
		if strings.ToUpper(u) == u && len(u) > 3 || strings.Trim(u, "0123456789") == "" {
			partes = partes[:len(partes)-1]
			continue
		}
		break
	}
	return strings.Join(partes, ".")
}

// Ramais lista os ramais agrupados pelo número. q filtra por nome, número, setor, máquina ou perfil.
func (b *Banco) Ramais(q string, inativos bool) ([]Ramal, error) {
	pessoas := b.pessoasPorLogin() // antes da consulta principal: o banco usa uma conexão só
	rows, err := b.db.Query(`SELECT r.ramal, r.perfil, r.nome_sip, r.servidor_sip, r.versao, r.erro, r.ativo, r.ultimo_visto,
		m.id, m.hostname, m.setor, m.ultimo_usuario_nome,
		COALESCE(a.nome,''), COALESCE(a.setor,''), r.id
		FROM ramais r JOIN maquinas m ON m.id=r.maquina_id
		LEFT JOIN ramal_ajustes a ON a.ramal=r.ramal
		WHERE r.excluido_em=''
		ORDER BY r.ativo DESC, r.ultimo_visto DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	porNumero := map[string]*Ramal{}
	var ordem []string
	for rows.Next() {
		var numero, perfil, nomeSIP, servidor, versao, erro, visto, hostname, setorMaq, nomeUltimo, ajNome, ajSetor string
		var ativo int
		var maqID, linhaID int64
		if err := rows.Scan(&numero, &perfil, &nomeSIP, &servidor, &versao, &erro, &ativo, &visto,
			&maqID, &hostname, &setorMaq, &nomeUltimo, &ajNome, &ajSetor, &linhaID); err != nil {
			return nil, err
		}
		r := porNumero[numero]
		if r == nil {
			r = &Ramal{Numero: numero, NomeAjuste: ajNome, SetorAjust: ajSetor, NomeSIP: nomeSIP, Perfil: perfil, UltimoVisto: visto}
			porNumero[numero] = r
			ordem = append(ordem, numero)
			// nome: ajuste manual > cadastro de usuários > nome no MicroSIP > nome do perfil > último usuário da máquina
			p, achou := pessoas[strings.ToLower(perfil)]
			if !achou {
				p, achou = pessoas[strings.ToLower(perfilSemSufixo(perfil))]
			}
			switch {
			case ajNome != "":
				r.Nome, r.OrigemNome = ajNome, "ajustado aqui"
			case achou && p.nome != "":
				r.Nome, r.OrigemNome = p.nome, "cadastro de usuários"
			case nomeSIP != "" && !soDigitos(nomeSIP):
				r.Nome, r.OrigemNome = nomeSIP, "nome no MicroSIP"
			case perfil != "":
				r.Nome, r.OrigemNome = nomeDoLogin(perfil), "pasta do usuário ("+perfil+")"
			case nomeUltimo != "":
				r.Nome, r.OrigemNome = nomeUltimo, "último usuário da máquina"
			}
			switch {
			case ajSetor != "":
				r.Setor = ajSetor
			case achou && p.setor != "":
				r.Setor = p.setor
			default:
				r.Setor = setorMaq
			}
		}
		if ativo == 1 {
			r.Ativo = true
		}
		r.Maquinas = append(r.Maquinas, RamalMaquina{LinhaID: linhaID, MaquinaID: maqID, Hostname: hostname, Perfil: perfil, Servidor: servidor,
			Versao: versao, Erro: erro, Ativo: ativo == 1, UltimoVisto: visto})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, r := range porNumero {
		ativos := 0
		for _, m := range r.Maquinas {
			if m.Ativo {
				ativos++
			}
		}
		r.Duplicado = ativos > 1
	}
	// chamadas dos últimos 30 dias
	desde := time.Now().AddDate(0, 0, -30).Unix()
	if cr, err := b.db.Query(`SELECT ramal, tipo, COUNT(*) FROM ramal_chamadas WHERE inicio>=? GROUP BY ramal, tipo`, desde); err == nil {
		for cr.Next() {
			var ramal string
			var tipo, n int
			if cr.Scan(&ramal, &tipo, &n) == nil {
				if r := porNumero[ramal]; r != nil {
					switch tipo {
					case 0:
						r.Feitas += n
					case 1:
						r.Recebidas += n
					default:
						r.Perdidas += n
					}
				}
			}
		}
		cr.Close()
	}
	q = strings.ToLower(strings.TrimSpace(q))
	var lista []Ramal
	for _, n := range ordem {
		r := porNumero[n]
		if !r.Ativo && !inativos {
			continue
		}
		if q != "" {
			alvo := strings.ToLower(r.Rotulo() + " " + r.Perfil + " " + r.NomeSIP)
			for _, m := range r.Maquinas {
				alvo += " " + strings.ToLower(m.Hostname+" "+m.Perfil)
			}
			if !strings.Contains(alvo, q) {
				continue
			}
		}
		lista = append(lista, *r)
	}
	sort.SliceStable(lista, func(i, j int) bool {
		if lista[i].Ativo != lista[j].Ativo {
			return lista[i].Ativo
		}
		return strings.ToLower(lista[i].Nome) < strings.ToLower(lista[j].Nome)
	})
	return lista, nil
}

type ChamadaRamal struct {
	Inicio    time.Time
	Tipo      int
	Numero    string
	Nome      string // nome gravado pelo MicroSIP
	Interno   string // se o número é outro ramal, o rótulo dele
	Duracao   int
	MaquinaID int64
	Hostname  string
}

func (c ChamadaRamal) TipoTexto() string {
	switch c.Tipo {
	case 0:
		return "Feita"
	case 1:
		return "Recebida"
	case 2:
		return "Perdida"
	}
	return "Tipo " + strconv.Itoa(c.Tipo)
}

func (c ChamadaRamal) DuracaoTexto() string {
	if c.Duracao <= 0 {
		return "—"
	}
	return fmt.Sprintf("%d:%02d", c.Duracao/60, c.Duracao%60)
}

func (b *Banco) ChamadasDoRamal(ramal string, tipo, limite int) ([]ChamadaRamal, error) {
	q := `SELECT c.inicio, c.tipo, c.numero, c.nome, c.duracao, COALESCE(c.maquina_id,0), COALESCE(m.hostname,'')
		FROM ramal_chamadas c LEFT JOIN maquinas m ON m.id=c.maquina_id WHERE c.ramal=?`
	args := []any{ramal}
	if tipo >= 0 {
		q += ` AND c.tipo=?`
		args = append(args, tipo)
	}
	q += ` ORDER BY c.inicio DESC LIMIT ?`
	args = append(args, limite)
	rows, err := b.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var r []ChamadaRamal
	for rows.Next() {
		var c ChamadaRamal
		var ini int64
		if err := rows.Scan(&ini, &c.Tipo, &c.Numero, &c.Nome, &c.Duracao, &c.MaquinaID, &c.Hostname); err != nil {
			return nil, err
		}
		c.Inicio = time.Unix(ini, 0)
		r = append(r, c)
	}
	return r, rows.Err()
}

// numeroDoSIP: "sip:2011@pabx" / "2011@pabx" / "<sip:2011@x>" → "2011"
func numeroDoSIP(s string) string {
	s = strings.Trim(strings.TrimSpace(s), "<>\"")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "sips:"), "sip:")
	if i := strings.IndexByte(s, '@'); i >= 0 {
		s = s[:i]
	}
	return s
}

func (b *Banco) AjustarRamal(ramal, nome, setor string) error {
	nome, setor = strings.TrimSpace(nome), strings.TrimSpace(setor)
	if nome == "" && setor == "" {
		_, err := b.db.Exec(`DELETE FROM ramal_ajustes WHERE ramal=?`, ramal)
		return err
	}
	_, err := b.db.Exec(`INSERT INTO ramal_ajustes(ramal,nome,setor) VALUES(?,?,?)
		ON CONFLICT(ramal) DO UPDATE SET nome=excluded.nome, setor=excluded.setor`, ramal, nome, setor)
	return err
}

// ---------- páginas ----------

func (s *Servidor) paginaRamais(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	q := r.URL.Query().Get("q")
	inativos := r.URL.Query().Get("inativos") == "1"
	lista, err := s.b.Ramais(q, inativos)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var ativos, comMicroSIP int
	todos, _ := s.b.Ramais("", true)
	for _, x := range todos {
		if x.Ativo {
			ativos++
		}
	}
	_ = s.b.db.QueryRow(`SELECT COUNT(DISTINCT maquina_id) FROM ramais WHERE ativo=1`).Scan(&comMicroSIP)
	semAgente := 0
	if vr, err := s.b.db.Query(`SELECT versao_agente FROM maquinas WHERE versao_agente<>''`); err == nil {
		for vr.Next() {
			var v string
			if vr.Scan(&v) == nil && versaoMenor(v, "1.11.0") {
				semAgente++
			}
		}
		vr.Close()
	}
	s.render(w, r, "ramais", "Ramais", "ramais", sess, map[string]any{
		"Duplicados": s.b.RamaisDuplicados(), "Excluidos": len(s.b.RamaisExcluidos()),
		"Ramais": lista, "Q": q, "Inativos": inativos, "TotalAtivos": ativos, "Maquinas": comMicroSIP, "AgentesAntigos": semAgente,
	})
}

func (s *Servidor) paginaRamal(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	numero := r.PathValue("ramal")
	todos, err := s.b.Ramais("", true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var atual *Ramal
	for i := range todos {
		if todos[i].Numero == numero {
			atual = &todos[i]
		}
	}
	if atual == nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "ramal", atual.Rotulo(), "ramais", sess, map[string]any{
		"R": atual,
	})
}

func (s *Servidor) acaoAjustarRamal(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	numero := r.PathValue("ramal")
	err := s.b.AjustarRamal(numero, r.FormValue("nome"), r.FormValue("setor"))
	redirecionar(w, r, "/ramais/"+numero, "Ramal atualizado.", err)
}

func (s *Servidor) exportarRamaisCSV(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	lista, err := s.b.Ramais(r.URL.Query().Get("q"), true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ramais.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	_ = cw.Write([]string{"Ramal", "Nome", "Setor", "Descrição", "Máquinas", "Perfil do Windows", "Ativo", "Visto por último"})
	for _, x := range lista {
		var maqs []string
		for _, m := range x.Maquinas {
			maqs = append(maqs, m.Hostname)
		}
		ativo := "não"
		if x.Ativo {
			ativo = "sim"
		}
		_ = cw.Write([]string{x.Numero, x.Nome, x.Setor, x.Rotulo(), strings.Join(maqs, ", "), x.Perfil, ativo,
			formatarData(x.UltimoVisto)})
	}
	cw.Flush()
}

// ---------- exclusão / lixeira de ramais (1.16) ----------

// ExcluirRamalLinha manda uma ocorrência (máquina + usuário) para a lixeira. Enquanto estiver lá,
// os envios do agente atualizam a linha, mas ela não aparece nas listas.
func (b *Banco) ExcluirRamalLinha(id int64, por string) error {
	_, err := b.db.Exec(`UPDATE ramais SET excluido_em=?, excluido_por=? WHERE id=? AND excluido_em=''`, agora(), por, id)
	return err
}

// ExcluirRamalNumero manda todas as ocorrências de um número para a lixeira.
func (b *Banco) ExcluirRamalNumero(numero, por string) error {
	_, err := b.db.Exec(`UPDATE ramais SET excluido_em=?, excluido_por=? WHERE ramal=? AND excluido_em=''`, agora(), por, numero)
	return err
}

func (b *Banco) RestaurarRamal(id int64) error {
	_, err := b.db.Exec(`UPDATE ramais SET excluido_em='', excluido_por='' WHERE id=?`, id)
	return err
}

func (b *Banco) ApagarRamal(id int64) error {
	_, err := b.db.Exec(`DELETE FROM ramais WHERE id=? AND excluido_em<>''`, id)
	return err
}

type RamalExcluido struct {
	ID                                   int64
	Numero, Perfil, NomeSIP, Hostname    string
	MaquinaID                            int64
	ExcluidoEm, ExcluidoPor, UltimoVisto string
	AindaAtivo                           bool // o MicroSIP da máquina continua com a conta
}

func (b *Banco) RamaisExcluidos() []RamalExcluido {
	rows, err := b.db.Query(`SELECT r.id, r.ramal, r.perfil, r.nome_sip, m.hostname, m.id, r.excluido_em, r.excluido_por, r.ultimo_visto, r.ativo
		FROM ramais r JOIN maquinas m ON m.id=r.maquina_id WHERE r.excluido_em<>'' ORDER BY r.excluido_em DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []RamalExcluido
	for rows.Next() {
		var x RamalExcluido
		var ativo int
		if rows.Scan(&x.ID, &x.Numero, &x.Perfil, &x.NomeSIP, &x.Hostname, &x.MaquinaID, &x.ExcluidoEm, &x.ExcluidoPor, &x.UltimoVisto, &ativo) == nil {
			x.AindaAtivo = ativo == 1
			l = append(l, x)
		}
	}
	return l
}

// RamaisDuplicados: números ativos em mais de um lugar.
func (b *Banco) RamaisDuplicados() []Ramal {
	todos, _ := b.Ramais("", false)
	var r []Ramal
	for _, x := range todos {
		if x.Duplicado {
			r = append(r, x)
		}
	}
	return r
}

func (s *Servidor) acaoExcluirRamalLinha(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var numero, host string
	_ = s.b.db.QueryRow(`SELECT r.ramal, m.hostname FROM ramais r JOIN maquinas m ON m.id=r.maquina_id WHERE r.id=?`, id).Scan(&numero, &host)
	err := s.b.ExcluirRamalLinha(id, sess.Admin.Usuario)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "excluir ramal", numero+" em "+host)
	}
	destino := "/ramais/" + numero
	var resta int
	_ = s.b.db.QueryRow(`SELECT COUNT(*) FROM ramais WHERE ramal=? AND excluido_em=''`, numero).Scan(&resta)
	if resta == 0 {
		destino = "/ramais"
	}
	redirecionar(w, r, destino, "Ramal "+numero+" em "+host+" foi para a lixeira (dá para restaurar em Inventário → Lixeira).", err)
}

func (s *Servidor) acaoExcluirRamal(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	numero := r.PathValue("ramal")
	err := s.b.ExcluirRamalNumero(numero, sess.Admin.Usuario)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "excluir ramal", numero)
	}
	redirecionar(w, r, "/ramais", "Ramal "+numero+" foi para a lixeira (dá para restaurar em Inventário → Lixeira).", err)
}

func (s *Servidor) acaoRestaurarRamal(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var numero string
	_ = s.b.db.QueryRow(`SELECT ramal FROM ramais WHERE id=?`, id).Scan(&numero)
	err := s.b.RestaurarRamal(id)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "restaurar ramal", numero)
	}
	redirecionar(w, r, "/lixeira#ramais", "Ramal "+numero+" restaurado.", err)
}

func (s *Servidor) acaoApagarRamal(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var numero string
	_ = s.b.db.QueryRow(`SELECT ramal FROM ramais WHERE id=?`, id).Scan(&numero)
	err := s.b.ApagarRamal(id)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "apagar ramal permanentemente", numero)
	}
	redirecionar(w, r, "/lixeira#ramais", "Ramal "+numero+" apagado permanentemente.", err)
}
