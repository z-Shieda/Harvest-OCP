package servidor

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"github.com/tetratelabs/wazero"

	"inventario/internal/modelo"
)

// A biblioteca SQLite roda em WebAssembly (wazero). O modo "compilador" (padrão) gera
// código nativo e, sob carga, pode estourar a pilha do processo. O modo "interpretador"
// é um pouco mais lento, porém estável sob qualquer carga — a escolha certa para um
// servidor que recebe muitos agentes ao mesmo tempo.
var configurarWazero sync.Once

func usarInterpretador() {
	configurarWazero.Do(func() {
		sqlite3.RuntimeConfig = wazero.NewRuntimeConfigInterpreter().WithMemoryLimitPages(4096)
	})
}

const esquema = `
CREATE TABLE IF NOT EXISTS maquinas (
	id INTEGER PRIMARY KEY,
	agent_id TEXT NOT NULL UNIQUE,
	hostname TEXT NOT NULL DEFAULT '',
	dominio TEXT NOT NULL DEFAULT '',
	fabricante TEXT NOT NULL DEFAULT '',
	modelo TEXT NOT NULL DEFAULT '',
	serial TEXT NOT NULL DEFAULT '',
	hw_uuid TEXT NOT NULL DEFAULT '',
	bios TEXT NOT NULL DEFAULT '',
	so TEXT NOT NULL DEFAULT '',
	so_versao TEXT NOT NULL DEFAULT '',
	so_build TEXT NOT NULL DEFAULT '',
	so_arquitetura TEXT NOT NULL DEFAULT '',
	so_instalacao TEXT NOT NULL DEFAULT '',
	ultimo_boot TEXT NOT NULL DEFAULT '',
	cpu TEXT NOT NULL DEFAULT '',
	cpu_nucleos INTEGER NOT NULL DEFAULT 0,
	cpu_threads INTEGER NOT NULL DEFAULT 0,
	ram_bytes INTEGER NOT NULL DEFAULT 0,
	disco_total INTEGER NOT NULL DEFAULT 0,
	discos_resumo TEXT NOT NULL DEFAULT '',
	gpu TEXT NOT NULL DEFAULT '',
	ips TEXT NOT NULL DEFAULT '',
	macs TEXT NOT NULL DEFAULT '',
	usuario_atual TEXT NOT NULL DEFAULT '',
	ultimo_usuario TEXT NOT NULL DEFAULT '',
	ultimo_usuario_nome TEXT NOT NULL DEFAULT '',
	ultimo_usuario_em TEXT NOT NULL DEFAULT '',
	versao_agente TEXT NOT NULL DEFAULT '',
	ip_origem TEXT NOT NULL DEFAULT '',
	primeiro_contato TEXT NOT NULL DEFAULT '',
	ultimo_contato TEXT NOT NULL DEFAULT '',
	dados_json TEXT NOT NULL DEFAULT '{}',
	patrimonio TEXT NOT NULL DEFAULT '',
	setor TEXT NOT NULL DEFAULT '',
	responsavel TEXT NOT NULL DEFAULT '',
	localizacao TEXT NOT NULL DEFAULT '',
	observacoes TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS interfaces (
	maquina_id INTEGER NOT NULL REFERENCES maquinas(id) ON DELETE CASCADE,
	mac TEXT NOT NULL,
	nome TEXT NOT NULL DEFAULT '',
	ipv4 TEXT NOT NULL DEFAULT '',
	conectada INTEGER NOT NULL DEFAULT 0,
	fisica INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_interfaces_mac ON interfaces(mac);
CREATE INDEX IF NOT EXISTS idx_interfaces_maq ON interfaces(maquina_id);
CREATE TABLE IF NOT EXISTS usuarios (
	id INTEGER PRIMARY KEY,
	login TEXT NOT NULL UNIQUE COLLATE NOCASE,
	nome TEXT NOT NULL DEFAULT '',
	email TEXT NOT NULL DEFAULT '',
	setor TEXT NOT NULL DEFAULT '',
	observacoes TEXT NOT NULL DEFAULT '',
	primeiro_visto TEXT NOT NULL DEFAULT '',
	ultimo_visto TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS usuario_apelidos (
	apelido TEXT PRIMARY KEY COLLATE NOCASE,
	usuario_id INTEGER NOT NULL REFERENCES usuarios(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS logins (
	usuario_id INTEGER NOT NULL REFERENCES usuarios(id) ON DELETE CASCADE,
	maquina_id INTEGER NOT NULL REFERENCES maquinas(id) ON DELETE CASCADE,
	primeiro TEXT NOT NULL,
	ultimo TEXT NOT NULL,
	PRIMARY KEY (usuario_id, maquina_id)
);
CREATE TABLE IF NOT EXISTS macs_ignorados (
	mac TEXT PRIMARY KEY,
	motivo TEXT NOT NULL DEFAULT '',
	criado_em TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS alertas_enviados (
	chave TEXT PRIMARY KEY,
	criado_em TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS admins (
	id INTEGER PRIMARY KEY,
	usuario TEXT NOT NULL UNIQUE COLLATE NOCASE,
	senha TEXT NOT NULL,
	criado_em TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS sessoes (
	token TEXT PRIMARY KEY,
	admin_id INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
	csrf TEXT NOT NULL,
	expira TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS config (
	chave TEXT PRIMARY KEY,
	valor TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS auditoria (
	id INTEGER PRIMARY KEY,
	em TEXT NOT NULL,
	admin TEXT NOT NULL,
	acao TEXT NOT NULL,
	detalhe TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS filtros (
	id INTEGER PRIMARY KEY,
	nome TEXT NOT NULL,
	campo TEXT NOT NULL,
	operador TEXT NOT NULL,
	valor TEXT NOT NULL DEFAULT ''
);
`

type Banco struct{ db *sql.DB }

