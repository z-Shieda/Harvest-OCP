package servidor

// Módulo Termos (1.23) — fase 1 (Base): migrações, ficha do colaborador, empresas e
// configuração do módulo. Especificação: docs/SDD-1.23-TERMOS.md.
//
// Tudo é criado sem alteração destrutiva: tabelas novas com CREATE IF NOT EXISTS e colunas
// novas com ALTER TABLE ADD COLUMN (o erro de "coluna já existe" é ignorado, como no resto do banco).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const esquemaTermos = `
CREATE TABLE IF NOT EXISTS empresas (
	id INTEGER PRIMARY KEY,
	razao_social TEXT NOT NULL,
	cnpj TEXT NOT NULL DEFAULT '',
	unidades_json TEXT NOT NULL DEFAULT '[]',
	ativa INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS termo_envios (
	id INTEGER PRIMARY KEY,
	termo_id INTEGER,
	admin_id INTEGER,
	destino TEXT NOT NULL DEFAULT 'pessoa',
	tipo TEXT NOT NULL DEFAULT '',
	para TEXT NOT NULL DEFAULT '',
	assunto TEXT NOT NULL DEFAULT '',
	texto TEXT NOT NULL DEFAULT '',
	html TEXT NOT NULL DEFAULT '',
	anexo_nome TEXT NOT NULL DEFAULT '',
	anexo_tipo TEXT NOT NULL DEFAULT '',
	anexo BLOB,
	situacao TEXT NOT NULL DEFAULT 'pendente',
	tentativas INTEGER NOT NULL DEFAULT 0,
	erro TEXT NOT NULL DEFAULT '',
	criado_em TEXT NOT NULL DEFAULT '',
	tentado_em TEXT NOT NULL DEFAULT '',
	proxima_em TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_termo_envios_fila ON termo_envios(situacao, proxima_em);
`

func (b *Banco) migrarTermos() {
	_, _ = b.db.Exec(esquemaTermos)
	for _, alter := range []string{
		// ficha do colaborador (D10) e login de rede (identificação da máquina, D15)
		`ALTER TABLE admins ADD COLUMN login_rede TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN cpf TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN matricula TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN cargo TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN setor TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN unidade TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN empresa_id INTEGER`,
		`ALTER TABLE admins ADD COLUMN gestor TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN vinculo TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN regime TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN telefone TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN ficha_preenchida_em TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN ficha_validada_por TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN ficha_validada_em TEXT NOT NULL DEFAULT ''`,
		// contas por convite (D2)
		`ALTER TABLE admins ADD COLUMN convite_token_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN convite_expira TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN convidado_por TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admins ADD COLUMN ativo INTEGER NOT NULL DEFAULT 1`,
		`CREATE INDEX IF NOT EXISTS idx_admins_convite ON admins(convite_token_hash)`,
	} {
		_, _ = b.db.Exec(alter)
	}
}

// ---------------- configuração do módulo ----------------

// ConfigTermos: opções de /admin/termos (guardadas na tabela config, prefixo termos_).
type ConfigTermos struct {
	Ativo        bool
	PermitirHTTP bool     // só para testes: liga o módulo sem HTTPS (auditado, com faixa de aviso)
	Dominios     []string // domínios de e-mail aceitos (D3)
	EmailsRH     []string // destinatários do aviso ao RH (D7)
	ConviteDias  int      // validade do convite (padrão 7)
	LembreteDias int      // lembrete ao colaborador enquanto aguarda (padrão 3)
	AtencaoDias  int      // "pedem atenção" no Início: aguardando há mais de N dias (padrão 7)
}

const dominioPadrao = "ocupacional.com.br"

func (b *Banco) ConfigTermos() ConfigTermos {
	c := ConfigTermos{
		Ativo:        b.Config("termos_ativo") == "1",
		PermitirHTTP: b.Config("termos_permitir_http") == "1",
		Dominios:     listaConfig(b.Config("termos_dominios")),
		EmailsRH:     listaConfig(b.Config("termos_emails_rh")),
		ConviteDias:  numeroConfig(b.Config("termos_convite_dias"), 7, 1, 30),
		LembreteDias: numeroConfig(b.Config("termos_lembrete_dias"), 3, 0, 30),
		AtencaoDias:  numeroConfig(b.Config("termos_atencao_dias"), 7, 1, 90),
	}
	if b.Config("termos_dominios") == "" {
		c.Dominios = []string{dominioPadrao}
	}
	return c
}

// listaConfig separa por vírgula, ponto e vírgula, espaço ou quebra de linha (sem repetir).
func listaConfig(s string) []string {
	campos := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
	vistos := map[string]bool{}
	var l []string
	for _, c := range campos {
		c = strings.Trim(c, " .@")
		if c != "" && !vistos[c] {
			vistos[c] = true
			l = append(l, c)
		}
	}
	return l
}

func numeroConfig(s string, padrao, min, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return padrao
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// dominioAceito confere se o e-mail é de um domínio corporativo da lista (D3). Subdomínios não
// contam: "x@rh.ocupacional.com.br" só passa se "rh.ocupacional.com.br" estiver na lista.
func dominioAceito(email string, dominios []string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	i := strings.LastIndex(email, "@")
	if i <= 0 || i == len(email)-1 {
		return false
	}
	d := email[i+1:]
	for _, x := range dominios {
		if d == strings.ToLower(strings.TrimSpace(x)) {
			return true
		}
	}
	return false
}

// emailValido: validação simples (sem espaços, um @, domínio com ponto, sem caracteres de cabeçalho).
func emailValido(e string) bool {
	e = strings.TrimSpace(e)
	if len(e) < 6 || len(e) > 160 || strings.ContainsAny(e, " \t\r\n<>,;\"'()[]\\") {
		return false
	}
	i := strings.LastIndex(e, "@")
	if i <= 0 || strings.Count(e, "@") != 1 {
		return false
	}
	d := e[i+1:]
	return strings.Contains(d, ".") && !strings.HasPrefix(d, ".") && !strings.HasSuffix(d, ".") && !strings.Contains(d, "..")
}

func (b *Banco) SalvarConfigTermos(c ConfigTermos) error {
	for _, d := range c.Dominios {
		if !emailValido("x@" + d) {
			return fmt.Errorf("domínio inválido: %s", d)
		}
	}
	if len(c.Dominios) == 0 {
		return errors.New("informe pelo menos um domínio de e-mail aceito")
	}
	for _, e := range c.EmailsRH {
		if !emailValido(e) {
			return fmt.Errorf("e-mail do RH inválido: %s", e)
		}
	}
	um := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	for k, v := range map[string]string{
		"termos_ativo":         um(c.Ativo),
		"termos_permitir_http": um(c.PermitirHTTP),
		"termos_dominios":      strings.Join(c.Dominios, ", "),
		"termos_emails_rh":     strings.Join(c.EmailsRH, ", "),
		"termos_convite_dias":  strconv.Itoa(c.ConviteDias),
		"termos_lembrete_dias": strconv.Itoa(c.LembreteDias),
		"termos_atencao_dias":  strconv.Itoa(c.AtencaoDias),
	} {
		if err := b.DefinirConfig(k, v); err != nil {
			return err
		}
	}
	return nil
}

// ---------------- empresas e unidades ----------------

type Empresa struct {
	ID          int64
	RazaoSocial string
	CNPJ        string
	Unidades    []string
	Ativa       bool
}

func (e Empresa) CNPJFormatado() string { return formatarCNPJ(e.CNPJ) }
func (e Empresa) UnidadesTexto() string { return strings.Join(e.Unidades, "\n") }

func (b *Banco) Empresas(soAtivas bool) []Empresa {
	q := `SELECT id, razao_social, cnpj, unidades_json, ativa FROM empresas`
	if soAtivas {
		q += ` WHERE ativa=1`
	}
	rows, err := b.db.Query(q + ` ORDER BY razao_social`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []Empresa
	for rows.Next() {
		var e Empresa
		var uj string
		if rows.Scan(&e.ID, &e.RazaoSocial, &e.CNPJ, &uj, &e.Ativa) == nil {
			_ = json.Unmarshal([]byte(uj), &e.Unidades)
			l = append(l, e)
		}
	}
	return l
}

func (b *Banco) Empresa(id int64) (*Empresa, error) {
	var e Empresa
	var uj string
	err := b.db.QueryRow(`SELECT id, razao_social, cnpj, unidades_json, ativa FROM empresas WHERE id=?`, id).
		Scan(&e.ID, &e.RazaoSocial, &e.CNPJ, &uj, &e.Ativa)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(uj), &e.Unidades)
	return &e, nil
}

// SalvarEmpresa cria (id 0) ou atualiza uma empresa do grupo. Unidades: uma por linha.
func (b *Banco) SalvarEmpresa(id int64, razao, cnpj, unidades string, ativa bool) (int64, error) {
	razao = strings.TrimSpace(razao)
	if razao == "" || len([]rune(razao)) > 160 {
		return 0, errors.New("informe a razão social")
	}
	cnpj = apenasDigitos(cnpj)
	if cnpj != "" && !cnpjValido(cnpj) {
		return 0, errors.New("CNPJ inválido")
	}
	var us []string
	vistos := map[string]bool{}
	for _, u := range strings.Split(unidades, "\n") {
		u = strings.TrimSpace(u)
		if u != "" && !vistos[strings.ToLower(u)] && len([]rune(u)) <= 120 {
			vistos[strings.ToLower(u)] = true
			us = append(us, u)
		}
	}
	if us == nil {
		us = []string{}
	}
	uj, _ := json.Marshal(us)
	if id > 0 {
		_, err := b.db.Exec(`UPDATE empresas SET razao_social=?, cnpj=?, unidades_json=?, ativa=? WHERE id=?`, razao, cnpj, string(uj), ativa, id)
		return id, err
	}
	x, err := b.db.Exec(`INSERT INTO empresas(razao_social,cnpj,unidades_json,ativa) VALUES(?,?,?,?)`, razao, cnpj, string(uj), ativa)
	if err != nil {
		return 0, err
	}
	return x.LastInsertId()
}

// ExcluirEmpresa só apaga empresa sem ninguém vinculado; com vínculo, desativa.
func (b *Banco) ExcluirEmpresa(id int64) (desativada bool, err error) {
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM admins WHERE empresa_id=?`, id).Scan(&n)
	if n > 0 {
		_, err = b.db.Exec(`UPDATE empresas SET ativa=0 WHERE id=?`, id)
		return true, err
	}
	_, err = b.db.Exec(`DELETE FROM empresas WHERE id=?`, id)
	return false, err
}

