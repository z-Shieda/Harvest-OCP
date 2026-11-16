package servidor

// Equipamentos e valores automáticos dos termos (1.23): gestão do ativo na ficha da máquina
// (D13), ativos manuais sem agente (celular, monitor) e o catálogo de campos que o Harvest
// preenche sozinho (colaborador, equipamento, termo).

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// fusoBrasilia: horários dos termos sempre em UTC-3 (o Brasil não tem horário de verão desde 2019).
var fusoBrasilia = time.FixedZone("UTC-3", -3*3600)

func dataHoraBrasilia(rfc string) string {
	t, ok := parseData(rfc)
	if !ok {
		return rfc
	}
	return t.In(fusoBrasilia).Format("02/01/2006 15:04") + " (UTC-3)"
}

func dataBrasilia(rfc string) string {
	t, ok := parseData(rfc)
	if !ok {
		return rfc
	}
	return t.In(fusoBrasilia).Format("02/01/2006")
}

var tiposEquip = []string{"desktop", "notebook", "celular", "tablet", "monitor", "outro"}
var condicoesEquip = []string{"novo", "usado", "reutilizado"}

func rotuloTipoEquip(t string) string {
	switch t {
	case "desktop":
		return "Desktop"
	case "notebook":
		return "Notebook"
	case "celular":
		return "Celular"
	case "tablet":
		return "Tablet"
	case "monitor":
		return "Monitor"
	case "outro":
		return "Outro"
	}
	return t
}

func rotuloCondicao(c string) string {
	switch c {
	case "novo":
		return "Novo"
	case "usado":
		return "Usado"
	case "reutilizado":
		return "Reutilizado"
	}
	return c
}

// GestaoMaquina: dados de gestão do ativo e de segurança (agente 1.22) de uma máquina.
type GestaoMaquina struct {
	Tipo, Condicao, NFNumero, NFData, Valor string
	BitLocker, Antivirus, AntivirusOK       string
	AdminsLocais, UltimaAtualizacao         string
	SegColetadoEm                           string
}

func (g GestaoMaquina) RotuloTipo() string     { return rotuloTipoEquip(g.Tipo) }
func (g GestaoMaquina) RotuloCondicao() string { return rotuloCondicao(g.Condicao) }

func (b *Banco) GestaoDaMaquina(id int64) GestaoMaquina {
	var g GestaoMaquina
	_ = b.db.QueryRow(`SELECT tipo_equip, condicao, nf_numero, nf_data, valor_referencia, seg_bitlocker, seg_antivirus, seg_antivirus_ok,
		seg_admins_locais, seg_ultima_atualizacao, seg_coletado_em FROM maquinas WHERE id=?`, id).
		Scan(&g.Tipo, &g.Condicao, &g.NFNumero, &g.NFData, &g.Valor, &g.BitLocker, &g.Antivirus, &g.AntivirusOK, &g.AdminsLocais, &g.UltimaAtualizacao, &g.SegColetadoEm)
	return g
}

// normalizarValor aceita "1.234,56", "1234.56", "R$ 1.234" e devolve "1234.56".
func normalizarValor(v string) (string, error) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "R$"))
	if v == "" {
		return "", nil
	}
	v = strings.ReplaceAll(v, " ", "")
	if strings.Contains(v, ",") {
		v = strings.ReplaceAll(v, ".", "")
		v = strings.Replace(v, ",", ".", 1)
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 10_000_000 {
		return "", errors.New("valor de referência inválido (use, por exemplo, 3.500,00)")
	}
	return strconv.FormatFloat(f, 'f', 2, 64), nil
}

// formatarReais: "3500.00" → "R$ 3.500,00".
func formatarReais(v string) string {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return v
	}
	inteiro := int64(f)
	cent := int64((f-float64(inteiro))*100 + 0.5)
	if cent == 100 {
		inteiro, cent = inteiro+1, 0
	}
	s := strconv.FormatInt(inteiro, 10)
	var partes []string
	for len(s) > 3 {
		partes = append([]string{s[len(s)-3:]}, partes...)
		s = s[:len(s)-3]
	}
	partes = append([]string{s}, partes...)
	return fmt.Sprintf("R$ %s,%02d", strings.Join(partes, "."), cent)
}

func validarGestao(g *GestaoMaquina) error {
	if g.Tipo != "" && !temValor(tiposEquip, g.Tipo) {
		return errors.New("tipo de equipamento inválido")
	}
	if g.Condicao != "" && !temValor(condicoesEquip, g.Condicao) {
		return errors.New("condição inválida")
	}
	g.NFNumero = limitar(g.NFNumero, 40)
	if g.NFData != "" && !reDataHora.MatchString(g.NFData) {
		return errors.New("data da nota fiscal inválida")
	}
	v, err := normalizarValor(g.Valor)
	if err != nil {
		return err
	}
	g.Valor = v
	return nil
}

