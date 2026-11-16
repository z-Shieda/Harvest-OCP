package servidor

// Montagem do termo (1.23): marcação + campos + valores → blocos resolvidos, que viram
// HTML (leitura na tela), texto puro (base do hash SHA-256) e PDF (internal/pdf).

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"image"
	"regexp"
	"strings"

	"inventario/internal/pdf"
)

type blocoTermo struct {
	tipo      string // titulo | secao | subtitulo | par | item | marcador | destaque | tabela | campo | quebra
	texto     string
	numero    string
	linhas    [][]string
	cabecalho int
	cond      string // "devolucao" = só com a devolução iniciada
}

var (
	reItemNum  = regexp.MustCompile(`^(\d+(?:\.\d+)+)\.?\s+(.+)$`)
	reCampoSo  = regexp.MustCompile(`^\{([a-z][a-z0-9_]*)\}$`)
	reSepTab   = regexp.MustCompile(`^\|[\s\-:|]+\|$`)
	reCondIni  = regexp.MustCompile(`^\[\[([a-z]+)\]\]$`)
	reCondFim  = regexp.MustCompile(`^\[\[/([a-z]+)\]\]$`)
)

// lerMarcacao transforma o texto do termo em blocos.
func lerMarcacao(corpo string) []blocoTermo {
	var bs []blocoTermo
	cond := ""
	var par []string
	var tab [][]string
	cab := 0
	fecharPar := func() {
		if len(par) > 0 {
			bs = append(bs, blocoTermo{tipo: "par", texto: strings.Join(par, " "), cond: cond})
			par = nil
		}
	}
	fecharTab := func() {
		if len(tab) > 0 {
			bs = append(bs, blocoTermo{tipo: "tabela", linhas: tab, cabecalho: cab, cond: cond})
			tab, cab = nil, 0
		}
	}
	for _, l := range strings.Split(strings.ReplaceAll(corpo, "\r", ""), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "|") {
			fecharPar()
			if reSepTab.MatchString(l) {
				cab = len(tab)
				continue
			}
			cels := strings.Split(strings.Trim(l, "|"), "|")
			for i := range cels {
				cels[i] = strings.TrimSpace(cels[i])
			}
			tab = append(tab, cels)
			continue
		}
		fecharTab()
		switch {
		case l == "":
			fecharPar()
		case reCondIni.MatchString(l):
			fecharPar()
			cond = reCondIni.FindStringSubmatch(l)[1]
		case reCondFim.MatchString(l):
			fecharPar()
			cond = ""
		case l == "===":
			fecharPar()
			bs = append(bs, blocoTermo{tipo: "quebra", cond: cond})
		case strings.HasPrefix(l, "### "):
			fecharPar()
			bs = append(bs, blocoTermo{tipo: "subtitulo", texto: strings.TrimSpace(l[4:]), cond: cond})
		case strings.HasPrefix(l, "## "):
			fecharPar()
			bs = append(bs, blocoTermo{tipo: "secao", texto: strings.TrimSpace(l[3:]), cond: cond})
		case strings.HasPrefix(l, "# "):
			fecharPar()
			bs = append(bs, blocoTermo{tipo: "titulo", texto: strings.TrimSpace(l[2:]), cond: cond})
		case strings.HasPrefix(l, "- "):
			fecharPar()
			bs = append(bs, blocoTermo{tipo: "marcador", texto: strings.TrimSpace(l[2:]), cond: cond})
		case strings.HasPrefix(l, "> "):
			fecharPar()
			bs = append(bs, blocoTermo{tipo: "destaque", texto: strings.TrimSpace(l[2:]), cond: cond})
		case reCampoSo.MatchString(l):
			fecharPar()
			bs = append(bs, blocoTermo{tipo: "campo", texto: reCampoSo.FindStringSubmatch(l)[1], cond: cond})
		case reItemNum.MatchString(l):
			fecharPar()
			m := reItemNum.FindStringSubmatch(l)
			bs = append(bs, blocoTermo{tipo: "item", numero: m[1], texto: m[2], cond: cond})
		default:
			par = append(par, l)
		}
	}
	fecharTab()
	fecharPar()
	return bs
}

