package servidor

// Active Directory (1.22): comparativo "máquinas no AD × máquinas com o agente" e usuários ativos.
// O servidor NÃO consulta o AD (nem guarda senha do domínio): o script Sincronizar-AD.ps1, rodando
// no controlador de domínio como tarefa agendada, lê o AD e envia a lista para /api/v1/ad com um
// token próprio (diferente do token dos agentes). Também dá para enviar o arquivo pelo painel.

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ComputadorAD struct {
	Nome        string `json:"nome"`
	DNS         string `json:"dns"`
	SO          string `json:"so"`
	OU          string `json:"ou"`
	Ativo       bool   `json:"ativo"` // conta habilitada
	UltimoLogon string `json:"ultimo_logon"`
	Criado      string `json:"criado"`
	// calculados
	TemAgente bool  `json:"-"`
	MaquinaID int64 `json:"-"`
}

type UsuarioAD struct {
	Login        string `json:"login"`
	Nome         string `json:"nome"`
	Email        string `json:"email"`
	Departamento string `json:"departamento"`
	Cargo        string `json:"cargo"`
	OU           string `json:"ou"`
	Ativo        bool   `json:"ativo"`
	UltimoLogon  string `json:"ultimo_logon"`
}

type pacoteAD struct {
	Dominio      string         `json:"dominio"`
	Origem       string         `json:"origem"`
	GeradoEm     string         `json:"gerado_em"`
	Computadores []ComputadorAD `json:"computadores"`
	Usuarios     []UsuarioAD    `json:"usuarios"`
}

const esquemaAD = `
CREATE TABLE IF NOT EXISTS ad_computadores (
	nome TEXT PRIMARY KEY, dns TEXT NOT NULL DEFAULT '', so TEXT NOT NULL DEFAULT '', ou TEXT NOT NULL DEFAULT '',
	ativo INTEGER NOT NULL DEFAULT 1, ultimo_logon TEXT NOT NULL DEFAULT '', criado TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS ad_usuarios (
	login TEXT PRIMARY KEY, nome TEXT NOT NULL DEFAULT '', email TEXT NOT NULL DEFAULT '', departamento TEXT NOT NULL DEFAULT '',
	cargo TEXT NOT NULL DEFAULT '', ou TEXT NOT NULL DEFAULT '', ativo INTEGER NOT NULL DEFAULT 1, ultimo_logon TEXT NOT NULL DEFAULT ''
);`

func (b *Banco) garantirTabelasAD() {
	_, _ = b.db.Exec(esquemaAD)
}

// SalvarAD substitui a foto do AD pela recebida.
func (b *Banco) SalvarAD(p pacoteAD) error {
	if len(p.Computadores) == 0 && len(p.Usuarios) == 0 {
		return errors.New("o arquivo não tem computadores nem usuários")
	}
	b.garantirTabelasAD()
	tx, err := b.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM ad_computadores`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM ad_usuarios`); err != nil {
		return err
	}
	for _, c := range p.Computadores {
		n := nomeMaquinaAD(c.Nome)
		if n == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO ad_computadores(nome,dns,so,ou,ativo,ultimo_logon,criado) VALUES(?,?,?,?,?,?,?)`,
			n, c.DNS, c.SO, c.OU, c.Ativo, normalizarDataAD(c.UltimoLogon), normalizarDataAD(c.Criado)); err != nil {
			return err
		}
	}
	for _, u := range p.Usuarios {
		l := strings.ToLower(strings.TrimSpace(u.Login))
		if l == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO ad_usuarios(login,nome,email,departamento,cargo,ou,ativo,ultimo_logon) VALUES(?,?,?,?,?,?,?,?)`,
			l, u.Nome, u.Email, u.Departamento, u.Cargo, u.OU, u.Ativo, normalizarDataAD(u.UltimoLogon)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_ = b.DefinirConfig("ad_sincronizado_em", agora())
	_ = b.DefinirConfig("ad_dominio", p.Dominio)
	_ = b.DefinirConfig("ad_origem", p.Origem)
	return nil
}

func nomeMaquinaAD(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "$")
	if i := strings.IndexByte(s, '.'); i > 0 {
		s = s[:i]
	}
	return s
}

