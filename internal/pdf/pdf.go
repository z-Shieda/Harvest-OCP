// Package pdf gera documentos PDF simples em Go puro, sem dependência externa (Harvest 1.23,
// termos de responsabilidade). Usa as fontes padrão do PDF (Helvetica, negrito e itálico) em
// WinAnsiEncoding — acentuação do português completa, sem embutir fonte —, com quebra de linha
// pelas métricas oficiais, texto justificado, faixas de título, tabelas com repetição do
// cabeçalho ao mudar de página, caixas de marcar, imagens (PNG/JPEG/GIF) e rodapé numerado.
//
// Coordenadas: a API trabalha "de cima para baixo" (y cresce para baixo, a partir da margem);
// a conversão para o sistema do PDF (origem embaixo à esquerda) é feita na escrita.
package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"  // decodificadores registrados para Imagem()
	_ "image/jpeg" //
	_ "image/png"  //
	"math"
	"strings"
	"time"
)

// A4 em pontos (1/72 pol.).
const (
	LarguraA4 = 595.28
	AlturaA4  = 841.89
)

type Alinhamento int

const (
	Esquerda Alinhamento = iota
	Centro
	Direita
	Justificado
)

// Cor RGB de 0 a 255.
type Cor struct{ R, G, B uint8 }

var (
	Preto      = Cor{0, 0, 0}
	Branco     = Cor{255, 255, 255}
	CinzaClaro = Cor{238, 241, 240}
	CinzaBorda = Cor{170, 178, 180}
	CinzaTexto = Cor{90, 105, 110}
)

// CorHex converte "#RRGGBB" (ou "RRGGBB"); inválido = preto.
func CorHex(h string) Cor {
	h = strings.TrimPrefix(strings.TrimSpace(h), "#")
	if len(h) != 6 {
		return Preto
	}
	var c Cor
	if _, err := fmt.Sscanf(h, "%02x%02x%02x", &c.R, &c.G, &c.B); err != nil {
		return Preto
	}
	return c
}

func (c Cor) ops(preench bool) string {
	op := "RG"
	if preench {
		op = "rg"
	}
	return fmt.Sprintf("%.3f %.3f %.3f %s", float64(c.R)/255, float64(c.G)/255, float64(c.B)/255, op)
}

// Trecho de texto com estilo (um parágrafo é uma lista de trechos).
type Trecho struct {
	Texto   string
	Negrito bool
	Italico bool
}

// T é um atalho para um trecho simples.
func T(s string) Trecho { return Trecho{Texto: s} }

// N é um atalho para um trecho em negrito.
func N(s string) Trecho { return Trecho{Texto: s, Negrito: true} }

// Doc é um documento em construção.
type Doc struct {
	Largura, Altura         float64
	MargemEsq, MargemDir    float64
	MargemTopo, MargemBaixo float64
	Titulo, Autor, Assunto  string
	Rodape                  func(pagina, total int) string // texto do rodapé ("" = sem)
	CorRodape               Cor
	paginas                 []*bytes.Buffer
	pag                     *bytes.Buffer
	y                       float64 // posição atual a partir do topo da página
	imagens                 []*imagemPDF
	aoNovaPagina            func(d *Doc)
	criado                  time.Time
}

type imagemPDF struct {
	nome         string
	largura, alt int
	rgb, alfa    []byte // comprimidos (zlib)
}

// Novo cria um documento A4 retrato com margens de 2 cm (topo/base 2,2 cm).
func Novo() *Doc {
	d := &Doc{Largura: LarguraA4, Altura: AlturaA4, MargemEsq: 56, MargemDir: 56, MargemTopo: 56, MargemBaixo: 62,
		CorRodape: CinzaTexto, criado: time.Now()}
	d.NovaPagina()
	return d
}

// AoNovaPagina registra uma função chamada no início de cada página nova (ex.: cabeçalho).
// Ela é chamada também para a página atual, se ainda estiver vazia.
func (d *Doc) AoNovaPagina(f func(d *Doc)) {
	d.aoNovaPagina = f
	if d.pag != nil && d.pag.Len() == 0 && f != nil {
		f(d)
	}
}

// LarguraUtil é a largura entre as margens.
func (d *Doc) LarguraUtil() float64 { return d.Largura - d.MargemEsq - d.MargemDir }

