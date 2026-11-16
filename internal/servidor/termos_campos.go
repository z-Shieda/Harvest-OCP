package servidor

// Campos declarados e marcação do texto dos termos (1.23, fase 2 — editor de termos).
//
// MARCAÇÃO DO TEXTO (uma linha = um bloco; linhas seguidas formam um parágrafo):
//   # Título                      título principal (centralizado)
//   ## 1. Seção                   faixa colorida de seção
//   ### Subtítulo                 subtítulo em negrito
//   6.1 Texto do item             item numerado (o número sai em negrito)
//   - texto                       marcador (•)
//   > texto                       destaque (caixa)
//   | a | b | c |                 linha de tabela (2 a 6 colunas); "|---|" separa o cabeçalho
//   {campo}                       sozinho na linha: o campo em bloco (checklist vira tabela)
//   {aceites} / {evidencias}      caixas de aceite e quadro de evidências da assinatura
//   [[devolucao]] … [[/devolucao]] só aparece depois que a devolução começa
//   ===                           quebra de página
//   **negrito** e {campo} valem em qualquer texto, inclusive nas tabelas.
//
// CAMPOS (um por linha):  chave | Rótulo | tipo | quem | opções…
//   tipo: auto, texto, textolongo, opcao, checklist, fotos, datahora
//   quem: auto (Harvest), ti (antes de enviar), colaborador (ao assinar), devolucao (T.I no encerramento)
//   opções: itens=A;B;C · colunas=Sim;Não · obs=Rótulo da observação · opcoes=a;b · minimo=4
//           · coluna=Item (cabeçalho da 1ª coluna da checklist) · opcional · se=regime:hibrido,teletrabalho (obrigatório só nessa condição)
//
// ACEITES (um por linha):  Texto da caixa | doc=psi   (doc = documento de referência, opcional)

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type CampoTermo struct {
	Chave    string
	Rotulo   string
	Tipo     string // auto | texto | textolongo | opcao | checklist | fotos | datahora
	Quem     string // auto | ti | colaborador | devolucao
	Itens    []string
	Colunas  []string
	Obs      string // rótulo da coluna de observação (checklist)
	Coluna   string // cabeçalho da 1ª coluna da checklist (padrão: o rótulo)
	Opcoes   []string
	Minimo   int // fotos
	Opcional bool
	SeCampo  string   // condição: campo (ex.: regime)
	SeValor  []string // valores que tornam obrigatório
}

type AceiteTermo struct {
	Texto string
	Doc   string // chave do documento de referência (ex.: psi)
}

var tiposCampo = map[string]string{
	"auto": "Automático", "texto": "Texto", "textolongo": "Texto longo", "opcao": "Opção",
	"checklist": "Checklist", "fotos": "Fotos", "datahora": "Data e hora",
}
var quemCampo = map[string]string{"auto": "Harvest", "ti": "T.I", "colaborador": "Colaborador", "devolucao": "T.I na devolução"}

// camposAuto: o que o Harvest sabe preencher (catálogo para o tipo "auto").
var camposAuto = map[string]string{
	"termo_numero": "Nº do termo", "termo_versao": "Versão do modelo", "termo_emitido_em": "Emitido em",
	"chamado_numero": "Nº do chamado", "data_hoje": "Data de hoje",
	"colaborador_nome": "Nome completo", "colaborador_cpf": "CPF", "colaborador_matricula": "Matrícula",
	"colaborador_cpf_matricula": "CPF / matrícula", "colaborador_email": "E-mail corporativo",
	"colaborador_cargo": "Cargo / função", "colaborador_setor": "Setor", "colaborador_empresa": "Empresa (razão social e CNPJ)",
	"colaborador_unidade": "Unidade", "colaborador_gestor": "Gestor imediato", "colaborador_vinculo": "Tipo de vínculo",
	"colaborador_regime": "Regime de trabalho", "colaborador_telefone": "Telefone",
	"equipamento_tipo": "Tipo do equipamento", "equipamento_fabricante_modelo": "Fabricante / modelo",
	"equipamento_serial": "Nº de série", "equipamento_patrimonio": "Nº de patrimônio", "equipamento_hostname": "Nome do computador",
	"equipamento_so": "Sistema operacional", "equipamento_hardware": "Processador / RAM / disco", "equipamento_mac": "Endereço MAC",
	"equipamento_imei_linha": "IMEI / linha", "equipamento_condicao": "Condição", "equipamento_nf": "Nota fiscal / aquisição",
	"equipamento_valor": "Valor de referência", "entregue_por": "Entregue por (T.I)", "entregue_em": "Data e hora da entrega",
	"recebido_por": "Recebido por (T.I)", "fotos_entrega": "Fotos da entrega", "fotos_devolucao": "Fotos da devolução",
}