// ---------------- contexto de montagem ----------------

// Evidencias do aceite (quadro "Registro de evidências do aceite").
type Evidencias struct {
	AceitoPor      string
	Autenticacao   string
	DataHora       string
	IP             string
	Dispositivo    string
	Maquina        string // "OCP-123 (confirmada)"
	Codigo         string
	Hash           string
	EmailCopia     string
	EntreguePor    string
	ValidacaoTI    string
	AlertaMaquina  string
}

type montagem struct {
	campos     []CampoTermo
	porChave   map[string]*CampoTermo
	valores    ValoresTermo
	aceites    []AceiteTermo
	marcados   []bool // aceites marcados (assinado)
	evid       *Evidencias
	devolucao  bool // mostra os blocos [[devolucao]]
	previa     bool // valores em branco aparecem como [Rótulo]
	docs       map[string]string // chave do documento -> título
}

func novaMontagem(camposDef, aceitesDef string, v ValoresTermo) *montagem {
	m := &montagem{porChave: map[string]*CampoTermo{}, valores: v}
	m.campos, _ = LerCampos(camposDef)
	m.aceites, _ = LerAceites(aceitesDef)
	for i := range m.campos {
		m.porChave[m.campos[i].Chave] = &m.campos[i]
	}
	if m.valores.Texto == nil {
		m.valores = novosValores()
	}
	return m
}

func (m *montagem) rotulo(k string) string {
	if c := m.porChave[k]; c != nil {
		return c.Rotulo
	}
	if r, ok := camposAuto[k]; ok {
		return r
	}
	return k
}

func (m *montagem) valor(k string) string {
	if c := m.porChave[k]; c != nil {
		return textoDoValor(c, m.valores)
	}
	return m.valores.Texto[k]
}

// trechos resolve {campo} e **negrito** de uma linha de texto. Campo vazio vira "[Rótulo]"
// em itálico (na prévia e enquanto alguém ainda vai preencher) ou "—" no termo final.
func (m *montagem) trechos(s string) []pdf.Trecho {
	var out []pdf.Trecho
	negrito := false
	for i, parte := range strings.Split(s, "**") {
		if i > 0 {
			negrito = !negrito
		}
		pos := 0
		for _, ix := range reCampoTexto.FindAllStringSubmatchIndex(parte, -1) {
			if ix[0] > pos {
				out = append(out, pdf.Trecho{Texto: parte[pos:ix[0]], Negrito: negrito})
			}
			k := parte[ix[2]:ix[3]]
			v := strings.ReplaceAll(m.valor(k), "**", "")
			if strings.TrimSpace(v) == "" {
				if m.previa || m.evid == nil {
					out = append(out, pdf.Trecho{Texto: "[" + m.rotulo(k) + "]", Italico: true})
				} else {
					out = append(out, pdf.Trecho{Texto: "—", Negrito: negrito})
				}
			} else {
				out = append(out, pdf.Trecho{Texto: v, Negrito: negrito})
			}
			pos = ix[1]
		}
		if pos < len(parte) {
			out = append(out, pdf.Trecho{Texto: parte[pos:], Negrito: negrito})
		}
	}
	return out
}

// ---------------- blocos resolvidos ----------------

type celulaR struct {
	trechos []pdf.Trecho
	caixa   int // 0 texto, 1 vazia, 2 marcada
	cab     bool
}

type blocoR struct {
	tipo      string // titulo | secao | subtitulo | par | item | marcador | destaque | tabela | caixa | quebra
	trechos   []pdf.Trecho
	numero    string
	linhas    [][]celulaR
	larguras  []float64
	cabecalho int
	marcada   bool
	doc       string // aceite com documento de referência
}