func (b *Banco) SalvarGestaoMaquina(id int64, g GestaoMaquina) error {
	if err := validarGestao(&g); err != nil {
		return err
	}
	_, err := b.db.Exec(`UPDATE maquinas SET tipo_equip=?, condicao=?, nf_numero=?, nf_data=?, valor_referencia=? WHERE id=?`,
		g.Tipo, g.Condicao, g.NFNumero, g.NFData, g.Valor, id)
	return err
}

// ---------------- ativos manuais ----------------

type AtivoManual struct {
	ID                                                  int64
	Tipo, Fabricante, Modelo, Serial, Patrimonio        string
	IMEI, Linha, Condicao, NFNumero, NFData, Valor, Obs string
	Ativo                                               bool
	CriadoEm, CriadoPor                                 string
}

func (a AtivoManual) Nome() string {
	n := strings.TrimSpace(rotuloTipoEquip(a.Tipo) + " " + a.Fabricante + " " + a.Modelo)
	if a.Patrimonio != "" {
		n += " · pat. " + a.Patrimonio
	} else if a.Serial != "" {
		n += " · série " + a.Serial
	}
	return n
}

func (a AtivoManual) RotuloCondicao() string { return rotuloCondicao(a.Condicao) }
func (a AtivoManual) ValorFormatado() string {
	if a.Valor == "" {
		return ""
	}
	return formatarReais(a.Valor)
}

const colunasAtivo = `id, tipo, fabricante, modelo, serial, patrimonio, imei, linha, condicao, nf_numero, nf_data, valor_referencia, observacoes, ativo, criado_em, criado_por`