func normalizarDataAD(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, f := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "02/01/2006 15:04:05"} {
		if t, err := time.Parse(f, s); err == nil {
			if t.Year() < 1990 {
				return ""
			}
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func (b *Banco) ComputadoresAD() []ComputadorAD {
	b.garantirTabelasAD()
	var l []ComputadorAD
	rows, err := b.db.Query(`SELECT nome,dns,so,ou,ativo,ultimo_logon,criado FROM ad_computadores ORDER BY nome`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var c ComputadorAD
		if rows.Scan(&c.Nome, &c.DNS, &c.SO, &c.OU, &c.Ativo, &c.UltimoLogon, &c.Criado) == nil {
			l = append(l, c)
		}
	}
	return l
}

func (b *Banco) UsuariosAD() []UsuarioAD {
	b.garantirTabelasAD()
	var l []UsuarioAD
	rows, err := b.db.Query(`SELECT login,nome,email,departamento,cargo,ou,ativo,ultimo_logon FROM ad_usuarios ORDER BY ultimo_logon DESC, login`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var u UsuarioAD
		if rows.Scan(&u.Login, &u.Nome, &u.Email, &u.Departamento, &u.Cargo, &u.OU, &u.Ativo, &u.UltimoLogon) == nil {
			l = append(l, u)
		}
	}
	return l
}

func ehServidorAD(so string) bool { return strings.Contains(strings.ToLower(so), "server") }

func dentroDe(data string, dias int) bool {
	t, err := time.Parse(time.RFC3339, data)
	return err == nil && time.Since(t) <= time.Duration(dias)*24*time.Hour
}

// CoberturaAD: estações ativas no AD × com o agente.
type CoberturaAD struct {
	Sincronizado  bool
	Em, Dominio   string
	Origem        string
	Dias          int
	Estacoes      int // estações habilitadas com logon nos últimos Dias
	ComAgente     int
	SemAgente     []ComputadorAD
	Pct           int
	ForaDoAD      []*Maquina // têm o agente mas não aparecem no AD
	TotalAD       int
	Servidores    int
	Inativas      int // habilitadas sem logon no período
	Desabilitadas int
}

func (s *Servidor) coberturaAD(dias int) CoberturaAD {
	if dias <= 0 {
		dias = 30
	}
	c := CoberturaAD{Dias: dias, Em: s.b.Config("ad_sincronizado_em"), Dominio: s.b.Config("ad_dominio"), Origem: s.b.Config("ad_origem")}
	c.Sincronizado = c.Em != ""
	if !c.Sincronizado {
		return c
	}
	maqs, _ := s.b.Maquinas("")
	porNome := map[string]*Maquina{}
	for _, m := range maqs {
		porNome[nomeMaquinaAD(m.Hostname)] = m
	}
	noAD := map[string]bool{}
	for _, pc := range s.b.ComputadoresAD() {
		c.TotalAD++
		noAD[pc.Nome] = true
		switch {
		case !pc.Ativo:
			c.Desabilitadas++
			continue
		case ehServidorAD(pc.SO):
			c.Servidores++
			continue
		case !dentroDe(pc.UltimoLogon, dias):
			c.Inativas++
			continue
		}
		c.Estacoes++
		if m := porNome[pc.Nome]; m != nil {
			c.ComAgente++
		} else {
			c.SemAgente = append(c.SemAgente, pc)
		}
	}
	sort.Slice(c.SemAgente, func(i, j int) bool { return c.SemAgente[i].UltimoLogon > c.SemAgente[j].UltimoLogon })
	for _, m := range maqs {
		if !noAD[nomeMaquinaAD(m.Hostname)] {
			c.ForaDoAD = append(c.ForaDoAD, m)
		}
	}
	if c.Estacoes > 0 {
		c.Pct = c.ComAgente * 100 / c.Estacoes
	}
	return c
}

func usuariosAtivos(us []UsuarioAD, dias int) []UsuarioAD {
	var l []UsuarioAD
	for _, u := range us {
		if u.Ativo && dentroDe(u.UltimoLogon, dias) {
			l = append(l, u)
		}
	}
	return l
}

func diasDaURL(r *http.Request, campo string, padrao int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(campo)); err == nil && v > 0 && v <= 3650 {
		return v
	}
	return padrao
}

// ---------------- rotas ----------------

func (s *Servidor) apiAD(w http.ResponseWriter, r *http.Request) {
	tok := s.b.Config("ad_token")
	rec := strings.TrimSpace(r.Header.Get("X-AD-Token"))
	if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(rec)) != 1 {
		http.Error(w, "token do AD inválido (gere em Inventário → Active Directory)", http.StatusUnauthorized)
		return
	}
	var p pacoteAD
	if err := json.NewDecoder(io.LimitReader(r.Body, 50<<20)).Decode(&p); err != nil {
		http.Error(w, "JSON inválido: "+err.Error(), http.StatusBadRequest)
		return
	}
	if p.Origem == "" {
		p.Origem = ipCliente(r)
	}
	if err := s.b.SalvarAD(p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cob := s.coberturaAD(30)
	s.b.Auditar("ad", "sincronização do Active Directory", fmt.Sprintf("%d computadores, %d usuários, de %s", len(p.Computadores), len(p.Usuarios), p.Origem))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "computadores": len(p.Computadores), "usuarios": len(p.Usuarios),
		"estacoes_ativas": cob.Estacoes, "com_agente": cob.ComAgente, "cobertura_pct": cob.Pct})
}