// Y devolve a posição vertical atual (a partir do topo).
func (d *Doc) Y() float64 { return d.y }

// Restante devolve o espaço vertical livre na página atual.
func (d *Doc) Restante() float64 { return d.Altura - d.MargemBaixo - d.y }

// NovaPagina começa uma página.
func (d *Doc) NovaPagina() {
	d.pag = &bytes.Buffer{}
	d.paginas = append(d.paginas, d.pag)
	d.y = d.MargemTopo
	if d.aoNovaPagina != nil {
		d.aoNovaPagina(d)
	}
}

// Garantir muda de página se não houver h pontos livres.
func (d *Doc) Garantir(h float64) {
	if h > d.Restante() && d.y > d.MargemTopo+1 {
		d.NovaPagina()
	}
}

// Espaco avança h pontos.
func (d *Doc) Espaco(h float64) {
	d.y += h
	if d.y > d.Altura-d.MargemBaixo {
		d.NovaPagina()
	}
}

func (d *Doc) py(y float64) float64 { return d.Altura - y }

// ---------------- texto ----------------

// cp1252Alto: caracteres de 0x80 a 0x9F do Windows-1252 (o resto do Latin-1 é igual ao Unicode).
var cp1252Alto = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84, '…': 0x85, '†': 0x86, '‡': 0x87, 'ˆ': 0x88, '‰': 0x89,
	'Š': 0x8A, '‹': 0x8B, 'Œ': 0x8C, 'Ž': 0x8E, '‘': 0x91, '’': 0x92, '“': 0x93, '”': 0x94, '•': 0x95,
	'–': 0x96, '—': 0x97, '˜': 0x98, '™': 0x99, 'š': 0x9A, '›': 0x9B, 'œ': 0x9C, 'ž': 0x9E, 'Ÿ': 0x9F,
}

// codificar converte UTF-8 em cp1252 (caractere sem equivalente vira um substituto próximo ou '?').
func codificar(s string) []byte {
	var out []byte
	for _, r := range s {
		switch r {
		case '\t':
			r = ' '
		case '☐', '□':
			out = append(out, '[', ' ', ']')
			continue
		case '☒', '☑', '✔', '✓':
			out = append(out, '[', 'X', ']')
			continue
		case '≥':
			out = append(out, '>', '=')
			continue
		case '≤':
			out = append(out, '<', '=')
			continue
		case '→':
			out = append(out, '-', '>')
			continue
		case ' ', ' ', ' ':
			r = ' '
		case '‑', '‐':
			r = '-'
		}
		if r < 32 {
			continue
		}
		switch {
		case r < 0x7F || (r >= 0xA0 && r <= 0xFF):
			out = append(out, byte(r))
		case cp1252Alto[r] != 0:
			out = append(out, cp1252Alto[r])
		default:
			out = append(out, '?')
		}
	}
	return out
}

func larguras(negrito bool) *[256]uint16 {
	if negrito {
		return &larguraHelveticaNegrito
	}
	return &larguraHelvetica
}

// LarguraTexto mede o texto (em pontos) no tamanho dado.
func LarguraTexto(s string, tamanho float64, negrito bool) float64 {
	return larguraBytes(codificar(s), tamanho, negrito)
}

func larguraBytes(b []byte, tamanho float64, negrito bool) float64 {
	w := larguras(negrito)
	var soma int
	for _, c := range b {
		soma += int(w[c])
	}
	return float64(soma) * tamanho / 1000
}

func fonte(t Trecho) string {
	switch {
	case t.Negrito:
		return "/F2"
	case t.Italico:
		return "/F3"
	}
	return "/F1"
}

func escaparPDF(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		switch {
		case c == '(' || c == ')' || c == '\\':
			s.WriteByte('\\')
			s.WriteByte(c)
		case c < 32 || c > 126:
			fmt.Fprintf(&s, "\\%03o", c)
		default:
			s.WriteByte(c)
		}
	}
	return s.String()
}

// palavra é um pedaço sem espaço, com o estilo dele; espacoAntes diz se havia espaço antes.
type palavra struct {
	b           []byte
	t           Trecho
	largura     float64
	espacoAntes bool
}

type linha struct {
	palavras []palavra
	largura  float64
	espacos  int
	forcada  bool // terminou por \n (não justificar)
}