func AbrirBanco(caminho string) (*Banco, error) {
	usarInterpretador()
	dsn := "file:" + caminho + "?_pragma=foreign_keys(1)&_pragma=journal_mode(wal)&_pragma=busy_timeout(10000)&_pragma=synchronous(normal)"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(esquema); err != nil {
		return nil, fmt.Errorf("criando tabelas: %w", err)
	}
	// Migrações de esquema para bancos criados por versões anteriores (ADD COLUMN é ignorado
	// com erro se a coluna já existe).
	for _, alter := range []string{
		`ALTER TABLE maquinas ADD COLUMN servidor_agente TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = db.Exec(alter)
	}
	return &Banco{db: db}, nil
}

func (b *Banco) Fechar() error { return b.db.Close() }

// Backup grava uma cópia consistente do banco no arquivo informado (com o servidor em funcionamento).
func (b *Banco) Backup(destino string) error {
	_, err := b.db.Exec(`VACUUM INTO ?`, destino)
	return err
}

func agora() string { return time.Now().UTC().Format(time.RFC3339) }

// ---------------- Configurações ----------------

func (b *Banco) Config(chave string) string {
	var v string
	_ = b.db.QueryRow(`SELECT valor FROM config WHERE chave=?`, chave).Scan(&v)
	return v
}

func (b *Banco) DefinirConfig(chave, valor string) error {
	_, err := b.db.Exec(`INSERT INTO config(chave,valor) VALUES(?,?) ON CONFLICT(chave) DO UPDATE SET valor=excluded.valor`, chave, valor)
	return err
}

func (b *Banco) Auditar(admin, acao, detalhe string) {
	_, _ = b.db.Exec(`INSERT INTO auditoria(em,admin,acao,detalhe) VALUES(?,?,?,?)`, agora(), admin, acao, detalhe)
}

// ---------------- Normalizações ----------------

func normalizarMAC(m string) string {
	m = strings.ToUpper(strings.TrimSpace(m))
	m = strings.ReplaceAll(m, "-", ":")
	if len(m) == 12 && !strings.Contains(m, ":") {
		var p []string
		for i := 0; i < 12; i += 2 {
			p = append(p, m[i:i+2])
		}
		m = strings.Join(p, ":")
	}
	return m
}

func macValido(m string) bool {
	return len(m) == 17 && m != "00:00:00:00:00:00" && m != "FF:FF:FF:FF:FF:FF"
}

// loginSistema identifica contas que não representam pessoas.
func loginSistema(l string) bool {
	if l == "" {
		return true
	}
	u := strings.ToUpper(l)
	if i := strings.LastIndex(u, `\`); i >= 0 {
		u = u[i+1:]
	}
	if strings.HasSuffix(u, "$") || strings.HasPrefix(u, "DWM-") || strings.HasPrefix(u, "UMFD-") {
		return true
	}
	switch u {
	case "SYSTEM", "SISTEMA", "LOCAL SERVICE", "NETWORK SERVICE", "DEFAULTUSER0", "DEFAULTUSER100000":
		return true
	}
	return false
}

// chaveCurta devolve o nome do usuário sem domínio, usado para sugerir duplicados.
func chaveCurta(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if i := strings.LastIndex(l, `\`); i >= 0 {
		l = l[i+1:]
	}
	if i := strings.Index(l, "@"); i >= 0 {
		l = l[:i]
	}
	return l
}

// ---------------- Recebimento de relatório ----------------

type ResultadoIngestao struct {
	MaquinaID   int64
	Nova        bool
	Duplicados  []GrupoMAC // somente grupos envolvendo esta máquina
	NovosAvisos []GrupoMAC // grupos que ainda não tinham sido avisados
}

func (b *Banco) Ingerir(r *modelo.Relatorio, ipOrigem string) (*ResultadoIngestao, error) {
	if strings.TrimSpace(r.AgentID) == "" {
		return nil, errors.New("agent_id vazio")
	}
	bruto, _ := json.Marshal(r)
	t := agora()

	var discoTotal uint64
	var resumo []string
	for _, d := range r.Discos {
		discoTotal += d.Tamanho
		resumo = append(resumo, strings.TrimSpace(fmt.Sprintf("%s %s %.0fGB", d.Tipo, d.Modelo, float64(d.Tamanho)/1e9)))
	}
	var ips, macs []string
	for _, n := range r.Redes {
		if !n.Fisica {
			continue
		}
		m := normalizarMAC(n.MAC)
		if macValido(m) {
			macs = append(macs, m)
		}
		ips = append(ips, n.IPv4...)
	}

	// usuários conectados agora
	var atuais []string
	vistos := map[string]bool{}
	for _, u := range append([]string{r.UsuarioAtual}, r.UsuariosSessao...) {
		u = strings.TrimSpace(u)
		if loginSistema(u) || vistos[strings.ToLower(u)] {
			continue
		}
		vistos[strings.ToLower(u)] = true
		atuais = append(atuais, u)
	}
	ultimoLogon := strings.TrimSpace(r.UltimoLogon)
	if loginSistema(ultimoLogon) {
		ultimoLogon = ""
	}

	tx, err := b.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res := &ResultadoIngestao{}
	err = tx.QueryRow(`SELECT id FROM maquinas WHERE agent_id=?`, r.AgentID).Scan(&res.MaquinaID)
	if errors.Is(err, sql.ErrNoRows) {
		x, err := tx.Exec(`INSERT INTO maquinas(agent_id, primeiro_contato) VALUES(?,?)`, r.AgentID, t)
		if err != nil {
			return nil, err
		}
		res.MaquinaID, _ = x.LastInsertId()
		res.Nova = true
	} else if err != nil {
		return nil, err
	}

	_, err = tx.Exec(`UPDATE maquinas SET hostname=?, dominio=?, fabricante=?, modelo=?, serial=?, hw_uuid=?, bios=?,
		so=?, so_versao=?, so_build=?, so_arquitetura=?, so_instalacao=?, ultimo_boot=?, cpu=?, cpu_nucleos=?, cpu_threads=?,
		ram_bytes=?, disco_total=?, discos_resumo=?, gpu=?, ips=?, macs=?, usuario_atual=?, versao_agente=?, ip_origem=?,
		ultimo_contato=?, dados_json=?, servidor_agente=? WHERE id=?`,
		r.Hostname, r.Dominio, r.Fabricante, r.Modelo, r.Serial, r.HWUUID, r.BIOS,
		r.SOName, r.SOVersao, r.SOBuild, r.SOArquitetura, r.SOInstalacao, r.UltimoBoot, r.CPU, r.CPUNucleos, r.CPUThreads,
		int64(r.RAMBytes), int64(discoTotal), strings.Join(resumo, " | "), strings.Join(r.GPUs, " | "),
		strings.Join(ips, ", "), strings.Join(macs, ", "), strings.Join(atuais, ", "), r.AgentVersion, ipOrigem,
		t, string(bruto), strings.TrimRight(r.ServidorAtual, "/"), res.MaquinaID)
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(`DELETE FROM interfaces WHERE maquina_id=?`, res.MaquinaID); err != nil {
		return nil, err
	}
	for _, n := range r.Redes {
		m := normalizarMAC(n.MAC)
		if !macValido(m) {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO interfaces(maquina_id,mac,nome,ipv4,conectada,fisica) VALUES(?,?,?,?,?,?)`,
			res.MaquinaID, m, n.Nome, strings.Join(n.IPv4, ", "), n.Conectada, n.Fisica); err != nil {
			return nil, err
		}
	}

	// registra usuários e logins
	registrar := func(login string, atual bool) (string, error) {
		uid, canonico, err := resolverUsuario(tx, login, t)
		if err != nil {
			return "", err
		}
		ultimo := t
		if !atual {
			// apenas sabemos que foi o último a entrar; não atualiza a data se já existir
			_, err = tx.Exec(`INSERT INTO logins(usuario_id,maquina_id,primeiro,ultimo) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, uid, res.MaquinaID, t, ultimo)
			return canonico, err
		}
		_, err = tx.Exec(`INSERT INTO logins(usuario_id,maquina_id,primeiro,ultimo) VALUES(?,?,?,?)
			ON CONFLICT(usuario_id,maquina_id) DO UPDATE SET ultimo=excluded.ultimo`, uid, res.MaquinaID, t, ultimo)
		if err == nil {
			_, err = tx.Exec(`UPDATE usuarios SET ultimo_visto=? WHERE id=?`, t, uid)
		}
		return canonico, err
	}
	var ultimoUsuario, ultimoNome, ultimoEm string
	for i, u := range atuais {
		c, err := registrar(u, true)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			ultimoUsuario, ultimoEm = c, t
		}
	}
	if ultimoLogon != "" {
		c, err := registrar(ultimoLogon, false)
		if err != nil {
			return nil, err
		}
		if ultimoUsuario == "" {
			ultimoUsuario, ultimoNome = c, r.UltimoLogonNome
		} else if strings.EqualFold(c, ultimoUsuario) {
			ultimoNome = r.UltimoLogonNome
		}
	}
	if ultimoUsuario != "" {
		if ultimoEm == "" {
			// mantém a última data em que o usuário foi visto conectado nesta máquina
			_ = tx.QueryRow(`SELECT l.ultimo FROM logins l JOIN usuarios u ON u.id=l.usuario_id WHERE l.maquina_id=? AND u.login=?`,
				res.MaquinaID, ultimoUsuario).Scan(&ultimoEm)
		}
		if _, err := tx.Exec(`UPDATE maquinas SET ultimo_usuario=?, ultimo_usuario_nome=?, ultimo_usuario_em=? WHERE id=?`,
			ultimoUsuario, ultimoNome, ultimoEm, res.MaquinaID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// verificação de MAC duplicado
	grupos, err := b.DuplicadosMAC()
	if err != nil {
		return res, nil
	}
	for _, g := range grupos {
		envolve := false
		for _, m := range g.Maquinas {
			if m.ID == res.MaquinaID {
				envolve = true
			}
		}
		if !envolve {
			continue
		}
		res.Duplicados = append(res.Duplicados, g)
		x, err := b.db.Exec(`INSERT INTO alertas_enviados(chave,criado_em) VALUES(?,?) ON CONFLICT DO NOTHING`, g.Chave(), t)
		if err == nil {
			if n, _ := x.RowsAffected(); n > 0 {
				res.NovosAvisos = append(res.NovosAvisos, g)
			}
		}
	}
	return res, nil
}

func resolverUsuario(tx *sql.Tx, login, t string) (int64, string, error) {
	var id int64
	var canonico string
	err := tx.QueryRow(`SELECT u.id, u.login FROM usuario_apelidos a JOIN usuarios u ON u.id=a.usuario_id WHERE a.apelido=?`, login).Scan(&id, &canonico)
	if err == nil {
		return id, canonico, nil
	}
	err = tx.QueryRow(`SELECT id, login FROM usuarios WHERE login=?`, login).Scan(&id, &canonico)
	if err == nil {
		return id, canonico, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, "", err
	}
	x, err := tx.Exec(`INSERT INTO usuarios(login,primeiro_visto,ultimo_visto) VALUES(?,?,?)`, login, t, t)
	if err != nil {
		return 0, "", err
	}
	id, _ = x.LastInsertId()
	return id, login, nil
}

// ---------------- Máquinas ----------------

type Maquina struct {
	ID                                                 int64
	AgentID, Hostname, Dominio, Fabricante, Modelo     string
	Serial, HWUUID, BIOS, SO, SOVersao, SOBuild, SOArq string
	SOInstalacao, UltimoBoot, CPU                      string
	CPUNucleos, CPUThreads                             int
	RAM, DiscoTotal                                    int64
	DiscosResumo, GPU, IPs, MACs                       string
	UsuarioAtual, UltimoUsuario, UltimoUsuarioNome     string
	UltimoUsuarioEm, VersaoAgente, IPOrigem            string
	PrimeiroContato, UltimoContato, DadosJSON          string
	Patrimonio, Setor, Responsavel, Localizacao, Obs   string
	Duplicada                                          bool
}

const colunasMaquina = `id, agent_id, hostname, dominio, fabricante, modelo, serial, hw_uuid, bios, so, so_versao, so_build,
	so_arquitetura, so_instalacao, ultimo_boot, cpu, cpu_nucleos, cpu_threads, ram_bytes, disco_total, discos_resumo, gpu,
	ips, macs, usuario_atual, ultimo_usuario, ultimo_usuario_nome, ultimo_usuario_em, versao_agente, ip_origem,
	primeiro_contato, ultimo_contato, dados_json, patrimonio, setor, responsavel, localizacao, observacoes`

type scanner interface{ Scan(...any) error }

func lerMaquina(s scanner) (*Maquina, error) {
	m := &Maquina{}
	err := s.Scan(&m.ID, &m.AgentID, &m.Hostname, &m.Dominio, &m.Fabricante, &m.Modelo, &m.Serial, &m.HWUUID, &m.BIOS,
		&m.SO, &m.SOVersao, &m.SOBuild, &m.SOArq, &m.SOInstalacao, &m.UltimoBoot, &m.CPU, &m.CPUNucleos, &m.CPUThreads,
		&m.RAM, &m.DiscoTotal, &m.DiscosResumo, &m.GPU, &m.IPs, &m.MACs, &m.UsuarioAtual, &m.UltimoUsuario,
		&m.UltimoUsuarioNome, &m.UltimoUsuarioEm, &m.VersaoAgente, &m.IPOrigem, &m.PrimeiroContato, &m.UltimoContato,
		&m.DadosJSON, &m.Patrimonio, &m.Setor, &m.Responsavel, &m.Localizacao, &m.Obs)
	return m, err
}

func (b *Banco) Maquinas(busca string) ([]*Maquina, error) {
	q := `SELECT ` + colunasMaquina + ` FROM maquinas`
	var args []any
	if busca = strings.TrimSpace(busca); busca != "" {
		like := "%" + busca + "%"
		q += ` WHERE hostname LIKE ? OR ultimo_usuario LIKE ? OR usuario_atual LIKE ? OR ips LIKE ? OR macs LIKE ? OR serial LIKE ?
			OR patrimonio LIKE ? OR setor LIKE ? OR responsavel LIKE ? OR modelo LIKE ? OR so LIKE ? OR cpu LIKE ? OR localizacao LIKE ?`
		for i := 0; i < 13; i++ {
			args = append(args, like)
		}
	}
	q += ` ORDER BY hostname COLLATE NOCASE, id`
	rows, err := b.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lista []*Maquina
	for rows.Next() {
		m, err := lerMaquina(rows)
		if err != nil {
			return nil, err
		}
		lista = append(lista, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	dup, _ := b.maquinasComMACDuplicado()
	for _, m := range lista {
		m.Duplicada = dup[m.ID]
	}
	return lista, nil
}

func (b *Banco) Maquina(id int64) (*Maquina, error) {
	m, err := lerMaquina(b.db.QueryRow(`SELECT `+colunasMaquina+` FROM maquinas WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	dup, _ := b.maquinasComMACDuplicado()
	m.Duplicada = dup[m.ID]
	return m, nil
}

func (b *Banco) AtualizarMaquina(id int64, patrimonio, setor, responsavel, localizacao, obs string) error {
	_, err := b.db.Exec(`UPDATE maquinas SET patrimonio=?, setor=?, responsavel=?, localizacao=?, observacoes=? WHERE id=?`,
		patrimonio, setor, responsavel, localizacao, obs, id)
	return err
}

func (b *Banco) ExcluirMaquina(id int64) error {
	_, err := b.db.Exec(`DELETE FROM maquinas WHERE id=?`, id)
	return err
}

// MesclarMaquinas move o histórico de logins e os dados cadastrais de origem para destino e exclui a origem.
func (b *Banco) MesclarMaquinas(origem, destino int64) error {
	if origem == destino {
		return errors.New("selecione máquinas diferentes")
	}
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM maquinas WHERE id IN (?,?)`, origem, destino).Scan(&n); err != nil || n != 2 {
		return errors.New("máquina não encontrada")
	}
	for _, c := range []string{"patrimonio", "setor", "responsavel", "localizacao", "observacoes"} {
		if _, err := tx.Exec(`UPDATE maquinas SET `+c+` = (SELECT `+c+` FROM maquinas WHERE id=?) WHERE id=? AND `+c+`=''`, origem, destino); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE maquinas SET primeiro_contato = MIN(primeiro_contato, (SELECT primeiro_contato FROM maquinas WHERE id=?)) WHERE id=?`, origem, destino); err != nil {
		return err
	}
	// Se a origem comunicou mais recentemente (ex.: é a instalação nova), o registro mantido herda
	// o agente e os dados coletados dela, para que os próximos envios continuem caindo nele.
	var contatoOrig, contatoDest, agenteOrig string
	_ = tx.QueryRow(`SELECT ultimo_contato, agent_id FROM maquinas WHERE id=?`, origem).Scan(&contatoOrig, &agenteOrig)
	_ = tx.QueryRow(`SELECT ultimo_contato FROM maquinas WHERE id=?`, destino).Scan(&contatoDest)
	herdar := contatoOrig > contatoDest || (contatoOrig == contatoDest && origem > destino)
	if herdar {
		cols := `hostname, dominio, fabricante, modelo, serial, hw_uuid, bios, so, so_versao, so_build, so_arquitetura, so_instalacao,
			ultimo_boot, cpu, cpu_nucleos, cpu_threads, ram_bytes, disco_total, discos_resumo, gpu, ips, macs, usuario_atual,
			ultimo_usuario, ultimo_usuario_nome, ultimo_usuario_em, versao_agente, ip_origem, ultimo_contato, dados_json`
		for _, q := range []string{
			`DELETE FROM interfaces WHERE maquina_id=?2`,
			`UPDATE interfaces SET maquina_id=?2 WHERE maquina_id=?1`,
			`UPDATE maquinas SET (` + cols + `) = (SELECT ` + cols + ` FROM maquinas WHERE id=?1) WHERE id=?2`,
		} {
			if _, err := tx.Exec(q, origem, destino); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO logins(usuario_id,maquina_id,primeiro,ultimo)
		SELECT usuario_id, ?, primeiro, ultimo FROM logins WHERE maquina_id=?
		ON CONFLICT(usuario_id,maquina_id) DO UPDATE SET primeiro=MIN(primeiro,excluded.primeiro), ultimo=MAX(ultimo,excluded.ultimo)`, destino, origem); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM maquinas WHERE id=?`, origem); err != nil {
		return err
	}
	if herdar {
		if _, err := tx.Exec(`UPDATE maquinas SET agent_id=? WHERE id=?`, agenteOrig, destino); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type Interface struct {
	MaquinaID         int64
	Hostname          string
	MAC, Nome, IPv4   string
	Conectada, Fisica bool
	Ignorado          bool
}

func (b *Banco) Interfaces(maquinaID int64) ([]Interface, error) {
	q := `SELECT i.maquina_id, m.hostname, i.mac, i.nome, i.ipv4, i.conectada, i.fisica, (SELECT COUNT(*) FROM macs_ignorados g WHERE g.mac=i.mac)
		FROM interfaces i JOIN maquinas m ON m.id=i.maquina_id`
	var args []any
	if maquinaID > 0 {
		q += ` WHERE i.maquina_id=?`
		args = append(args, maquinaID)
	}
	q += ` ORDER BY m.hostname COLLATE NOCASE, i.fisica DESC, i.nome`
	rows, err := b.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []Interface
	for rows.Next() {
		var i Interface
		var ig int
		if err := rows.Scan(&i.MaquinaID, &i.Hostname, &i.MAC, &i.Nome, &i.IPv4, &i.Conectada, &i.Fisica, &ig); err != nil {
			return nil, err
		}
		i.Ignorado = ig > 0
		l = append(l, i)
	}
	return l, rows.Err()
}

// ---------------- Duplicados ----------------

type GrupoMAC struct {
	MAC      string
	Maquinas []*Maquina
}

func (g GrupoMAC) Chave() string {
	var ids []string
	for _, m := range g.Maquinas {
		ids = append(ids, fmt.Sprint(m.ID))
	}
	sort.Strings(ids)
	return g.MAC + "|" + strings.Join(ids, ",")
}

func (b *Banco) DuplicadosMAC() ([]GrupoMAC, error) {
	rows, err := b.db.Query(`SELECT mac, GROUP_CONCAT(DISTINCT maquina_id) FROM interfaces
		WHERE fisica=1 AND mac NOT IN (SELECT mac FROM macs_ignorados)
		GROUP BY mac HAVING COUNT(DISTINCT maquina_id) > 1 ORDER BY mac`)
	if err != nil {
		return nil, err
	}
	type par struct{ mac, ids string }
	var pares []par
	for rows.Next() {
		var p par
		if err := rows.Scan(&p.mac, &p.ids); err != nil {
			rows.Close()
			return nil, err
		}
		pares = append(pares, p)
	}
	rows.Close()
	var grupos []GrupoMAC
	for _, p := range pares {
		g := GrupoMAC{MAC: p.mac}
		for _, s := range strings.Split(p.ids, ",") {
			var id int64
			fmt.Sscan(s, &id)
			if m, err := lerMaquina(b.db.QueryRow(`SELECT `+colunasMaquina+` FROM maquinas WHERE id=?`, id)); err == nil {
				m.Duplicada = true
				g.Maquinas = append(g.Maquinas, m)
			}
		}
		sort.Slice(g.Maquinas, func(i, j int) bool { return g.Maquinas[i].UltimoContato > g.Maquinas[j].UltimoContato })
		grupos = append(grupos, g)
	}
	return grupos, nil
}

func (b *Banco) maquinasComMACDuplicado() (map[int64]bool, error) {
	rows, err := b.db.Query(`SELECT DISTINCT maquina_id FROM interfaces WHERE fisica=1 AND mac IN (
		SELECT mac FROM interfaces WHERE fisica=1 AND mac NOT IN (SELECT mac FROM macs_ignorados)
		GROUP BY mac HAVING COUNT(DISTINCT maquina_id) > 1)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	r := map[int64]bool{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			r[id] = true
		}
	}
	return r, rows.Err()
}

type GrupoNome struct {
	Hostname string
	Maquinas []*Maquina
}

func (b *Banco) DuplicadosHostname() ([]GrupoNome, error) {
	todas, err := b.Maquinas("")
	if err != nil {
		return nil, err
	}
	m := map[string][]*Maquina{}
	var ordem []string
	for _, x := range todas {
		k := strings.ToUpper(x.Hostname)
		if _, ok := m[k]; !ok {
			ordem = append(ordem, k)
		}
		m[k] = append(m[k], x)
	}
	var g []GrupoNome
	for _, k := range ordem {
		if len(m[k]) > 1 {
			g = append(g, GrupoNome{Hostname: k, Maquinas: m[k]})
		}
	}
	return g, nil
}

type MACIgnorado struct{ MAC, Motivo, CriadoEm string }

func (b *Banco) MACsIgnorados() ([]MACIgnorado, error) {
	rows, err := b.db.Query(`SELECT mac, motivo, criado_em FROM macs_ignorados ORDER BY mac`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []MACIgnorado
	for rows.Next() {
		var x MACIgnorado
		if rows.Scan(&x.MAC, &x.Motivo, &x.CriadoEm) == nil {
			l = append(l, x)
		}
	}
	return l, rows.Err()
}

func (b *Banco) IgnorarMAC(mac, motivo string) error {
	mac = normalizarMAC(mac)
	if !macValido(mac) {
		return errors.New("MAC inválido")
	}
	_, err := b.db.Exec(`INSERT INTO macs_ignorados(mac,motivo,criado_em) VALUES(?,?,?) ON CONFLICT(mac) DO UPDATE SET motivo=excluded.motivo`, mac, motivo, agora())
	return err
}

func (b *Banco) RemoverIgnorado(mac string) error {
	_, err := b.db.Exec(`DELETE FROM macs_ignorados WHERE mac=?`, normalizarMAC(mac))
	return err
}

// ---------------- Usuários ----------------

type Usuario struct {
	ID                             int64
	Login, Nome, Email, Setor, Obs string
	PrimeiroVisto, UltimoVisto     string
	QtdMaquinas                    int
	Maquinas                       string
	Apelidos                       string
}

func (b *Banco) Usuarios(busca string) ([]*Usuario, error) {
	q := `SELECT u.id, u.login, u.nome, u.email, u.setor, u.observacoes, u.primeiro_visto, u.ultimo_visto,
		(SELECT COUNT(*) FROM logins l WHERE l.usuario_id=u.id),
		COALESCE((SELECT GROUP_CONCAT(m.hostname, ', ') FROM logins l JOIN maquinas m ON m.id=l.maquina_id WHERE l.usuario_id=u.id),''),
		COALESCE((SELECT GROUP_CONCAT(a.apelido, ', ') FROM usuario_apelidos a WHERE a.usuario_id=u.id),'')
		FROM usuarios u`
	var args []any
	if busca = strings.TrimSpace(busca); busca != "" {
		like := "%" + busca + "%"
		q += ` WHERE u.login LIKE ? OR u.nome LIKE ? OR u.email LIKE ? OR u.setor LIKE ?`
		args = []any{like, like, like, like}
	}
	q += ` ORDER BY u.login COLLATE NOCASE`
	rows, err := b.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []*Usuario
	for rows.Next() {
		u := &Usuario{}
		if err := rows.Scan(&u.ID, &u.Login, &u.Nome, &u.Email, &u.Setor, &u.Obs, &u.PrimeiroVisto, &u.UltimoVisto,
			&u.QtdMaquinas, &u.Maquinas, &u.Apelidos); err != nil {
			return nil, err
		}
		l = append(l, u)
	}
	return l, rows.Err()
}

func (b *Banco) Usuario(id int64) (*Usuario, error) {
	l, err := b.Usuarios("")
	if err != nil {
		return nil, err
	}
	for _, u := range l {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, sql.ErrNoRows
}

// UsuariosDuplicados sugere grupos de logins que parecem ser a mesma pessoa (ex.: EMPRESA\joao e joao).
func (b *Banco) UsuariosDuplicados() ([][]*Usuario, error) {
	l, err := b.Usuarios("")
	if err != nil {
		return nil, err
	}
	m := map[string][]*Usuario{}
	var ordem []string
	for _, u := range l {
		k := chaveCurta(u.Login)
		if _, ok := m[k]; !ok {
			ordem = append(ordem, k)
		}
		m[k] = append(m[k], u)
	}
	var g [][]*Usuario
	for _, k := range ordem {
		if len(m[k]) > 1 {
			g = append(g, m[k])
		}
	}
	return g, nil
}

type LoginMaquina struct {
	UsuarioID             int64
	MaquinaID             int64
	Login, Nome, Hostname string
	Primeiro, Ultimo      string
}

func (b *Banco) Logins(maquinaID, usuarioID int64) ([]LoginMaquina, error) {
	q := `SELECT l.usuario_id, l.maquina_id, u.login, u.nome, m.hostname, l.primeiro, l.ultimo
		FROM logins l JOIN usuarios u ON u.id=l.usuario_id JOIN maquinas m ON m.id=l.maquina_id WHERE 1=1`
	var args []any
	if maquinaID > 0 {
		q += ` AND l.maquina_id=?`
		args = append(args, maquinaID)
	}
	if usuarioID > 0 {
		q += ` AND l.usuario_id=?`
		args = append(args, usuarioID)
	}
	q += ` ORDER BY l.ultimo DESC`
	rows, err := b.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []LoginMaquina
	for rows.Next() {
		var x LoginMaquina
		if err := rows.Scan(&x.UsuarioID, &x.MaquinaID, &x.Login, &x.Nome, &x.Hostname, &x.Primeiro, &x.Ultimo); err != nil {
			return nil, err
		}
		l = append(l, x)
	}
	return l, rows.Err()
}

func (b *Banco) AtualizarUsuario(id int64, login, nome, email, setor, obs string) error {
	login = strings.TrimSpace(login)
	if login == "" {
		return errors.New("o login não pode ficar vazio")
	}
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var antigo string
	if err := tx.QueryRow(`SELECT login FROM usuarios WHERE id=?`, id).Scan(&antigo); err != nil {
		return err
	}
	var outro int64
	if err := tx.QueryRow(`SELECT id FROM usuarios WHERE login=? AND id<>?`, login, id).Scan(&outro); err == nil {
		return fmt.Errorf("já existe outro usuário com o login %q — use a opção Mesclar", login)
	}
	if _, err := tx.Exec(`UPDATE usuarios SET login=?, nome=?, email=?, setor=?, observacoes=? WHERE id=?`, login, nome, email, setor, obs, id); err != nil {
		return err
	}
	if !strings.EqualFold(antigo, login) {
		// o login antigo vira apelido para que novos relatórios continuem apontando para este cadastro
		if _, err := tx.Exec(`INSERT INTO usuario_apelidos(apelido,usuario_id) VALUES(?,?) ON CONFLICT(apelido) DO UPDATE SET usuario_id=excluded.usuario_id`, antigo, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM usuario_apelidos WHERE apelido=?`, login); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE maquinas SET ultimo_usuario=? WHERE ultimo_usuario=? COLLATE NOCASE`, login, antigo); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (b *Banco) ExcluirUsuario(id int64) error {
	_, err := b.db.Exec(`DELETE FROM usuarios WHERE id=?`, id)
	return err
}

// MesclarUsuarios une o usuário origem ao destino; o login de origem passa a ser apelido do destino.
func (b *Banco) MesclarUsuarios(origem, destino int64) error {
	if origem == destino {
		return errors.New("selecione usuários diferentes")
	}
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var loginOrig, loginDest string
	if err := tx.QueryRow(`SELECT login FROM usuarios WHERE id=?`, origem).Scan(&loginOrig); err != nil {
		return errors.New("usuário de origem não encontrado")
	}
	if err := tx.QueryRow(`SELECT login FROM usuarios WHERE id=?`, destino).Scan(&loginDest); err != nil {
		return errors.New("usuário de destino não encontrado")
	}
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO logins(usuario_id,maquina_id,primeiro,ultimo) SELECT ?, maquina_id, primeiro, ultimo FROM logins WHERE usuario_id=?
			ON CONFLICT(usuario_id,maquina_id) DO UPDATE SET primeiro=MIN(primeiro,excluded.primeiro), ultimo=MAX(ultimo,excluded.ultimo)`, []any{destino, origem}},
		{`UPDATE usuario_apelidos SET usuario_id=? WHERE usuario_id=?`, []any{destino, origem}},
		{`UPDATE usuarios SET
			nome = CASE WHEN nome='' THEN (SELECT nome FROM usuarios WHERE id=?) ELSE nome END,
			email = CASE WHEN email='' THEN (SELECT email FROM usuarios WHERE id=?) ELSE email END,
			setor = CASE WHEN setor='' THEN (SELECT setor FROM usuarios WHERE id=?) ELSE setor END,
			observacoes = CASE WHEN observacoes='' THEN (SELECT observacoes FROM usuarios WHERE id=?) ELSE observacoes END,
			primeiro_visto = MIN(primeiro_visto, (SELECT primeiro_visto FROM usuarios WHERE id=?)),
			ultimo_visto = MAX(ultimo_visto, (SELECT ultimo_visto FROM usuarios WHERE id=?))
			WHERE id=?`, []any{origem, origem, origem, origem, origem, origem, destino}},
		{`UPDATE maquinas SET ultimo_usuario=? WHERE ultimo_usuario=? COLLATE NOCASE`, []any{loginDest, loginOrig}},
		{`DELETE FROM usuarios WHERE id=?`, []any{origem}},
		{`INSERT INTO usuario_apelidos(apelido,usuario_id) VALUES(?,?) ON CONFLICT(apelido) DO UPDATE SET usuario_id=excluded.usuario_id`, []any{loginOrig, destino}},
	}
	for _, s := range stmts {
		if _, err := tx.Exec(s.q, s.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (b *Banco) RemoverApelido(apelido string) error {
	_, err := b.db.Exec(`DELETE FROM usuario_apelidos WHERE apelido=?`, apelido)
	return err
}

// ---------------- Estatísticas ----------------

type Resumo struct {
	Maquinas, Ativas24h, Inativas7d, Usuarios, MACDuplicados, NomesDuplicados, UsuariosDuplicados int
}

func (b *Banco) Resumo() Resumo {
	var r Resumo
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM maquinas`).Scan(&r.Maquinas)
	lim24 := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	lim7 := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM maquinas WHERE ultimo_contato >= ?`, lim24).Scan(&r.Ativas24h)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM maquinas WHERE ultimo_contato < ?`, lim7).Scan(&r.Inativas7d)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM usuarios`).Scan(&r.Usuarios)
	if g, err := b.DuplicadosMAC(); err == nil {
		r.MACDuplicados = len(g)
	}
	if g, err := b.DuplicadosHostname(); err == nil {
		r.NomesDuplicados = len(g)
	}
	if g, err := b.UsuariosDuplicados(); err == nil {
		r.UsuariosDuplicados = len(g)
	}
	return r
}

type RegistroAuditoria struct{ Em, Admin, Acao, Detalhe string }

func (b *Banco) Auditoria(limite int) ([]RegistroAuditoria, error) {
	rows, err := b.db.Query(`SELECT em, admin, acao, detalhe FROM auditoria ORDER BY id DESC LIMIT ?`, limite)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []RegistroAuditoria
	for rows.Next() {
		var x RegistroAuditoria
		if rows.Scan(&x.Em, &x.Admin, &x.Acao, &x.Detalhe) == nil {
			l = append(l, x)
		}
	}
	return l, rows.Err()
}

// StatusMigracao resume o andamento da migração do ponto de vista DESTE servidor. Quando um
// agente migra, ele deixa de se comunicar com este servidor; por isso o indicador confiável é
// quantas máquinas AINDA estão falando com este servidor (nas últimas 24 h). Quando chega a
// zero, todas as máquinas ativas já migraram e este servidor pode ser desligado.
type StatusMigracao struct {
	Destino   string
	Ativa     bool // true = autorizada; os agentes já estão sendo avisados
	AindaAqui int  // ativas nas últimas 24 h que ainda reportam a este servidor
	Total     int
}

func (b *Banco) Migracao() StatusMigracao {
	st := StatusMigracao{Destino: strings.TrimRight(b.Config("migrar_servidor"), "/"), Ativa: b.Config("migrar_ativa") == "1"}
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM maquinas`).Scan(&st.Total)
	if st.Destino == "" {
		return st
	}
	lim := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM maquinas WHERE ultimo_contato >= ? AND TRIM(servidor_agente,'/') <> ?`, lim, st.Destino).Scan(&st.AindaAqui)
	return st
}

// ---------------- Nome genérico ----------------

var reGenericoWin = regexp.MustCompile(`(?i)^(DESKTOP|WIN|LAPTOP|MININT|MINWINPC|WORKSTATION)-[A-Z0-9]{5,}$`)

// nomeGenerico diz se um hostname parece um nome padrão de fábrica (não renomeado),
// como DESKTOP-A1B2C3D, ou se casa com algum padrão informado pelo administrador.
// padroes: lista separada por vírgula; cada item é um trecho (contém) ou usa * como curinga.
func nomeGenerico(hostname, padroes string) bool {
	h := strings.TrimSpace(hostname)
	if h == "" {
		return false
	}
	if reGenericoWin.MatchString(h) {
		return true
	}
	for _, p := range strings.Split(padroes, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if casaCuringa(strings.ToLower(h), strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// casaCuringa compara com suporte a * (qualquer sequência). Sem *, verifica "contém".
func casaCuringa(s, padrao string) bool {
	if !strings.Contains(padrao, "*") {
		return strings.Contains(s, padrao)
	}
	partes := strings.Split(padrao, "*")
	pos := 0
	for i, parte := range partes {
		if parte == "" {
			continue
		}
		idx := strings.Index(s[pos:], parte)
		if idx < 0 {
			return false
		}
		if i == 0 && idx != 0 && !strings.HasPrefix(padrao, "*") {
			return false
		}
		pos += idx + len(parte)
	}
	if !strings.HasSuffix(padrao, "*") && len(partes) > 0 {
		ultima := partes[len(partes)-1]
		if ultima != "" && !strings.HasSuffix(s, ultima) {
			return false
		}
	}
	return true
}

// ---------------- Filtros salvos ----------------

type Filtro struct {
	ID                           int64
	Nome, Campo, Operador, Valor string
}

func (b *Banco) Filtros() ([]Filtro, error) {
	rows, err := b.db.Query(`SELECT id, nome, campo, operador, valor FROM filtros ORDER BY nome COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []Filtro
	for rows.Next() {
		var f Filtro
		if rows.Scan(&f.ID, &f.Nome, &f.Campo, &f.Operador, &f.Valor) == nil {
			l = append(l, f)
		}
	}
	return l, rows.Err()
}

func (b *Banco) Filtro(id int64) (*Filtro, error) {
	var f Filtro
	err := b.db.QueryRow(`SELECT id, nome, campo, operador, valor FROM filtros WHERE id=?`, id).Scan(&f.ID, &f.Nome, &f.Campo, &f.Operador, &f.Valor)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (b *Banco) CriarFiltro(nome, campo, operador, valor string) error {
	if strings.TrimSpace(nome) == "" || strings.TrimSpace(campo) == "" {
		return errors.New("informe o nome e o campo do filtro")
	}
	_, err := b.db.Exec(`INSERT INTO filtros(nome,campo,operador,valor) VALUES(?,?,?,?)`,
		strings.TrimSpace(nome), strings.TrimSpace(campo), strings.TrimSpace(operador), strings.TrimSpace(valor))
	return err
}

func (b *Banco) ExcluirFiltro(id int64) error {
	_, err := b.db.Exec(`DELETE FROM filtros WHERE id=?`, id)
	return err
}

// valorCampo devolve o texto de um campo da máquina para aplicar um filtro.
func valorCampo(m *Maquina, campo string) string {
	switch campo {
	case "hostname":
		return m.Hostname
	case "usuario":
		return m.UltimoUsuario + " " + m.UsuarioAtual
	case "so":
		return m.SO + " " + m.SOVersao
	case "setor":
		return m.Setor
	case "responsavel":
		return m.Responsavel
	case "ip":
		return m.IPs
	case "mac":
		return m.MACs
	case "modelo":
		return m.Fabricante + " " + m.Modelo
	case "patrimonio":
		return m.Patrimonio
	}
	return ""
}

// AplicarFiltro filtra uma lista de máquinas conforme um filtro salvo.
func AplicarFiltro(lista []*Maquina, f *Filtro) []*Maquina {
	var r []*Maquina
	alvo := strings.ToLower(f.Valor)
	for _, m := range lista {
		v := strings.ToLower(valorCampo(m, f.Campo))
		ok := false
		switch f.Operador {
		case "contem":
			ok = strings.Contains(v, alvo)
		case "nao_contem":
			ok = !strings.Contains(v, alvo)
		case "comeca":
			ok = strings.HasPrefix(v, alvo)
		case "igual":
			ok = strings.TrimSpace(v) == alvo
		case "generico":
			ok = nomeGenerico(m.Hostname, "")
		default:
			ok = strings.Contains(v, alvo)
		}
		if ok {
			r = append(r, m)
		}
	}
	return r
}

// ---------------- Alertas (notificações) ----------------

type Alerta struct {
	Tipo    string // generico, inativa, disco
	Maquina *Maquina
	Detalhe string
}

// Alertas calcula as notificações ativas com base nas configurações salvas.
func (b *Banco) Alertas() ([]Alerta, error) {
	maqs, err := b.Maquinas("")
	if err != nil {
		return nil, err
	}
	var al []Alerta
	padroes := b.Config("alerta_generico_padroes")
	ligGenerico := b.Config("alerta_generico") == "1"
	diasInativa := 0
	fmt.Sscan(b.Config("alerta_inativa_dias"), &diasInativa)
	pctDisco := 0
	fmt.Sscan(b.Config("alerta_disco_pct"), &pctDisco)

	var limInativa time.Time
	if diasInativa > 0 {
		limInativa = time.Now().Add(-time.Duration(diasInativa) * 24 * time.Hour)
	}
	for _, m := range maqs {
		if ligGenerico && nomeGenerico(m.Hostname, padroes) {
			al = append(al, Alerta{Tipo: "generico", Maquina: m, Detalhe: "nome parece padrão de fábrica"})
		}
		if diasInativa > 0 {
			if t, ok := parseDataBanco(m.UltimoContato); ok && t.Before(limInativa) {
				al = append(al, Alerta{Tipo: "inativa", Maquina: m, Detalhe: "sem contato desde " + t.Local().Format("02/01/2006")})
			}
		}
		if pctDisco > 0 {
			if pior, letra := piorVolume(m.DadosJSON); pior >= 0 && pior < pctDisco {
				al = append(al, Alerta{Tipo: "disco", Maquina: m, Detalhe: fmt.Sprintf("%s com %d%% livre", letra, pior)})
			}
		}
	}
	return al, nil
}

func parseDataBanco(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// piorVolume devolve o menor percentual de espaço livre entre os volumes da máquina.
func piorVolume(dadosJSON string) (int, string) {
	var r modelo.Relatorio
	if json.Unmarshal([]byte(dadosJSON), &r) != nil {
		return -1, ""
	}
	pior, letra := -1, ""
	for _, v := range r.Volumes {
		if v.Tamanho == 0 {
			continue
		}
		pct := int(v.Livre * 100 / v.Tamanho)
		if pior < 0 || pct < pior {
			pior, letra = pct, v.Letra
		}
	}
	return pior, letra
}

// ---------------- Estatísticas do painel ----------------

type Painel struct {
	Total                     int
	Ativas, Ociosas, Inativas int // <=7d, 7-14d, >14d (por último contato)
	SOs, Setores              []Fatia
	DiscoCheio                int // máquinas com algum volume < 10% livre
	SemCadastro               int // sem setor/responsável preenchidos
}

func (b *Banco) EstatisticasPainel() Painel {
	var p Painel
	maqs, _ := b.Maquinas("")
	p.Total = len(maqs)
	ag := time.Now()
	so := map[string]int{}
	setor := map[string]int{}
	var ordemSO, ordemSetor []string
	for _, m := range maqs {
		if t, ok := parseDataBanco(m.UltimoContato); ok {
			d := ag.Sub(t)
			switch {
			case d <= 7*24*time.Hour:
				p.Ativas++
			case d <= 14*24*time.Hour:
				p.Ociosas++
			default:
				p.Inativas++
			}
		} else {
			p.Inativas++
		}
		k := strings.TrimSpace(m.SO + " " + m.SOVersao)
		if k == "" {
			k = "(desconhecido)"
		}
		if so[k] == 0 {
			ordemSO = append(ordemSO, k)
		}
		so[k]++
		st := strings.TrimSpace(m.Setor)
		if st == "" {
			p.SemCadastro++
			st = "(sem setor)"
		}
		if setor[st] == 0 {
			ordemSetor = append(ordemSetor, st)
		}
		setor[st]++
		if pior, _ := piorVolume(m.DadosJSON); pior >= 0 && pior < 10 {
			p.DiscoCheio++
		}
	}
	for _, k := range ordemSO {
		p.SOs = append(p.SOs, Fatia{Rotulo: k, Valor: so[k], Cor: "var(--pri)"})
	}
	for _, k := range ordemSetor {
		p.Setores = append(p.Setores, Fatia{Rotulo: k, Valor: setor[k], Cor: "var(--pri)"})
	}
	ordenarFatias(p.SOs)
	ordenarFatias(p.Setores)
	if len(p.SOs) > 6 {
		p.SOs = p.SOs[:6]
	}
	if len(p.Setores) > 6 {
		p.Setores = p.Setores[:6]
	}
	return p
}

func ordenarFatias(f []Fatia) {
	for i := 0; i < len(f); i++ {
		for j := i + 1; j < len(f); j++ {
			if f[j].Valor > f[i].Valor {
				f[i], f[j] = f[j], f[i]
			}
		}
	}
}