func lerAtivo(sc interface{ Scan(...any) error }) (*AtivoManual, error) {
	var a AtivoManual
	err := sc.Scan(&a.ID, &a.Tipo, &a.Fabricante, &a.Modelo, &a.Serial, &a.Patrimonio, &a.IMEI, &a.Linha, &a.Condicao, &a.NFNumero, &a.NFData, &a.Valor, &a.Obs, &a.Ativo, &a.CriadoEm, &a.CriadoPor)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (b *Banco) AtivosManuais(soAtivos bool) []*AtivoManual {
	q := `SELECT ` + colunasAtivo + ` FROM ativos_manuais`
	if soAtivos {
		q += ` WHERE ativo=1`
	}
	rows, err := b.db.Query(q + ` ORDER BY tipo, fabricante, modelo`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []*AtivoManual
	for rows.Next() {
		if a, err := lerAtivo(rows); err == nil {
			l = append(l, a)
		}
	}
	return l
}

func (b *Banco) AtivoManual(id int64) (*AtivoManual, error) {
	return lerAtivo(b.db.QueryRow(`SELECT `+colunasAtivo+` FROM ativos_manuais WHERE id=?`, id))
}

func (b *Banco) SalvarAtivoManual(a AtivoManual, por string) (int64, error) {
	g := GestaoMaquina{Tipo: a.Tipo, Condicao: a.Condicao, NFNumero: a.NFNumero, NFData: a.NFData, Valor: a.Valor}
	if err := validarGestao(&g); err != nil {
		return 0, err
	}
	if a.Tipo == "" {
		return 0, errors.New("informe o tipo do equipamento")
	}
	a.NFNumero, a.Valor = g.NFNumero, g.Valor
	for _, p := range []*string{&a.Fabricante, &a.Modelo, &a.Serial, &a.Patrimonio, &a.IMEI, &a.Linha} {
		*p = limitar(*p, 80)
	}
	a.Obs = limitar(a.Obs, 500)
	if a.Serial == "" && a.Patrimonio == "" && a.IMEI == "" {
		return 0, errors.New("informe pelo menos o nº de série, o patrimônio ou o IMEI")
	}
	if a.ID > 0 {
		_, err := b.db.Exec(`UPDATE ativos_manuais SET tipo=?, fabricante=?, modelo=?, serial=?, patrimonio=?, imei=?, linha=?, condicao=?, nf_numero=?, nf_data=?, valor_referencia=?, observacoes=?, ativo=? WHERE id=?`,
			a.Tipo, a.Fabricante, a.Modelo, a.Serial, a.Patrimonio, a.IMEI, a.Linha, a.Condicao, a.NFNumero, a.NFData, a.Valor, a.Obs, a.Ativo, a.ID)
		return a.ID, err
	}
	x, err := b.db.Exec(`INSERT INTO ativos_manuais(tipo,fabricante,modelo,serial,patrimonio,imei,linha,condicao,nf_numero,nf_data,valor_referencia,observacoes,ativo,criado_em,criado_por)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,1,?,?)`, a.Tipo, a.Fabricante, a.Modelo, a.Serial, a.Patrimonio, a.IMEI, a.Linha, a.Condicao, a.NFNumero, a.NFData, a.Valor, a.Obs, agora(), por)
	if err != nil {
		return 0, err
	}
	return x.LastInsertId()
}

// ---------------- equipamento do termo ----------------

// Equipamento: visão única de uma máquina do inventário ou de um ativo manual.
type Equipamento struct {
	MaquinaID, AtivoID                                 int64
	Nome                                               string
	Tipo, FabricanteModelo, Serial, Patrimonio, Host   string
	SO, Hardware, MAC, IMEILinha, Condicao, NF, Valor  string
}

func (b *Banco) EquipamentoMaquina(id int64) (*Equipamento, error) {
	m, err := b.Maquina(id)
	if err != nil {
		return nil, err
	}
	g := b.GestaoDaMaquina(id)
	e := &Equipamento{MaquinaID: id, Nome: m.Hostname, Tipo: rotuloTipoEquip(g.Tipo), Serial: m.Serial, Patrimonio: m.Patrimonio, Host: m.Hostname,
		Condicao: rotuloCondicao(g.Condicao)}
	e.FabricanteModelo = strings.TrimSpace(strings.Join(naoVazios(m.Fabricante, m.Modelo), " / "))
	e.SO = strings.TrimSpace(m.SO + " " + m.SOVersao)
	e.Hardware = strings.Join(naoVazios(m.CPU, formatarTamanho(uint64(max(m.RAM, 0)))+" RAM", strings.TrimSpace(m.DiscosResumo)), " / ")
	e.MAC = primeiroMAC(m.MACs)
	if g.NFNumero != "" || g.NFData != "" {
		e.NF = strings.Join(naoVazios("NF "+g.NFNumero, formatarDataHoraCampo(g.NFData)), " · ")
	}
	if g.Valor != "" {
		e.Valor = formatarReais(g.Valor)
	}
	if m.Patrimonio != "" {
		e.Nome += " · pat. " + m.Patrimonio
	}
	return e, nil
}

func (b *Banco) EquipamentoAtivo(id int64) (*Equipamento, error) {
	a, err := b.AtivoManual(id)
	if err != nil {
		return nil, err
	}
	e := &Equipamento{AtivoID: id, Nome: a.Nome(), Tipo: rotuloTipoEquip(a.Tipo), Serial: a.Serial, Patrimonio: a.Patrimonio,
		Condicao: rotuloCondicao(a.Condicao), Valor: a.ValorFormatado()}
	e.FabricanteModelo = strings.Join(naoVazios(a.Fabricante, a.Modelo), " / ")
	e.IMEILinha = strings.Join(naoVazios(a.IMEI, a.Linha), " / ")
	if a.NFNumero != "" || a.NFData != "" {
		e.NF = strings.Join(naoVazios("NF "+a.NFNumero, formatarDataHoraCampo(a.NFData)), " · ")
	}
	return e, nil
}

func naoVazios(l ...string) []string {
	var r []string
	for _, s := range l {
		if s = strings.TrimSpace(s); s != "" && s != "-" && s != "- RAM" && s != "NF" {
			r = append(r, s)
		}
	}
	return r
}

func primeiroMAC(macs string) string {
	for _, m := range strings.FieldsFunc(macs, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if m = strings.TrimSpace(m); m != "" {
			return m
		}
	}
	return ""
}

// valoresAuto monta os campos automáticos a partir da ficha, da empresa e do equipamento.
func (b *Banco) valoresAuto(v *ValoresTermo, f *Ficha, eq *Equipamento) {
	t := v.Texto
	if f != nil {
		t["colaborador_nome"] = f.Nome
		t["colaborador_cpf"] = f.CPFFormatado()
		t["colaborador_matricula"] = f.Matricula
		t["colaborador_cpf_matricula"] = strings.Join(naoVazios(f.CPFFormatado(), f.Matricula), " / ")
		t["colaborador_email"] = vazioPadrao(f.Email, f.Usuario)
		t["colaborador_cargo"] = f.Cargo
		t["colaborador_setor"] = f.Setor
		t["colaborador_unidade"] = f.Unidade
		t["colaborador_gestor"] = f.Gestor
		t["colaborador_vinculo"] = f.RotuloVinculo()
		t["colaborador_vinculo_chave"] = f.Vinculo
		t["colaborador_regime"] = f.RotuloRegime()
		t["colaborador_regime_chave"] = f.Regime
		t["colaborador_telefone"] = f.Telefone
		t["colaborador_empresa"] = ""
		if f.EmpresaID > 0 {
			if e, err := b.Empresa(f.EmpresaID); err == nil {
				t["colaborador_empresa"] = strings.Join(naoVazios(e.RazaoSocial, e.CNPJFormatado()), " – CNPJ ")
			}
		}
	}
	if eq != nil {
		t["equipamento_tipo"] = eq.Tipo
		t["equipamento_fabricante_modelo"] = eq.FabricanteModelo
		t["equipamento_serial"] = eq.Serial
		t["equipamento_patrimonio"] = eq.Patrimonio
		t["equipamento_hostname"] = eq.Host
		t["equipamento_so"] = eq.SO
		t["equipamento_hardware"] = eq.Hardware
		t["equipamento_mac"] = eq.MAC
		t["equipamento_imei_linha"] = eq.IMEILinha
		t["equipamento_condicao"] = eq.Condicao
		t["equipamento_nf"] = eq.NF
		t["equipamento_valor"] = eq.Valor
	}
	t["data_hoje"] = time.Now().In(fusoBrasilia).Format("02/01/2006")
}