// quebrar divide os trechos em linhas de até largura pontos.
func quebrar(trechos []Trecho, tamanho, largura float64) []linha {
	var linhas []linha
	atual := linha{}
	espacoPendente := false
	espW := func(t Trecho) float64 { return float64(larguras(t.Negrito)[' ']) * tamanho / 1000 }
	fechar := func(forcada bool) {
		atual.forcada = forcada
		linhas = append(linhas, atual)
		atual = linha{}
		espacoPendente = false
	}
	adicionar := func(p palavra) {
		extra := 0.0
		if len(atual.palavras) > 0 && p.espacoAntes {
			extra = espW(p.t)
		}
		if len(atual.palavras) > 0 && atual.largura+extra+p.largura > largura {
			fechar(false)
			extra = 0
			p.espacoAntes = false
		}
		if len(atual.palavras) == 0 {
			p.espacoAntes = false
		}
		// palavra maior que a linha inteira: corta por caracteres
		for p.largura > largura && len(p.b) > 1 {
			n := len(p.b)
			for n > 1 && larguraBytes(p.b[:n], tamanho, p.t.Negrito) > largura-atual.largura {
				n--
			}
			pedaco := palavra{b: p.b[:n], t: p.t, largura: larguraBytes(p.b[:n], tamanho, p.t.Negrito)}
			atual.palavras = append(atual.palavras, pedaco)
			atual.largura += pedaco.largura
			fechar(false)
			p.b = p.b[n:]
			p.largura = larguraBytes(p.b, tamanho, p.t.Negrito)
		}
		if extra > 0 {
			atual.espacos++
		}
		atual.palavras = append(atual.palavras, p)
		atual.largura += extra + p.largura
	}
	for _, t := range trechos {
		partes := strings.Split(t.Texto, "\n")
		for i, parte := range partes {
			if i > 0 {
				fechar(true)
			}
			b := codificar(parte)
			inicio := 0
			for j := 0; j <= len(b); j++ {
				if j == len(b) || b[j] == ' ' {
					if j > inicio {
						w := b[inicio:j]
						adicionar(palavra{b: w, t: t, largura: larguraBytes(w, tamanho, t.Negrito), espacoAntes: espacoPendente})
						espacoPendente = false
					}
					if j < len(b) {
						espacoPendente = true
					}
					inicio = j + 1
				}
			}
		}
	}
	if len(atual.palavras) > 0 || len(linhas) == 0 {
		fechar(true)
	}
	return linhas
}

// AlturaTexto calcula a altura que os trechos ocupam numa largura.
func AlturaTexto(trechos []Trecho, tamanho, largura, entrelinha float64) float64 {
	return float64(len(quebrar(trechos, tamanho, largura))) * tamanho * entrelinha
}

// escreverLinhas desenha linhas já quebradas a partir de (x, y do topo).
func (d *Doc) escreverLinhas(ls []linha, x, y, largura, tamanho, entrelinha float64, alinhar Alinhamento, cor Cor) {
	fmt.Fprintf(d.pag, "BT %s\n", cor.ops(true))
	for i, l := range ls {
		base := y + tamanho*0.8 + float64(i)*tamanho*entrelinha
		dx := 0.0
		tw := 0.0
		switch alinhar {
		case Centro:
			dx = (largura - l.largura) / 2
		case Direita:
			dx = largura - l.largura
		case Justificado:
			if !l.forcada && i < len(ls)-1 && l.espacos > 0 {
				tw = (largura - l.largura) / float64(l.espacos)
				if tw > tamanho*0.6 {
					tw = 0 // linha curta demais para justificar sem buraco feio
				}
			}
		}
		fmt.Fprintf(d.pag, "%.3f Tw 1 0 0 1 %.2f %.2f Tm\n", tw, x+dx, d.py(base))
		fonteAtual := ""
		var sb strings.Builder
		descarregar := func() {
			if sb.Len() > 0 {
				fmt.Fprintf(d.pag, "(%s) Tj\n", sb.String())
				sb.Reset()
			}
		}
		for _, p := range l.palavras {
			f := fonte(p.t)
			if f != fonteAtual {
				descarregar()
				fmt.Fprintf(d.pag, "%s %.2f Tf\n", f, tamanho)
				fonteAtual = f
			}
			if p.espacoAntes {
				sb.WriteString(" ")
			}
			sb.WriteString(escaparPDF(p.b))
		}
		descarregar()
	}
	d.pag.WriteString("0 Tw ET\n")
}