func (s *Servidor) paginaAD(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	diasPC := diasDaURL(r, "dias_pc", 30)
	diasUs := diasDaURL(r, "dias", 7)
	cob := s.coberturaAD(diasPC)
	us := s.b.UsuariosAD()
	habil := 0
	for _, u := range us {
		if u.Ativo {
			habil++
		}
	}
	ativos := usuariosAtivos(us, diasUs)
	d := map[string]any{"Cob": cob, "DiasPC": diasPC, "DiasUs": diasUs, "Ativos": ativos, "Habilitados": habil, "TotalUsuarios": len(us)}
	if sess.Admin.EhAdmin() {
		d["Token"] = s.b.Config("ad_token")
		d["URL"] = s.baseURL(r)
	}
	s.render(w, r, "ad", "Active Directory", "ad", sess, d)
}

func (s *Servidor) acaoTokenAD(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	err := s.b.DefinirConfig("ad_token", aleatorio(24))
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "gerar token do Active Directory", "")
	}
	redirecionar(w, r, "/ad#configurar", "Token do AD gerado. Use-o no Sincronizar-AD.ps1 (o anterior deixa de valer).", err)
}

func (s *Servidor) acaoEnviarAD(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	f, _, err := r.FormFile("arquivo")
	if err != nil {
		redirecionar(w, r, "/ad", "", errors.New("escolha o arquivo ad-harvest.json gerado pelo script"))
		return
	}
	defer f.Close()
	var p pacoteAD
	if err := json.NewDecoder(io.LimitReader(f, 50<<20)).Decode(&p); err != nil {
		redirecionar(w, r, "/ad", "", fmt.Errorf("arquivo inválido: %v", err))
		return
	}
	if err := s.b.SalvarAD(p); err != nil {
		redirecionar(w, r, "/ad", "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "enviar arquivo do Active Directory", fmt.Sprintf("%d computadores, %d usuários", len(p.Computadores), len(p.Usuarios)))
	redirecionar(w, r, "/ad", fmt.Sprintf("AD atualizado: %d computadores e %d usuários.", len(p.Computadores), len(p.Usuarios)), nil)
}

// baixarScriptAD entrega o Sincronizar-AD.ps1 já com o endereço e o token deste servidor.
func (s *Servidor) baixarScriptAD(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	tok := s.b.Config("ad_token")
	if tok == "" {
		redirecionar(w, r, "/ad#configurar", "", errors.New("gere o token do AD primeiro"))
		return
	}
	b, err := arquivosWeb.ReadFile("web/static/Sincronizar-AD.ps1")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	txt := strings.Replace(string(b), "[string]$Servidor = ''", "[string]$Servidor = '"+s.baseURL(r)+"'", 1)
	txt = strings.Replace(txt, "[string]$Token = ''", "[string]$Token = '"+tok+"'", 1)
	s.b.Auditar(sess.Admin.Usuario, "baixar script do Active Directory", "")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="Sincronizar-AD.ps1"`)
	_, _ = w.Write([]byte("\xef\xbb\xbf" + strings.ReplaceAll(strings.ReplaceAll(txt, "\r\n", "\n"), "\n", "\r\n")))
}

func (s *Servidor) exportarAD(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	diasPC := diasDaURL(r, "dias_pc", 30)
	diasUs := diasDaURL(r, "dias", 7)
	cob := s.coberturaAD(diasPC)
	if !cob.Sincronizado {
		redirecionar(w, r, "/ad", "", errors.New("o AD ainda não foi sincronizado"))
		return
	}
	us := s.b.UsuariosAD()
	linUs := func(l []UsuarioAD) [][]Celula {
		var out [][]Celula
		for _, u := range l {
			hab := "Sim"
			if !u.Ativo {
				hab = "Não"
			}
			out = append(out, []Celula{u.Login, u.Nome, u.Email, u.Departamento, u.Cargo, formatarData(u.UltimoLogon), hab, u.OU})
		}
		return out
	}
	cabUs := []string{"Login", "Nome", "E-mail", "Departamento", "Cargo", "Último logon", "Habilitado", "OU"}
	largUs := []float64{22, 32, 32, 20, 22, 18, 11, 50}
	var linPC, linSem [][]Celula
	maqs, _ := s.b.Maquinas("")
	porNome := map[string]*Maquina{}
	for _, m := range maqs {
		porNome[nomeMaquinaAD(m.Hostname)] = m
	}
	for _, pc := range s.b.ComputadoresAD() {
		ag, contato := "Não", ""
		if m := porNome[pc.Nome]; m != nil {
			ag, contato = "Sim", formatarData(m.UltimoContato)
		}
		tipo := "Estação"
		if ehServidorAD(pc.SO) {
			tipo = "Servidor"
		}
		hab := "Sim"
		if !pc.Ativo {
			hab = "Não"
		}
		linPC = append(linPC, []Celula{pc.Nome, tipo, pc.SO, hab, formatarData(pc.UltimoLogon), ag, contato, pc.OU})
	}
	for _, pc := range cob.SemAgente {
		linSem = append(linSem, []Celula{pc.Nome, pc.SO, formatarData(pc.UltimoLogon), pc.OU})
	}
	resumo := [][]Celula{
		{"Domínio", cob.Dominio}, {"Sincronizado em", formatarData(cob.Em)},
		{fmt.Sprintf("Estações ativas no AD (logon em %d dias)", diasPC), cob.Estacoes},
		{"Com o agente do Harvest", cob.ComAgente}, {"Sem o agente", len(cob.SemAgente)},
		{"Cobertura (%)", cob.Pct},
		{"Servidores no AD (fora da conta)", cob.Servidores}, {"Estações sem logon no período", cob.Inativas},
		{"Contas de computador desabilitadas", cob.Desabilitadas},
		{fmt.Sprintf("Usuários ativos (logon em %d dias)", diasUs), len(usuariosAtivos(us, diasUs))},
	}
	nome := "active-directory_" + time.Now().Format("2006-01-02_1504") + ".xlsx"
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+nome+`"`)
	s.b.Auditar(sess.Admin.Usuario, "exportar planilha do AD", nome)
	_ = EscreverXLSX(w, []Planilha{
		{Nome: "Resumo", Cabecalho: []string{"Item", "Valor"}, Larguras: []float64{44, 30}, Linhas: resumo},
		{Nome: fmt.Sprintf("Usuários ativos (%d dias)", diasUs), Cabecalho: cabUs, Larguras: largUs, Linhas: linUs(usuariosAtivos(us, diasUs))},
		{Nome: "Todos os usuários", Cabecalho: cabUs, Larguras: largUs, Linhas: linUs(us)},
		{Nome: "Computadores do AD", Cabecalho: []string{"Nome", "Tipo", "Sistema", "Habilitado", "Último logon (AD)", "Tem o agente", "Último contato (Harvest)", "OU"},
			Larguras: []float64{20, 10, 32, 11, 18, 12, 20, 50}, Linhas: linPC},
		{Nome: "Sem o agente", Cabecalho: []string{"Nome", "Sistema", "Último logon (AD)", "OU"}, Larguras: []float64{20, 32, 18, 50}, Linhas: linSem},
	})
}