// campos que o sistema preenche no envio/assinatura (não bloqueiam a emissão quando vazios)
var autoNoEnvio = map[string]bool{"entregue_por": true, "entregue_em": true, "recebido_por": true, "fotos_entrega": true, "fotos_devolucao": true}

func (c CampoTermo) RotuloTipo() string { return tiposCampo[c.Tipo] }
func (c CampoTermo) RotuloQuem() string { return quemCampo[c.Quem] }

// CabecalhoItens: título da 1ª coluna da checklist.
func (c CampoTermo) CabecalhoItens() string {
	if c.Coluna != "" {
		return c.Coluna
	}
	return c.Rotulo
}

// ColunasChecklist: colunas da checklist (padrão Sim / Não).
func (c CampoTermo) ColunasChecklist() []string {
	if len(c.Colunas) == 0 {
		return []string{"Sim", "Não"}
	}
	return c.Colunas
}

var reChave = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)

func dividir(s, sep string) []string {
	var l []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			l = append(l, p)
		}
	}
	return l
}

// LerCampos interpreta a definição de campos. Erros citam a linha.
func LerCampos(def string) ([]CampoTermo, error) {
	var l []CampoTermo
	vistos := map[string]bool{}
	for i, linha := range strings.Split(def, "\n") {
		linha = strings.TrimSpace(linha)
		if linha == "" || strings.HasPrefix(linha, "#") {
			continue
		}
		erro := func(f string, a ...any) error { return fmt.Errorf("campos, linha %d: %s", i+1, fmt.Sprintf(f, a...)) }
		p := strings.Split(linha, "|")
		for k := range p {
			p[k] = strings.TrimSpace(p[k])
		}
		if len(p) < 4 {
			return nil, erro("use  chave | Rótulo | tipo | quem")
		}
		c := CampoTermo{Chave: p[0], Rotulo: p[1], Tipo: strings.ToLower(p[2]), Quem: strings.ToLower(p[3])}
		if !reChave.MatchString(c.Chave) {
			return nil, erro("chave %q inválida (letras minúsculas, números e _)", c.Chave)
		}
		if vistos[c.Chave] {
			return nil, erro("chave %q repetida", c.Chave)
		}
		vistos[c.Chave] = true
		if _, ok := tiposCampo[c.Tipo]; !ok {
			return nil, erro("tipo %q desconhecido (auto, texto, textolongo, opcao, checklist, fotos, datahora)", c.Tipo)
		}
		if _, ok := quemCampo[c.Quem]; !ok {
			return nil, erro("quem preenche %q desconhecido (auto, ti, colaborador, devolucao)", c.Quem)
		}
		if (c.Tipo == "auto") != (c.Quem == "auto") {
			return nil, erro("campo automático usa tipo auto e quem auto")
		}
		if c.Tipo == "auto" {
			if _, ok := camposAuto[c.Chave]; !ok {
				return nil, erro("o Harvest não sabe preencher %q (veja a lista de campos automáticos)", c.Chave)
			}
		}
		for _, o := range p[4:] {
			k, v, _ := strings.Cut(o, "=")
			k = strings.ToLower(strings.TrimSpace(k))
			v = strings.TrimSpace(v)
			switch k {
			case "itens":
				c.Itens = dividir(v, ";")
			case "colunas":
				c.Colunas = dividir(v, ";")
			case "obs":
				c.Obs = v
			case "coluna":
				c.Coluna = v
			case "opcoes":
				c.Opcoes = dividir(v, ";")
			case "minimo":
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 || n > 30 {
					return nil, erro("minimo inválido")
				}
				c.Minimo = n
			case "opcional":
				c.Opcional = true
			case "se":
				campo, vals, ok := strings.Cut(v, ":")
				if !ok {
					return nil, erro("use se=campo:valor1,valor2")
				}
				c.SeCampo, c.SeValor = strings.TrimSpace(campo), dividir(vals, ",")
			case "":
			default:
				return nil, erro("opção %q desconhecida", k)
			}
		}
		if c.Rotulo == "" {
			c.Rotulo = c.Chave
		}
		if c.Tipo == "checklist" && len(c.Itens) == 0 {
			return nil, erro("checklist precisa de itens=A;B;C")
		}
		if c.Tipo == "opcao" && len(c.Opcoes) == 0 {
			return nil, erro("opção precisa de opcoes=a;b")
		}
		if c.Tipo == "fotos" && c.Quem == "colaborador" {
			return nil, erro("fotos são anexadas pela T.I no chamado")
		}
		l = append(l, c)
	}
	return l, nil
}