// Estilo de parágrafo.
type Estilo struct {
	Tamanho      float64 // padrão 9,5
	Entrelinha   float64 // múltiplo do tamanho; padrão 1,35
	Alinhar      Alinhamento
	Cor          Cor
	Recuo        float64 // recuo à esquerda
	EspacoDepois float64 // padrão 5
}

func (e Estilo) normal() Estilo {
	if e.Tamanho == 0 {
		e.Tamanho = 9.5
	}
	if e.Entrelinha == 0 {
		e.Entrelinha = 1.35
	}
	if e.EspacoDepois == 0 {
		e.EspacoDepois = 5
	}
	return e
}

// Paragrafo escreve texto com quebra automática (muda de página se precisar).
func (d *Doc) Paragrafo(trechos []Trecho, e Estilo) {
	e = e.normal()
	larg := d.LarguraUtil() - e.Recuo
	ls := quebrar(trechos, e.Tamanho, larg)
	alt := e.Tamanho * e.Entrelinha
	// evita deixar uma linha sozinha no pé da página
	if len(ls) > 1 && d.Restante() < 2*alt {
		d.NovaPagina()
	}
	for len(ls) > 0 {
		cabe := int(d.Restante() / alt)
		if cabe <= 0 {
			d.NovaPagina()
			continue
		}
		if cabe > len(ls) {
			cabe = len(ls)
		}
		d.escreverLinhas(ls[:cabe], d.MargemEsq+e.Recuo, d.y, larg, e.Tamanho, e.Entrelinha, e.Alinhar, e.Cor)
		d.y += float64(cabe) * alt
		ls = ls[cabe:]
		if len(ls) > 0 {
			d.NovaPagina()
		}
	}
	d.y += e.EspacoDepois
}

// ItemNumerado escreve "6.1  texto" com o número em negrito e o texto recuado (alinhado).
func (d *Doc) ItemNumerado(numero string, trechos []Trecho, e Estilo) {
	e = e.normal()
	w := math.Max(LarguraTexto(numero+" ", e.Tamanho, true)+2, 26)
	alt := AlturaTexto(trechos, e.Tamanho, d.LarguraUtil()-e.Recuo-w, e.Entrelinha)
	if alt > d.Restante() && alt <= d.Altura-d.MargemTopo-d.MargemBaixo {
		d.NovaPagina()
	}
	if alt <= d.Restante() {
		y0 := d.y
		d.escreverLinhas(quebrar([]Trecho{N(numero)}, e.Tamanho, w), d.MargemEsq+e.Recuo, y0, w, e.Tamanho, e.Entrelinha, Esquerda, e.Cor)
		e2 := e
		e2.Recuo += w
		d.Paragrafo(trechos, e2)
		return
	}
	// não cabe inteiro: escreve corrido (número + texto) deixando o Paragrafo paginar
	d.Paragrafo(append([]Trecho{N(numero + " ")}, trechos...), e)
}

// Marcador escreve um item de lista com "•".
func (d *Doc) Marcador(trechos []Trecho, e Estilo) {
	e = e.normal()
	d.Garantir(e.Tamanho * e.Entrelinha)
	d.escreverLinhas(quebrar([]Trecho{T("•")}, e.Tamanho, 12), d.MargemEsq+e.Recuo, d.y, 12, e.Tamanho, e.Entrelinha, Esquerda, e.Cor)
	e.Recuo += 12
	d.Paragrafo(trechos, e)
}

// Faixa: barra colorida com o título de uma seção.
func (d *Doc) Faixa(texto string, fundo, corTexto Cor, tamanho float64) {
	if tamanho == 0 {
		tamanho = 10
	}
	larg := d.LarguraUtil()
	ls := quebrar([]Trecho{N(texto)}, tamanho, larg-12)
	alt := float64(len(ls))*tamanho*1.3 + 8
	d.Garantir(alt + 40) // não deixa título órfão no pé da página
	d.Retangulo(d.MargemEsq, d.y, larg, alt, &fundo, nil, 0)
	d.escreverLinhas(ls, d.MargemEsq+6, d.y+4, larg-12, tamanho, 1.3, Esquerda, corTexto)
	d.y += alt + 6
}

