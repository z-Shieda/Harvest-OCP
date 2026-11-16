package pdf

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestCodificar(t *testing.T) {
	b := codificar("Ação ÇÉ º § “x” — €☐☒")
	esperado := []byte{'A', 0xE7, 0xE3, 'o', ' ', 0xC7, 0xC9, ' ', 0xBA, ' ', 0xA7, ' ', 0x93, 'x', 0x94, ' ', 0x97, ' ', 0x80, '[', ' ', ']', '[', 'X', ']'}
	if !bytes.Equal(b, esperado) {
		t.Fatalf("codificar: % x", b)
	}
	if LarguraTexto("A", 10, false) != 6.67 {
		t.Fatalf("largura de A: %v", LarguraTexto("A", 10, false))
	}
}

func TestQuebra(t *testing.T) {
	ls := quebrar([]Trecho{T("um dois três quatro cinco seis sete oito nove dez")}, 10, 80)
	if len(ls) < 3 {
		t.Fatalf("deveria quebrar em várias linhas: %d", len(ls))
	}
	for _, l := range ls {
		if l.largura > 80.01 {
			t.Fatalf("linha passou da largura: %.2f", l.largura)
		}
	}
	// palavra enorme é cortada
	ls = quebrar([]Trecho{T(strings.Repeat("x", 200))}, 10, 50)
	for _, l := range ls {
		if l.largura > 50.01 {
			t.Fatalf("palavra longa não cortada: %.2f", l.largura)
		}
	}
	// quebra forçada por \n
	if ls := quebrar([]Trecho{T("a\nb")}, 10, 500); len(ls) != 2 || !ls[0].forcada {
		t.Fatal("\\n deveria quebrar")
	}
}

func documentoExemplo() []byte {
	d := Novo()
	d.Titulo = "Termo de teste — ação"
	d.Rodape = func(p, total int) string { return "Página " + strconv.Itoa(p) + " de " + strconv.Itoa(total) + " · código ABCD-1234" }
	img := image.NewNRGBA(image.Rect(0, 0, 20, 10))
	for x := 0; x < 20; x++ {
		for y := 0; y < 10; y++ {
			img.Set(x, y, color.NRGBA{173, 218, 35, uint8(x * 12)})
		}
	}
	d.Imagem(img, d.MargemEsq, d.Y(), 60, 0)
	d.Espaco(34)
	d.Paragrafo([]Trecho{N("TERMO DE ENTREGA, RESPONSABILIDADE E USO DE EQUIPAMENTO CORPORATIVO")}, Estilo{Tamanho: 13, Alinhar: Centro})
	d.Faixa("1. IDENTIFICAÇÃO DO COLABORADOR", CorHex("#18424C"), Branco, 0)
	d.Tabela(Tabela{Colunas: []float64{1, 2, 1, 2}, Linhas: [][]Celula{
		{CN("Nome completo"), C("José da Conceição Ávila"), CN("CPF"), C("529.982.247-25")},
		{CN("Cargo / Função"), C("Analista de Segurança da Informação Sênior com nome de cargo bem comprido"), CN("Setor"), C("T.I")},
	}})
	linhas := [][]Celula{{CN("Item"), CN("Entregue"), CN("Não"), CN("Nº de série / observação")}}
	for i := 0; i < 40; i++ {
		linhas = append(linhas, []Celula{C("Acessório " + strconv.Itoa(i)), {Caixa: 2}, {Caixa: 1}, C("")})
	}
	d.Tabela(Tabela{Colunas: []float64{3, 1, 1, 3}, Linhas: linhas, Cabecalho: 1, FundoCab: &CinzaClaro})
	for i := 0; i < 30; i++ {
		d.ItemNumerado("6."+strconv.Itoa(i+1), []Trecho{T("Utilizar o equipamento "), N("exclusivamente para as atividades profissionais"), T(" do GRUPO OCUPACIONAL, conforme a Política de Segurança da Informação (PSI) vigente, zelando pela guarda.")}, Estilo{Alinhar: Justificado})
	}
	d.Destaque([]Trecho{T("Documento assinado eletronicamente (art. 10, § 2º, da MP nº 2.200-2/2001).")}, CinzaClaro, CorHex("#ADDA23"), Estilo{})
	d.Caixa(true, []Trecho{N("Li e aceito o Termo.")}, Estilo{})
	d.Caixa(false, []Trecho{T("Não marcado (parênteses e \\ barra)")}, Estilo{})
	return d.Bytes()
}

func TestPDFValido(t *testing.T) {
	b := documentoExemplo()
	if !bytes.HasPrefix(b, []byte("%PDF-1.4")) || !bytes.HasSuffix(b, []byte("%%EOF\n")) {
		t.Fatal("cabeçalho/fim inválidos")
	}
	// confere a tabela xref: cada offset aponta para "n 0 obj"
	m := regexp.MustCompile(`startxref\n(\d+)`).FindSubmatch(b)
	if m == nil {
		t.Fatal("sem startxref")
	}
	pos, _ := strconv.Atoi(string(m[1]))
	if !bytes.HasPrefix(b[pos:], []byte("xref\n")) {
		t.Fatal("startxref não aponta para a xref")
	}
	linhas := strings.Split(string(b[pos:]), "\n")
	n, _ := strconv.Atoi(strings.Fields(linhas[1])[1])
	for i := 1; i < n; i++ {
		off, _ := strconv.Atoi(strings.Fields(linhas[2+i])[0])
		if !bytes.HasPrefix(b[off:], []byte(strconv.Itoa(i)+" 0 obj")) {
			t.Fatalf("offset do objeto %d errado", i)
		}
	}
	if !bytes.Contains(b, []byte("/Count ")) || bytes.Contains(b, []byte("/Count 1 ")) {
		t.Fatal("o exemplo deveria ter várias páginas")
	}
	if os.Getenv("PDF_SAIDA") != "" {
		_ = os.WriteFile(os.Getenv("PDF_SAIDA"), b, 0o644)
	}
}