func LerAceites(def string) ([]AceiteTermo, error) {
	var l []AceiteTermo
	for i, linha := range strings.Split(def, "\n") {
		linha = strings.TrimSpace(linha)
		if linha == "" {
			continue
		}
		p := strings.Split(linha, "|")
		a := AceiteTermo{Texto: strings.TrimSpace(p[0])}
		for _, o := range p[1:] {
			k, v, _ := strings.Cut(strings.TrimSpace(o), "=")
			if strings.TrimSpace(k) != "doc" {
				return nil, fmt.Errorf("aceites, linha %d: opção %q desconhecida (use doc=chave)", i+1, k)
			}
			a.Doc = strings.TrimSpace(v)
		}
		if a.Texto == "" {
			return nil, fmt.Errorf("aceites, linha %d: texto vazio", i+1)
		}
		l = append(l, a)
	}
	return l, nil
}

var reCampoTexto = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)

// camposDoSistema podem aparecer no texto sem declaração.
var camposDoSistema = map[string]bool{"aceites": true, "evidencias": true}

// ConferirModelo valida corpo + campos + aceites juntos. Devolve avisos (não bloqueiam).
func ConferirModelo(corpo, camposDef, aceitesDef string, docs map[string]bool) (avisos []string, err error) {
	campos, err := LerCampos(camposDef)
	if err != nil {
		return nil, err
	}
	aceites, err := LerAceites(aceitesDef)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(corpo) == "" {
		return nil, errors.New("o texto do termo está vazio")
	}
	decl := map[string]CampoTermo{}
	for _, c := range campos {
		decl[c.Chave] = c
	}
	usados := map[string]bool{}
	var faltam []string
	for _, m := range reCampoTexto.FindAllStringSubmatch(corpo, -1) {
		usados[m[1]] = true
		if _, ok := decl[m[1]]; !ok && !camposDoSistema[m[1]] && !usadoAuto(m[1]) {
			faltam = append(faltam, m[1])
		}
	}
	if len(faltam) > 0 {
		sort.Strings(faltam)
		return nil, fmt.Errorf("o texto usa campo(s) não declarado(s): %s", strings.Join(unicos(faltam), ", "))
	}
	for _, c := range campos {
		if c.SeCampo != "" {
			if _, ok := decl[c.SeCampo]; !ok && !usadoAuto("colaborador_"+c.SeCampo) && c.SeCampo != "regime" && c.SeCampo != "vinculo" {
				return nil, fmt.Errorf("campo %s: a condição se= aponta para %q, que não existe", c.Chave, c.SeCampo)
			}
		}
		if !usados[c.Chave] && c.Tipo != "fotos" {
			avisos = append(avisos, "o campo "+c.Chave+" foi declarado mas não aparece no texto")
		}
	}
	for _, a := range aceites {
		if a.Doc != "" && !docs[a.Doc] {
			return nil, fmt.Errorf("o aceite %q aponta para o documento %q, que não existe em Documentos", a.Texto, a.Doc)
		}
	}
	if len(aceites) == 0 {
		avisos = append(avisos, "nenhum aceite declarado: o colaborador assina sem marcar caixas")
	}
	if !usados["evidencias"] {
		avisos = append(avisos, "o texto não tem {evidencias}: o quadro de evidências vai no fim do PDF")
	}
	return avisos, nil
}

// usadoAuto: campos automáticos podem ser usados no texto sem declarar (o Harvest preenche).
func usadoAuto(k string) bool { _, ok := camposAuto[k]; return ok }

func unicos(l []string) []string {
	var r []string
	for i, s := range l {
		if i == 0 || l[i-1] != s {
			r = append(r, s)
		}
	}
	return r
}

// ---------------- valores ----------------

// ValorChecklist: resposta de uma linha da checklist.
type ValorChecklist struct {
	V   string `json:"v"`             // coluna escolhida
	Obs string `json:"obs,omitempty"` // observação
}

// Valores de um termo: texto (auto, texto, opcao, datahora) ou checklist (item -> resposta).
type ValoresTermo struct {
	Texto      map[string]string                    `json:"texto"`
	Checklists map[string]map[string]ValorChecklist `json:"checklists"`
}

func novosValores() ValoresTermo {
	return ValoresTermo{Texto: map[string]string{}, Checklists: map[string]map[string]ValorChecklist{}}
}