// Destaque: texto dentro de uma caixa com fundo e borda à esquerda.
func (d *Doc) Destaque(trechos []Trecho, fundo, borda Cor, e Estilo) {
	e = e.normal()
	larg := d.LarguraUtil() - 16
	ls := quebrar(trechos, e.Tamanho, larg)
	alt := float64(len(ls))*e.Tamanho*e.Entrelinha + 10
	if alt > d.Restante() {
		d.NovaPagina()
	}
	if alt > d.Restante() { // maior que uma página: vira parágrafo comum
		d.Paragrafo(trechos, e)
		return
	}
	d.Retangulo(d.MargemEsq, d.y, d.LarguraUtil(), alt, &fundo, nil, 0)
	d.Retangulo(d.MargemEsq, d.y, 3, alt, &borda, nil, 0)
	d.escreverLinhas(ls, d.MargemEsq+10, d.y+5, larg, e.Tamanho, e.Entrelinha, e.Alinhar, e.Cor)
	d.y += alt + e.EspacoDepois
}

// Caixa de marcar seguida de texto (aceites).
func (d *Doc) Caixa(marcada bool, trechos []Trecho, e Estilo) {
	e = e.normal()
	larg := d.LarguraUtil() - e.Recuo - 16
	alt := AlturaTexto(trechos, e.Tamanho, larg, e.Entrelinha)
	d.Garantir(alt)
	d.desenharCaixa(d.MargemEsq+e.Recuo, d.y+e.Tamanho*0.1, e.Tamanho*0.95, marcada)
	e.Recuo += 16
	d.Paragrafo(trechos, e)
}

func (d *Doc) desenharCaixa(x, y, lado float64, marcada bool) {
	fmt.Fprintf(d.pag, "q 0.8 w %s %.2f %.2f %.2f %.2f re S", Preto.ops(false), x, d.py(y+lado), lado, lado)
	if marcada {
		// "X" desenhado (não depende de fonte)
		m := lado * 0.2
		fmt.Fprintf(d.pag, " 1.2 w %.2f %.2f m %.2f %.2f l S %.2f %.2f m %.2f %.2f l S",
			x+m, d.py(y+m), x+lado-m, d.py(y+lado-m), x+m, d.py(y+lado-m), x+lado-m, d.py(y+m))
	}
	d.pag.WriteString(" Q\n")
}

// Retangulo desenha (x, y a partir do topo). fundo/borda nil = sem.
func (d *Doc) Retangulo(x, y, w, h float64, fundo, borda *Cor, espessura float64) {
	if fundo == nil && borda == nil {
		return
	}
	d.pag.WriteString("q ")
	if fundo != nil {
		d.pag.WriteString(fundo.ops(true) + " ")
	}
	if borda != nil {
		fmt.Fprintf(d.pag, "%s %.2f w ", borda.ops(false), math.Max(espessura, 0.3))
	}
	op := "f"
	switch {
	case fundo != nil && borda != nil:
		op = "B"
	case borda != nil:
		op = "S"
	}
	fmt.Fprintf(d.pag, "%.2f %.2f %.2f %.2f re %s Q\n", x, d.py(y+h), w, h, op)
}

// Linha horizontal na posição atual.
func (d *Doc) LinhaHorizontal(cor Cor, espessura float64) {
	fmt.Fprintf(d.pag, "q %s %.2f w %.2f %.2f m %.2f %.2f l S Q\n", cor.ops(false), espessura, d.MargemEsq, d.py(d.y), d.Largura-d.MargemDir, d.py(d.y))
	d.y += 4
}

// TextoEm escreve uma linha de texto numa posição absoluta (sem mover o cursor).
func (d *Doc) TextoEm(x, y float64, trechos []Trecho, tamanho float64, largura float64, alinhar Alinhamento, cor Cor) {
	d.escreverLinhas(quebrar(trechos, tamanho, largura), x, y, largura, tamanho, 1.25, alinhar, cor)
}

// ---------------- tabelas ----------------

// Celula de tabela. Caixa: 0 = texto; 1 = caixa vazia; 2 = caixa marcada (centralizada).
type Celula struct {
	Trechos []Trecho
	Fundo   *Cor
	Caixa   int
	Alinhar Alinhamento
	Mesclar int // ocupa mais colunas (0 ou 1 = uma)
}