// ---------------- ficha do colaborador ----------------

// Vínculos e regimes aceitos (o tipo de termo diz quais vínculos podem assinar; o Termo de
// Entrega bloqueia PJ — fase 2).
var vinculosFicha = []string{"clt", "estagio", "aprendiz", "temporario", "pj"}
var regimesFicha = []string{"presencial", "hibrido", "teletrabalho"}

func rotuloVinculo(v string) string {
	switch v {
	case "clt":
		return "CLT"
	case "estagio":
		return "Estágio"
	case "aprendiz":
		return "Jovem aprendiz"
	case "temporario":
		return "Temporário"
	case "pj":
		return "PJ (prestador)"
	}
	return v
}

func rotuloRegime(r string) string {
	switch r {
	case "presencial":
		return "Presencial"
	case "hibrido":
		return "Híbrido"
	case "teletrabalho":
		return "Teletrabalho"
	}
	return r
}

type Ficha struct {
	AdminID   int64
	Usuario   string
	Nome      string
	Email     string
	Papel     string
	LoginRede string
	CPF       string
	Matricula string
	Cargo     string
	Setor     string
	Unidade   string
	EmpresaID int64
	Empresa   string // razão social (leitura)
	Gestor    string
	Vinculo   string
	Regime    string
	Telefone  string

	PreenchidaEm string
	ValidadaPor  string
	ValidadaEm   string

	TemSenha      bool
	ConviteExpira string
	ConvidadoPor  string
	Ativo         bool
	CriadoEm      string
}