// obrigatorio diz se o campo precisa de valor, considerando a condição se=.
func (c CampoTermo) obrigatorio(v ValoresTermo) bool {
	if c.Opcional {
		return false
	}
	if c.SeCampo == "" {
		return true
	}
	atual := v.Texto[c.SeCampo]
	if atual == "" {
		atual = v.Texto["colaborador_"+c.SeCampo+"_chave"]
	}
	for _, x := range c.SeValor {
		if strings.EqualFold(x, atual) {
			return true
		}
	}
	return false
}

// Pendencias lista o que falta entre os campos de um grupo (quem), em linguagem simples.
func Pendencias(campos []CampoTermo, v ValoresTermo, quem string, fotos int) []string {
	var l []string
	for _, c := range campos {
		if c.Quem != quem || !c.obrigatorio(v) {
			continue
		}
		switch c.Tipo {
		case "auto":
			if autoNoEnvio[c.Chave] {
				continue
			}
			if strings.TrimSpace(v.Texto[c.Chave]) == "" {
				l = append(l, c.Rotulo+" (não cadastrado)")
			}
		case "fotos":
			if fotos < c.Minimo {
				l = append(l, fmt.Sprintf("%s: mínimo de %d foto(s), há %d", c.Rotulo, c.Minimo, fotos))
			}
		case "checklist":
			resp := v.Checklists[c.Chave]
			n := 0
			for _, it := range c.Itens {
				if resp[it].V != "" {
					n++
				}
			}
			if n < len(c.Itens) {
				l = append(l, fmt.Sprintf("%s: %d de %d itens marcados", c.Rotulo, n, len(c.Itens)))
			}
		default:
			if strings.TrimSpace(v.Texto[c.Chave]) == "" {
				l = append(l, c.Rotulo)
			}
		}
	}
	return l
}

// LerValoresForm lê do formulário os campos de um grupo (quem). Campos de checklist vêm como
// "cl_<chave>_<n>" (coluna) e "clobs_<chave>_<n>" (observação), n = índice do item.
func LerValoresForm(campos []CampoTermo, quem string, v *ValoresTermo, get func(string) string) error {
	for _, c := range campos {
		if c.Quem != quem {
			continue
		}
		switch c.Tipo {
		case "auto", "fotos":
		case "checklist":
			m := map[string]ValorChecklist{}
			cols := c.ColunasChecklist()
			for i, it := range c.Itens {
				col := strings.TrimSpace(get(fmt.Sprintf("cl_%s_%d", c.Chave, i)))
				if col != "" && !temValor(cols, col) {
					return fmt.Errorf("%s: resposta inválida em %q", c.Rotulo, it)
				}
				obs := limitarCampo(get(fmt.Sprintf("clobs_%s_%d", c.Chave, i)), 200)
				if col != "" || obs != "" {
					m[it] = ValorChecklist{V: col, Obs: obs}
				}
			}
			v.Checklists[c.Chave] = m
		case "opcao":
			x := strings.TrimSpace(get("c_" + c.Chave))
			if x != "" && !temValor(c.Opcoes, x) {
				return fmt.Errorf("%s: opção inválida", c.Rotulo)
			}
			v.Texto[c.Chave] = x
		case "textolongo":
			v.Texto[c.Chave] = limitarCampo(get("c_"+c.Chave), 2000)
		case "datahora":
			x := strings.TrimSpace(get("c_" + c.Chave))
			if x != "" && !reDataHora.MatchString(x) {
				return fmt.Errorf("%s: data e hora inválidas", c.Rotulo)
			}
			v.Texto[c.Chave] = x
		default:
			v.Texto[c.Chave] = limitarCampo(strings.Join(strings.Fields(get("c_"+c.Chave)), " "), 300)
		}
	}
	return nil
}

var reDataHora = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2})?$`)

func limitarCampo(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// textoDoValor: como o valor aparece no texto corrido.
func textoDoValor(c *CampoTermo, v ValoresTermo) string {
	if c == nil {
		return ""
	}
	switch c.Tipo {
	case "checklist":
		var partes []string
		for _, it := range c.Itens {
			if r, ok := v.Checklists[c.Chave][it]; ok && r.V != "" {
				partes = append(partes, it+": "+r.V)
			}
		}
		return strings.Join(partes, "; ")
	case "datahora":
		return formatarDataHoraCampo(v.Texto[c.Chave])
	}
	return v.Texto[c.Chave]
}

func formatarDataHoraCampo(s string) string {
	if len(s) >= 10 && s[4] == '-' {
		d := s[8:10] + "/" + s[5:7] + "/" + s[0:4]
		if len(s) >= 16 {
			d += " " + s[11:16]
		}
		return d
	}
	return s
}