// C cria uma célula de texto simples.
func C(s string) Celula { return Celula{Trechos: []Trecho{T(s)}} }

// CN cria uma célula em negrito.
func CN(s string) Celula { return Celula{Trechos: []Trecho{N(s)}} }

// Tabela: Colunas = larguras relativas; as primeiras Cabecalho linhas repetem a cada página.
type Tabela struct {
	Colunas   []float64
	Linhas    [][]Celula
	Cabecalho int
	Tamanho   float64 // padrão 8,5
	FundoCab  *Cor
	CorBorda  Cor
}

func (d *Doc) Tabela(t Tabela) {
	if len(t.Colunas) == 0 || len(t.Linhas) == 0 {
		return
	}
	if t.Tamanho == 0 {
		t.Tamanho = 8.5
	}
	if t.CorBorda == (Cor{}) {
		t.CorBorda = CinzaBorda
	}
	soma := 0.0
	for _, c := range t.Colunas {
		soma += c
	}
	larg := d.LarguraUtil()
	ws := make([]float64, len(t.Colunas))
	for i, c := range t.Colunas {
		ws[i] = c / soma * larg
	}
	const pad = 4.0
	const entre = 1.25
	alturaLinha := func(l []Celula) float64 {
		h := t.Tamanho*entre + 2*pad
		col := 0
		for _, c := range l {
			span := max(c.Mesclar, 1)
			w := 0.0
			for k := col; k < col+span && k < len(ws); k++ {
				w += ws[k]
			}
			col += span
			if c.Caixa == 0 {
				if hh := AlturaTexto(c.Trechos, t.Tamanho, w-2*pad, entre) + 2*pad; hh > h {
					h = hh
				}
			}
		}
		return h
	}
	desenhar := func(l []Celula, cab bool) {
		h := alturaLinha(l)
		x := d.MargemEsq
		col := 0
		for _, c := range l {
			span := max(c.Mesclar, 1)
			w := 0.0
			for k := col; k < col+span && k < len(ws); k++ {
				w += ws[k]
			}
			col += span
			fundo := c.Fundo
			if fundo == nil && cab && t.FundoCab != nil {
				fundo = t.FundoCab
			}
			borda := t.CorBorda
			d.Retangulo(x, d.y, w, h, fundo, &borda, 0.5)
			if c.Caixa > 0 {
				lado := t.Tamanho * 0.95
				d.desenharCaixa(x+(w-lado)/2, d.y+(h-lado)/2, lado, c.Caixa == 2)
			} else {
				d.escreverLinhas(quebrar(c.Trechos, t.Tamanho, w-2*pad), x+pad, d.y+pad, w-2*pad, t.Tamanho, entre, c.Alinhar, Preto)
			}
			x += w
		}
		d.y += h
	}
	cab := t.Linhas[:min(t.Cabecalho, len(t.Linhas))]
	altCab := 0.0
	for _, l := range cab {
		altCab += alturaLinha(l)
	}
	for i, l := range t.Linhas {
		h := alturaLinha(l)
		ehCab := i < t.Cabecalho
		if !ehCab && h > d.Restante() {
			d.NovaPagina()
			for _, c := range cab {
				desenhar(c, true)
			}
		} else if ehCab && i == 0 && altCab+h > d.Restante() {
			d.NovaPagina()
		}
		desenhar(l, ehCab)
	}
	d.y += 6
}

// ---------------- imagens ----------------

// Imagem desenha uma imagem (PNG/JPEG/GIF já decodificada) em x, y (topo) com largura w
// (altura proporcional; h > 0 força a altura). Devolve a altura usada.
func (d *Doc) Imagem(img image.Image, x, y, w, h float64) float64 {
	if img == nil {
		return 0
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return 0
	}
	if h <= 0 {
		h = w * float64(b.Dy()) / float64(b.Dx())
	}
	rgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	rgb := make([]byte, 0, b.Dx()*b.Dy()*3)
	alfa := make([]byte, 0, b.Dx()*b.Dy())
	temAlfa := false
	for i := 0; i < len(rgba.Pix); i += 4 {
		rgb = append(rgb, rgba.Pix[i], rgba.Pix[i+1], rgba.Pix[i+2])
		alfa = append(alfa, rgba.Pix[i+3])
		if rgba.Pix[i+3] != 255 {
			temAlfa = true
		}
	}
	im := &imagemPDF{nome: fmt.Sprintf("Im%d", len(d.imagens)+1), largura: b.Dx(), alt: b.Dy(), rgb: comprimir(rgb)}
	if temAlfa {
		im.alfa = comprimir(alfa)
	}
	d.imagens = append(d.imagens, im)
	fmt.Fprintf(d.pag, "q %.2f 0 0 %.2f %.2f %.2f cm /%s Do Q\n", w, h, x, d.py(y+h), im.nome)
	return h
}