func (m *montagem) resolver(corpo string) []blocoR {
	var out []blocoR
	temEvid := false
	for _, b := range lerMarcacao(corpo) {
		if b.cond == "devolucao" && !m.devolucao {
			continue
		}
		switch b.tipo {
		case "tabela":
			r := blocoR{tipo: "tabela", cabecalho: b.cabecalho}
			ncol := 0
			for _, l := range b.linhas {
				ncol = max(ncol, len(l))
			}
			for i, l := range b.linhas {
				var cels []celulaR
				for j := 0; j < ncol; j++ {
					txt := ""
					if j < len(l) {
						txt = l[j]
					}
					c := celulaR{cab: i < b.cabecalho}
					switch txt {
					case "[ ]", "[]":
						c.caixa = 1
					case "[x]", "[X]":
						c.caixa = 2
					default:
						c.trechos = m.trechos(txt)
					}
					cels = append(cels, c)
				}
				r.linhas = append(r.linhas, cels)
			}
			r.larguras = largurasPadrao(ncol)
			out = append(out, r)
		case "campo":
			switch b.texto {
			case "aceites":
				out = append(out, m.blocosAceites()...)
			case "evidencias":
				temEvid = true
				out = append(out, m.blocosEvidencias()...)
			default:
				out = append(out, m.blocoCampo(b.texto)...)
			}
		default:
			out = append(out, blocoR{tipo: b.tipo, numero: b.numero, trechos: m.trechos(b.texto)})
		}
	}
	if !temEvid && m.evid != nil {
		out = append(out, blocoR{tipo: "secao", trechos: []pdf.Trecho{pdf.N("REGISTRO DE EVIDÊNCIAS DO ACEITE")}})
		out = append(out, m.blocosEvidencias()...)
	}
	return out
}

func largurasPadrao(n int) []float64 {
	switch n {
	case 2:
		return []float64{1, 2}
	case 4:
		return []float64{1.1, 1.6, 1.1, 1.6}
	}
	l := make([]float64, n)
	for i := range l {
		l[i] = 1
	}
	return l
}

func (m *montagem) blocoCampo(k string) []blocoR {
	c := m.porChave[k]
	if c == nil {
		return []blocoR{{tipo: "par", trechos: m.trechos("{" + k + "}")}}
	}
	switch c.Tipo {
	case "checklist":
		cols := c.ColunasChecklist()
		cab := []celulaR{{trechos: []pdf.Trecho{pdf.N(c.CabecalhoItens())}, cab: true}}
		larg := []float64{3}
		for _, col := range cols {
			cab = append(cab, celulaR{trechos: []pdf.Trecho{pdf.N(col)}, cab: true})
			larg = append(larg, 1)
		}
		if c.Obs != "" {
			cab = append(cab, celulaR{trechos: []pdf.Trecho{pdf.N(c.Obs)}, cab: true})
			larg = append(larg, 2.4)
		}
		r := blocoR{tipo: "tabela", cabecalho: 1, linhas: [][]celulaR{cab}, larguras: larg}
		resp := m.valores.Checklists[c.Chave]
		for _, it := range c.Itens {
			l := []celulaR{{trechos: []pdf.Trecho{pdf.T(it)}}}
			for _, col := range cols {
				cx := 1
				if resp[it].V == col {
					cx = 2
				}
				l = append(l, celulaR{caixa: cx})
			}
			if c.Obs != "" {
				l = append(l, celulaR{trechos: []pdf.Trecho{pdf.T(resp[it].Obs)}})
			}
			r.linhas = append(r.linhas, l)
		}
		return []blocoR{r}
	case "fotos":
		n := m.valores.Texto[c.Chave]
		if n == "" {
			n = "0"
		}
		txt := fmt.Sprintf("**%s:** %s foto(s) anexada(s) ao chamado", c.Rotulo, n)
		if ch := m.valores.Texto["chamado_numero"]; ch != "" {
			txt += " nº " + ch
		}
		return []blocoR{{tipo: "par", trechos: m.trechos(txt + ".")}}
	}
	return []blocoR{{tipo: "par", trechos: m.trechos("**" + c.Rotulo + ":** {" + c.Chave + "}")}}
}

func (m *montagem) blocosAceites() []blocoR {
	var out []blocoR
	for i, a := range m.aceites {
		out = append(out, blocoR{tipo: "caixa", marcada: i < len(m.marcados) && m.marcados[i], trechos: []pdf.Trecho{pdf.N(a.Texto)}, doc: a.Doc})
	}
	return out
}