func (f Ficha) CPFFormatado() string  { return formatarCPF(f.CPF) }
func (f Ficha) RotuloVinculo() string { return rotuloVinculo(f.Vinculo) }
func (f Ficha) RotuloRegime() string  { return rotuloRegime(f.Regime) }
func (f Ficha) Validada() bool        { return f.ValidadaEm != "" }
func (f Ficha) Preenchida() bool      { return f.PreenchidaEm != "" }
func (f Ficha) ConvitePendente() bool { return !f.TemSenha && f.ConviteExpira != "" }
func (f Ficha) ConviteExpirado() bool { return f.ConvitePendente() && f.ConviteExpira < agora() }
func (f Ficha) NomeExibicao() string {
	if strings.TrimSpace(f.Nome) != "" {
		return f.Nome
	}
	return f.Usuario
}

// Situacao resume a conta para a lista de colaboradores.
func (f Ficha) Situacao() string {
	switch {
	case !f.Ativo:
		return "desativada"
	case f.ConviteExpirado():
		return "convite_expirado"
	case f.ConvitePendente():
		return "convite"
	case !f.Preenchida():
		return "ficha_incompleta"
	case !f.Validada():
		return "a_validar"
	}
	return "validada"
}

func (f Ficha) RotuloSituacao() string {
	switch f.Situacao() {
	case "desativada":
		return "Conta desativada"
	case "convite_expirado":
		return "Convite expirado"
	case "convite":
		return "Convite enviado"
	case "ficha_incompleta":
		return "Ficha incompleta"
	case "a_validar":
		return "Ficha a validar"
	}
	return "Ficha validada"
}