func comprimir(b []byte) []byte {
	var buf bytes.Buffer
	z, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	_, _ = z.Write(b)
	_ = z.Close()
	return buf.Bytes()
}

// ---------------- saída ----------------

// Paginas devolve quantas páginas o documento tem até agora.
func (d *Doc) Paginas() int { return len(d.paginas) }

func textoInfo(s string) string { return "(" + escaparPDF(codificar(s)) + ")" }

// Bytes monta o arquivo PDF.
func (d *Doc) Bytes() []byte {
	// rodapé (precisa do total de páginas)
	if d.Rodape != nil {
		total := len(d.paginas)
		for i, p := range d.paginas {
			if txt := d.Rodape(i+1, total); txt != "" {
				salva := d.pag
				d.pag = p
				d.TextoEm(d.MargemEsq, d.Altura-d.MargemBaixo+22, []Trecho{T(txt)}, 7.5, d.LarguraUtil(), Centro, d.CorRodape)
				d.pag = salva
			}
		}
	}
	var out bytes.Buffer
	offsets := []int{0}
	obj := func(conteudo string) int {
		offsets = append(offsets, out.Len())
		n := len(offsets) - 1
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", n, conteudo)
		return n
	}
	objStream := func(dic string, dados []byte) int {
		offsets = append(offsets, out.Len())
		n := len(offsets) - 1
		fmt.Fprintf(&out, "%d 0 obj\n<< %s /Length %d >>\nstream\n", n, dic, len(dados))
		out.Write(dados)
		out.WriteString("\nendstream\nendobj\n")
		return n
	}
	out.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	// números fixos: 1 catálogo, 2 páginas, 3-5 fontes
	nPaginas := len(d.paginas)
	// reserva: catálogo(1) páginas(2) fontes(3,4,5)
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	// placeholder do /Pages: escrito depois (offset corrigido)
	paginasPos := len(offsets)
	offsets = append(offsets, 0)
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Oblique /Encoding /WinAnsiEncoding >>")
	var xobjs strings.Builder
	for _, im := range d.imagens {
		smask := ""
		if im.alfa != nil {
			s := objStream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode", im.largura, im.alt), im.alfa)
			smask = fmt.Sprintf(" /SMask %d 0 R", s)
		}
		n := objStream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode%s", im.largura, im.alt, smask), im.rgb)
		fmt.Fprintf(&xobjs, "/%s %d 0 R ", im.nome, n)
	}
	recursos := "<< /Font << /F1 3 0 R /F2 4 0 R /F3 5 0 R >>"
	if xobjs.Len() > 0 {
		recursos += " /XObject << " + xobjs.String() + ">>"
	}
	recursos += " >>"
	var kids []string
	for _, p := range d.paginas {
		c := objStream("/Filter /FlateDecode", comprimir(p.Bytes()))
		pg := obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Contents %d 0 R /Resources %s >>", d.Largura, d.Altura, c, recursos))
		kids = append(kids, fmt.Sprintf("%d 0 R", pg))
	}
	offsets[paginasPos] = out.Len()
	fmt.Fprintf(&out, "%d 0 obj\n<< /Type /Pages /Kids [%s] /Count %d >>\nendobj\n", paginasPos, strings.Join(kids, " "), nPaginas)
	z := d.criado.Format("-0700") // ex.: -0300 -> -03'00'
	data := d.criado.Format("20060102150405") + z[:3] + "'" + z[3:] + "'"
	info := obj(fmt.Sprintf("<< /Title %s /Author %s /Subject %s /Producer (Harvest) /CreationDate (D:%s) >>",
		textoInfo(d.Titulo), textoInfo(d.Autor), textoInfo(d.Assunto), data))
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, o := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), info, xref)
	return out.Bytes()
}