func (m *montagem) blocosEvidencias() []blocoR {
	e := m.evid
	if e == nil {
		e = &Evidencias{}
	}
	val := func(s string) celulaR {
		if s == "" {
			return celulaR{trechos: []pdf.Trecho{{Texto: "[gerado na assinatura]", Italico: true}}}
		}
		return celulaR{trechos: []pdf.Trecho{pdf.T(s)}}
	}
	rot := func(s string) celulaR { return celulaR{trechos: []pdf.Trecho{pdf.N(s)}} }
	r := blocoR{tipo: "tabela", larguras: []float64{1.1, 1.6, 1.1, 1.6}, linhas: [][]celulaR{
		{rot("Aceito por (nome / login)"), val(e.AceitoPor), rot("Autenticação"), val(e.Autenticacao)},
		{rot("Data e hora do aceite"), val(e.DataHora), rot("Endereço IP"), val(e.IP)},
		{rot("Dispositivo / navegador"), val(e.Dispositivo), rot("Assinado na máquina"), val(e.Maquina)},
		{rot("Código de verificação"), val(e.Codigo), rot("E-mail de envio da cópia"), val(e.EmailCopia)},
		{rot("Entregue por (técnico T.I)"), val(e.EntreguePor), rot("Data e hora da validação T.I"), val(e.ValidacaoTI)},
		{rot("Hash SHA-256 do documento"), func() celulaR { c := val(e.Hash); return c }(), rot(""), celulaR{}},
	}}
	// hash ocupa a linha toda
	ult := r.linhas[len(r.linhas)-1]
	r.linhas[len(r.linhas)-1] = []celulaR{ult[0], ult[1]}
	out := []blocoR{r}
	if e.AlertaMaquina != "" {
		out = append(out, blocoR{tipo: "destaque", trechos: []pdf.Trecho{pdf.N("Atenção: "), pdf.T(e.AlertaMaquina)}})
	}
	out = append(out, blocoR{tipo: "par", trechos: []pdf.Trecho{{Texto: "Horários no fuso de Brasília (UTC-3), pelo relógio do servidor. A autenticidade pode ser conferida pelo código de verificação no endereço /verificar do Harvest. Assinatura eletrônica nos termos do art. 10, § 2º, da MP nº 2.200-2/2001 e da Lei nº 14.063/2020.", Italico: true}}})
	return out
}

// ---------------- saídas ----------------

// textoPuro: representação estável do termo (base do hash). Uma linha por bloco.
func textoPuro(bs []blocoR) string {
	var s strings.Builder
	tr := func(ts []pdf.Trecho) string {
		var b strings.Builder
		for _, t := range ts {
			b.WriteString(t.Texto)
		}
		return strings.Join(strings.Fields(b.String()), " ")
	}
	for _, b := range bs {
		switch b.tipo {
		case "tabela":
			for _, l := range b.linhas {
				var cs []string
				for _, c := range l {
					switch c.caixa {
					case 1:
						cs = append(cs, "[ ]")
					case 2:
						cs = append(cs, "[X]")
					default:
						cs = append(cs, tr(c.trechos))
					}
				}
				s.WriteString("| " + strings.Join(cs, " | ") + " |\n")
			}
		case "caixa":
			x := "[ ]"
			if b.marcada {
				x = "[X]"
			}
			s.WriteString(x + " " + tr(b.trechos) + "\n")
		case "quebra":
		case "item":
			s.WriteString(b.numero + " " + tr(b.trechos) + "\n")
		default:
			s.WriteString(tr(b.trechos) + "\n")
		}
	}
	return s.String()
}

func htmlTrechos(ts []pdf.Trecho) string {
	var s strings.Builder
	for _, t := range ts {
		x := html.EscapeString(t.Texto)
		switch {
		case t.Italico && strings.HasPrefix(t.Texto, "["):
			x = `<span class="campo-vazio">` + x + `</span>`
		case t.Italico:
			x = "<i>" + x + "</i>"
		}
		if t.Negrito {
			x = "<b>" + x + "</b>"
		}
		s.WriteString(x)
	}
	return s.String()
}