func (f Ficha) CorSituacao() string {
	switch f.Situacao() {
	case "validada":
		return "verde"
	case "a_validar", "convite":
		return "amarelo"
	case "convite_expirado", "ficha_incompleta":
		return "vermelho"
	}
	return ""
}

// Faltando lista os campos obrigatórios em branco (vazio = ficha completa).
func (f Ficha) Faltando(unidadesDaEmpresa []string) []string {
	var l []string
	if strings.TrimSpace(f.Nome) == "" {
		l = append(l, "nome completo")
	}
	if f.CPF == "" {
		l = append(l, "CPF")
	}
	if strings.TrimSpace(f.Cargo) == "" {
		l = append(l, "cargo")
	}
	if strings.TrimSpace(f.Setor) == "" {
		l = append(l, "setor")
	}
	if f.EmpresaID == 0 {
		l = append(l, "empresa")
	}
	if len(unidadesDaEmpresa) > 0 && f.Unidade == "" {
		l = append(l, "unidade")
	}
	if strings.TrimSpace(f.Gestor) == "" {
		l = append(l, "gestor imediato")
	}
	if f.Vinculo == "" {
		l = append(l, "vínculo")
	}
	if f.Regime == "" {
		l = append(l, "regime de trabalho")
	}
	return l
}

const colunasFicha = `a.id, a.usuario, COALESCE(a.nome,''), COALESCE(a.email,''), COALESCE(a.papel,'admin'),
	a.login_rede, a.cpf, a.matricula, a.cargo, a.setor, a.unidade, COALESCE(a.empresa_id,0), COALESCE(e.razao_social,''),
	a.gestor, a.vinculo, a.regime, a.telefone, a.ficha_preenchida_em, a.ficha_validada_por, a.ficha_validada_em,
	a.senha <> '', a.convite_expira, a.convidado_por, a.ativo, a.criado_em`