// paraHTML monta o termo para leitura na tela (classe .termo-doc no CSS).
func paraHTML(bs []blocoR, docs map[string]string) template.HTML {
	var s strings.Builder
	s.WriteString(`<div class="termo-doc">`)
	for _, b := range bs {
		switch b.tipo {
		case "titulo":
			s.WriteString("<h2 class=\"td-titulo\">" + htmlTrechos(b.trechos) + "</h2>")
		case "secao":
			s.WriteString("<h3 class=\"td-secao\">" + htmlTrechos(b.trechos) + "</h3>")
		case "subtitulo":
			s.WriteString("<h4>" + htmlTrechos(b.trechos) + "</h4>")
		case "par":
			s.WriteString("<p>" + htmlTrechos(b.trechos) + "</p>")
		case "item":
			s.WriteString("<p class=\"td-item\"><b>" + html.EscapeString(b.numero) + "</b> " + htmlTrechos(b.trechos) + "</p>")
		case "marcador":
			s.WriteString("<p class=\"td-item\">• " + htmlTrechos(b.trechos) + "</p>")
		case "destaque":
			s.WriteString("<div class=\"td-destaque\">" + htmlTrechos(b.trechos) + "</div>")
		case "quebra":
			s.WriteString("<hr class=\"td-quebra\">")
		case "caixa":
			x := "☐"
			if b.marcada {
				x = "☒"
			}
			s.WriteString("<p class=\"td-caixa\">" + x + " " + htmlTrechos(b.trechos))
			if b.doc != "" {
				if t, ok := docs[b.doc]; ok {
					s.WriteString(` <a href="/documentos/` + html.EscapeString(b.doc) + `" target="_blank" rel="noopener">ler: ` + html.EscapeString(t) + `</a>`)
				}
			}
			s.WriteString("</p>")
		case "tabela":
			s.WriteString(`<div class="rolagem"><table class="td-tabela">`)
			for i, l := range b.linhas {
				s.WriteString("<tr>")
				for _, c := range l {
					tag := "td"
					if i < b.cabecalho {
						tag = "th"
					}
					conteudo := htmlTrechos(c.trechos)
					switch c.caixa {
					case 1:
						conteudo = `<span class="td-cx">☐</span>`
					case 2:
						conteudo = `<span class="td-cx">☒</span>`
					}
					s.WriteString("<" + tag + ">" + conteudo + "</" + tag + ">")
				}
				s.WriteString("</tr>")
			}
			s.WriteString("</table></div>")
		}
	}
	s.WriteString("</div>")
	return template.HTML(s.String())
}

// CabecalhoPDF: dados para o topo/rodapé de cada página.
type CabecalhoPDF struct {
	Logo      image.Image
	NomeMarca string
	CorSec    pdf.Cor
	CorPri    pdf.Cor
	Direita   string // "Termo nº 0042 · TRE-v1.0"
	Rodape    string // "Código de verificação …"
	Titulo    string
	Previa    bool
}

// paraPDF desenha o termo.
func paraPDF(bs []blocoR, c CabecalhoPDF) []byte {
	d := pdf.Novo()
	d.Titulo, d.Autor, d.Assunto = c.Titulo, c.NomeMarca, "Termo de responsabilidade"
	d.AoNovaPagina(func(d *pdf.Doc) {
		y := d.MargemTopo - 30
		if c.Logo != nil {
			d.Imagem(c.Logo, d.MargemEsq, y, 110, 0)
		} else {
			d.TextoEm(d.MargemEsq, y, []pdf.Trecho{pdf.N(c.NomeMarca)}, 12, 200, pdf.Esquerda, c.CorSec)
		}
		d.TextoEm(d.MargemEsq+200, y+3, []pdf.Trecho{pdf.T(c.Direita)}, 8, d.LarguraUtil()-200, pdf.Direita, pdf.CinzaTexto)
		if c.Previa {
			d.TextoEm(d.MargemEsq, y+3, []pdf.Trecho{pdf.N("PRÉVIA — SEM VALOR")}, 9, d.LarguraUtil(), pdf.Centro, pdf.Cor{R: 200, G: 40, B: 40})
		}
		d.Retangulo(d.MargemEsq, d.MargemTopo-8, d.LarguraUtil(), 1.2, &c.CorPri, nil, 0)
	})
	d.Rodape = func(p, total int) string {
		r := fmt.Sprintf("Página %d de %d", p, total)
		if c.Rodape != "" {
			r = c.Rodape + " · " + r
		}
		return r
	}
	fundoCab := pdf.CinzaClaro
	for _, b := range bs {
		switch b.tipo {
		case "titulo":
			d.Paragrafo(negritar(b.trechos), pdf.Estilo{Tamanho: 13, Alinhar: pdf.Centro, Cor: c.CorSec, EspacoDepois: 6})
		case "secao":
			d.Espaco(4)
			var txt strings.Builder
			for _, t := range b.trechos {
				txt.WriteString(t.Texto)
			}
			d.Faixa(txt.String(), c.CorSec, pdf.Branco, 9.5)
		case "subtitulo":
			d.Paragrafo(negritar(b.trechos), pdf.Estilo{Tamanho: 10})
		case "par":
			d.Paragrafo(b.trechos, pdf.Estilo{Alinhar: pdf.Justificado})
		case "item":
			d.ItemNumerado(b.numero, b.trechos, pdf.Estilo{Alinhar: pdf.Justificado})
		case "marcador":
			d.Marcador(b.trechos, pdf.Estilo{Alinhar: pdf.Justificado, Recuo: 6})
		case "destaque":
			d.Destaque(b.trechos, pdf.CinzaClaro, c.CorPri, pdf.Estilo{})
		case "caixa":
			d.Caixa(b.marcada, b.trechos, pdf.Estilo{})
		case "quebra":
			d.NovaPagina()
		case "tabela":
			t := pdf.Tabela{Colunas: b.larguras, Cabecalho: b.cabecalho, FundoCab: &fundoCab}
			for i, l := range b.linhas {
				var cels []pdf.Celula
				for j, cl := range l {
					cel := pdf.Celula{Trechos: cl.trechos, Caixa: cl.caixa}
					if i < b.cabecalho {
						cel.Trechos = negritar(cl.trechos)
					} else if b.cabecalho == 0 && len(l) >= 2 && j%2 == 0 && len(l)%2 == 0 {
						// tabelas de pares "rótulo | valor": rótulo com fundo
						cel.Fundo = &fundoCab
					}
					cels = append(cels, cel)
				}
				// linha mais curta (ex.: hash) ocupa o resto da largura
				if len(cels) < len(b.larguras) && len(cels) > 0 {
					cels[len(cels)-1].Mesclar = len(b.larguras) - len(cels) + 1
				}
				t.Linhas = append(t.Linhas, cels)
			}
			d.Tabela(t)
		}
	}
	return d.Bytes()
}

func negritar(ts []pdf.Trecho) []pdf.Trecho {
	out := make([]pdf.Trecho, len(ts))
	for i, t := range ts {
		t.Negrito = true
		out[i] = t
	}
	return out
}

// logoPDF: a logo da marca para o PDF (PNG/JPEG enviado em Marca, senão a logo da Ocupacional
// já convertida em PNG; na edição genérica sem logo, o nome em texto).
func (s *Servidor) logoPDF() image.Image {
	for _, k := range []string{"logo_escuro", "logo_claro"} {
		var tipo string
		var dados []byte
		if s.b.db.QueryRow(`SELECT tipo, dados FROM marca_arquivos WHERE chave=?`, k).Scan(&tipo, &dados) == nil && !strings.Contains(tipo, "svg") {
			if img, _, err := image.Decode(bytes.NewReader(dados)); err == nil {
				return img
			}
		}
	}
	if s.marca().Ocupacional && !s.marca().TemLogoClaro && !s.marca().TemLogoEscuro {
		if b, err := arquivosWeb.ReadFile("web/static/logo-pdf.png"); err == nil {
			if img, _, err := image.Decode(bytes.NewReader(b)); err == nil {
				return img
			}
		}
	}
	return nil
}

func hexPDF(h string) pdf.Cor { return pdf.CorHex(h) }