func lerFicha(sc interface{ Scan(...any) error }) (*Ficha, error) {
	var f Ficha
	err := sc.Scan(&f.AdminID, &f.Usuario, &f.Nome, &f.Email, &f.Papel,
		&f.LoginRede, &f.CPF, &f.Matricula, &f.Cargo, &f.Setor, &f.Unidade, &f.EmpresaID, &f.Empresa,
		&f.Gestor, &f.Vinculo, &f.Regime, &f.Telefone, &f.PreenchidaEm, &f.ValidadaPor, &f.ValidadaEm,
		&f.TemSenha, &f.ConviteExpira, &f.ConvidadoPor, &f.Ativo, &f.CriadoEm)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (b *Banco) Ficha(adminID int64) (*Ficha, error) {
	return lerFicha(b.db.QueryRow(`SELECT `+colunasFicha+` FROM admins a LEFT JOIN empresas e ON e.id=a.empresa_id WHERE a.id=?`, adminID))
}

// Colaboradores lista as contas para /admin/colaboradores. filtro: "" (todas) ou uma Situacao().
func (b *Banco) Colaboradores(busca, filtro string) []*Ficha {
	rows, err := b.db.Query(`SELECT ` + colunasFicha + ` FROM admins a LEFT JOIN empresas e ON e.id=a.empresa_id ORDER BY a.ativo DESC, COALESCE(NULLIF(a.nome,''), a.usuario) COLLATE NOCASE`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	busca = strings.ToLower(strings.TrimSpace(busca))
	var l []*Ficha
	for rows.Next() {
		f, err := lerFicha(rows)
		if err != nil {
			continue
		}
		if filtro != "" && f.Situacao() != filtro {
			continue
		}
		if busca != "" && !strings.Contains(strings.ToLower(f.Nome+" "+f.Usuario+" "+f.Email+" "+f.Setor+" "+f.LoginRede+" "+f.CPF+" "+f.Matricula), busca) {
			continue
		}
		l = append(l, f)
	}
	return l
}

// ContagemColaboradores devolve quantas contas há em cada situação (para o painel /termos).
func (b *Banco) ContagemColaboradores() map[string]int {
	m := map[string]int{}
	for _, f := range b.Colaboradores("", "") {
		m[f.Situacao()]++
	}
	return m
}

// DadosFicha: o que o colaborador (ou a T.I) envia pelo formulário.
type DadosFicha struct {
	Nome, CPF, Matricula, Cargo, Setor, Unidade, Gestor, Vinculo, Regime, Telefone string
	EmpresaID                                                                      int64
}

func temValor(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// normalizar valida e limpa os dados da ficha. Campos em branco são aceitos (a ficha fica
// incompleta); o que vier preenchido precisa ser válido.
func (b *Banco) normalizarFicha(d DadosFicha) (DadosFicha, []string, error) {
	limpa := func(s string, max int) (string, error) {
		s = strings.Join(strings.Fields(s), " ")
		if len([]rune(s)) > max {
			return "", fmt.Errorf("texto muito longo (máx. %d caracteres)", max)
		}
		return s, nil
	}
	var err error
	if d.Nome, err = limpa(d.Nome, 120); err != nil {
		return d, nil, err
	}
	if d.Cargo, err = limpa(d.Cargo, 80); err != nil {
		return d, nil, err
	}
	if d.Setor, err = limpa(d.Setor, 80); err != nil {
		return d, nil, err
	}
	if d.Gestor, err = limpa(d.Gestor, 120); err != nil {
		return d, nil, err
	}
	if d.Matricula, err = limpa(d.Matricula, 30); err != nil {
		return d, nil, err
	}
	d.CPF = apenasDigitos(d.CPF)
	if d.CPF != "" && !cpfValido(d.CPF) {
		return d, nil, errors.New("CPF inválido — confira os números")
	}
	d.Telefone = strings.TrimSpace(d.Telefone)
	if t := apenasDigitos(d.Telefone); d.Telefone != "" && (len(t) < 10 || len(t) > 13) {
		return d, nil, errors.New("telefone inválido — use DDD + número")
	}
	if d.Vinculo != "" && !temValor(vinculosFicha, d.Vinculo) {
		return d, nil, errors.New("vínculo inválido")
	}
	if d.Regime != "" && !temValor(regimesFicha, d.Regime) {
		return d, nil, errors.New("regime inválido")
	}
	var unidades []string
	if d.EmpresaID > 0 {
		e, err := b.Empresa(d.EmpresaID)
		if err != nil || !e.Ativa {
			return d, nil, errors.New("empresa inválida — escolha uma da lista")
		}
		unidades = e.Unidades
		d.Unidade = strings.TrimSpace(d.Unidade)
		if d.Unidade != "" && !temValor(e.Unidades, d.Unidade) {
			return d, nil, errors.New("unidade inválida — escolha uma da lista")
		}
		if len(e.Unidades) == 0 {
			d.Unidade = ""
		}
	} else {
		d.Unidade = ""
	}
	return d, unidades, nil
}

// SalvarFicha grava a ficha. porTI=true: a T.I corrigindo (não mexe na validação; quem valida
// é ValidarFicha). porTI=false: o próprio colaborador — qualquer mudança derruba a validação.
func (b *Banco) SalvarFicha(adminID int64, d DadosFicha, porTI bool) (completa bool, err error) {
	d, unidades, err := b.normalizarFicha(d)
	if err != nil {
		return false, err
	}
	if d.CPF != "" {
		var outro int64
		_ = b.db.QueryRow(`SELECT id FROM admins WHERE cpf=? AND id<>?`, d.CPF, adminID).Scan(&outro)
		if outro > 0 {
			return false, errors.New("esse CPF já está em outra conta — fale com a T.I")
		}
	}
	antes, err := b.Ficha(adminID)
	if err != nil {
		return false, err
	}
	f := *antes
	f.Nome, f.CPF, f.Matricula, f.Cargo, f.Setor, f.Unidade, f.EmpresaID = d.Nome, d.CPF, d.Matricula, d.Cargo, d.Setor, d.Unidade, d.EmpresaID
	f.Gestor, f.Vinculo, f.Regime, f.Telefone = d.Gestor, d.Vinculo, d.Regime, d.Telefone
	completa = len(f.Faltando(unidades)) == 0
	preenchida := ""
	if completa {
		preenchida = antes.PreenchidaEm
		if preenchida == "" || !porTI {
			preenchida = agora()
		}
	}
	mudou := antes.Nome != f.Nome || antes.CPF != f.CPF || antes.Matricula != f.Matricula || antes.Cargo != f.Cargo ||
		antes.Setor != f.Setor || antes.Unidade != f.Unidade || antes.EmpresaID != f.EmpresaID || antes.Gestor != f.Gestor ||
		antes.Vinculo != f.Vinculo || antes.Regime != f.Regime || antes.Telefone != f.Telefone
	validadaPor, validadaEm := antes.ValidadaPor, antes.ValidadaEm
	if (!porTI && mudou) || !completa {
		validadaPor, validadaEm = "", ""
	}
	if !mudou && !porTI {
		preenchida = antes.PreenchidaEm
		if completa && preenchida == "" {
			preenchida = agora()
		}
	}
	_, err = b.db.Exec(`UPDATE admins SET nome=?, cpf=?, matricula=?, cargo=?, setor=?, unidade=?, empresa_id=?, gestor=?, vinculo=?, regime=?, telefone=?,
		ficha_preenchida_em=?, ficha_validada_por=?, ficha_validada_em=? WHERE id=?`,
		f.Nome, f.CPF, f.Matricula, f.Cargo, f.Setor, f.Unidade, nullID(f.EmpresaID), f.Gestor, f.Vinculo, f.Regime, f.Telefone,
		preenchida, validadaPor, validadaEm, adminID)
	return completa, err
}

// ValidarFicha: a T.I confere a ficha (D10) e confirma o login de rede (D15). Só valida ficha completa.
func (b *Banco) ValidarFicha(adminID int64, loginRede, por string) error {
	f, err := b.Ficha(adminID)
	if err != nil {
		return err
	}
	var unidades []string
	if f.EmpresaID > 0 {
		if e, err := b.Empresa(f.EmpresaID); err == nil {
			unidades = e.Unidades
		}
	}
	if falta := f.Faltando(unidades); len(falta) > 0 {
		return fmt.Errorf("a ficha ainda não está completa: falta %s", strings.Join(falta, ", "))
	}
	if err := b.DefinirLoginRede(adminID, loginRede); err != nil {
		return err
	}
	pre := f.PreenchidaEm
	if pre == "" {
		pre = agora()
	}
	_, err = b.db.Exec(`UPDATE admins SET ficha_validada_por=?, ficha_validada_em=?, ficha_preenchida_em=? WHERE id=?`, por, agora(), pre, adminID)
	return err
}

// RevogarValidacao volta a ficha para "a validar".
func (b *Banco) RevogarValidacao(adminID int64) error {
	_, err := b.db.Exec(`UPDATE admins SET ficha_validada_por='', ficha_validada_em='' WHERE id=?`, adminID)
	return err
}

// DefinirLoginRede liga a conta a um login do inventário (tabela usuarios). Vazio = sem vínculo.
func (b *Banco) DefinirLoginRede(adminID int64, login string) error {
	login = strings.TrimSpace(login)
	if login != "" {
		var achou string
		err := b.db.QueryRow(`SELECT login FROM usuarios WHERE login=? COLLATE NOCASE`, login).Scan(&achou)
		if errors.Is(err, sql.ErrNoRows) {
			// aceita também um apelido (login antigo mesclado) e grava o login principal
			err = b.db.QueryRow(`SELECT u.login FROM usuario_apelidos a JOIN usuarios u ON u.id=a.usuario_id WHERE a.apelido=? COLLATE NOCASE`, login).Scan(&achou)
		}
		if err != nil {
			return fmt.Errorf("o login de rede %q não aparece no inventário (nenhuma máquina informou esse usuário)", login)
		}
		login = achou
		var outro string
		_ = b.db.QueryRow(`SELECT usuario FROM admins WHERE login_rede=? COLLATE NOCASE AND id<>?`, login, adminID).Scan(&outro)
		if outro != "" {
			return fmt.Errorf("o login de rede %s já está ligado à conta %s", login, outro)
		}
	}
	_, err := b.db.Exec(`UPDATE admins SET login_rede=? WHERE id=?`, login, adminID)
	return err
}

// SugestaoLogin: candidato do inventário para o login de rede da conta.
type SugestaoLogin struct {
	Login       string
	Nome        string
	UltimoVisto string
	Maquinas    int
}

// SugerirLoginRede procura no inventário logins cuja parte curta (sem domínio) bate com a parte do
// e-mail antes do @ (ex.: joao.silva@ocupacional.com.br → OCUPACIONAL\joao.silva).
func (b *Banco) SugerirLoginRede(email string) []SugestaoLogin {
	curto := chaveCurta(email)
	if curto == "" {
		return nil
	}
	rows, err := b.db.Query(`SELECT u.login, u.nome, u.ultimo_visto, (SELECT COUNT(*) FROM logins l WHERE l.usuario_id=u.id)
		FROM usuarios u WHERE u.login LIKE ? ESCAPE '!' OR u.login=? COLLATE NOCASE`, `%\`+escaparLike(curto), curto)
	if err != nil {
		return nil
	}
	var l []SugestaoLogin
	for rows.Next() {
		var s SugestaoLogin
		if rows.Scan(&s.Login, &s.Nome, &s.UltimoVisto, &s.Maquinas) == nil && chaveCurta(s.Login) == curto {
			l = append(l, s)
		}
	}
	rows.Close()
	sort.Slice(l, func(i, j int) bool { return l[i].UltimoVisto > l[j].UltimoVisto })
	return l
}

// DesativarConta (desligamento): a conta não entra mais, as sessões caem, nada é apagado (LGPD:
// os termos ficam pelo prazo de guarda). Não deixa desativar o último administrador.
func (b *Banco) DesativarConta(adminID int64, ativo bool) error {
	if !ativo {
		var papel string
		_ = b.db.QueryRow(`SELECT papel FROM admins WHERE id=?`, adminID).Scan(&papel)
		if papel == "admin" {
			var n int
			_ = b.db.QueryRow(`SELECT COUNT(*) FROM admins WHERE papel='admin' AND ativo=1`).Scan(&n)
			if n <= 1 {
				return errors.New("é preciso manter pelo menos um administrador ativo")
			}
		}
	}
	_, err := b.db.Exec(`UPDATE admins SET ativo=? WHERE id=?`, ativo, adminID)
	if err == nil && !ativo {
		_, _ = b.db.Exec(`DELETE FROM sessoes WHERE admin_id=?`, adminID)
	}
	return err
}

// FichasAValidar: contas ativas com ficha preenchida e ainda não validadas.
func (b *Banco) FichasAValidar() []*Ficha { return b.Colaboradores("", "a_validar") }

// ---------------- CPF / CNPJ ----------------

func apenasDigitos(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func cpfValido(c string) bool {
	if len(c) != 11 || strings.Count(c, c[:1]) == 11 {
		return false
	}
	dv := func(n int) int {
		s := 0
		for i := 0; i < n; i++ {
			s += int(c[i]-'0') * (n + 1 - i)
		}
		r := s * 10 % 11
		if r == 10 {
			r = 0
		}
		return r
	}
	return dv(9) == int(c[9]-'0') && dv(10) == int(c[10]-'0')
}

func cnpjValido(c string) bool {
	if len(c) != 14 || strings.Count(c, c[:1]) == 14 {
		return false
	}
	dv := func(n int) int {
		pesos := []int{6, 5, 4, 3, 2, 9, 8, 7, 6, 5, 4, 3, 2}[13-n:]
		s := 0
		for i := 0; i < n; i++ {
			s += int(c[i]-'0') * pesos[i]
		}
		r := s % 11
		if r < 2 {
			return 0
		}
		return 11 - r
	}
	return dv(12) == int(c[12]-'0') && dv(13) == int(c[13]-'0')
}

func formatarCPF(c string) string {
	if len(c) != 11 {
		return c
	}
	return c[:3] + "." + c[3:6] + "." + c[6:9] + "-" + c[9:]
}

func formatarCNPJ(c string) string {
	if len(c) != 14 {
		return c
	}
	return c[:2] + "." + c[2:5] + "." + c[5:8] + "/" + c[8:12] + "-" + c[12:]
}

// prazoConvite devolve o fim da validade do convite.
func prazoConvite(dias int) string {
	return time.Now().UTC().Add(time.Duration(dias) * 24 * time.Hour).Format(time.RFC3339)
}
