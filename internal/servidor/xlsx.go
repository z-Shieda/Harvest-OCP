package servidor

import (
	"archive/zip"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Gerador mínimo de arquivos .xlsx (Office Open XML), sem dependências externas.

type Celula = any // string, int, int64, float64

type Planilha struct {
	Nome      string
	Cabecalho []string
	Larguras  []float64
	Linhas    [][]Celula
}

func colunaLetra(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

func xmlTexto(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError || (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || (r >= 0xD800 && r <= 0xDFFF) || r == 0xFFFE || r == 0xFFFF {
			continue
		}
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func nomeAba(s string) string {
	r := strings.NewReplacer("\\", "", "/", "", "?", "", "*", "", "[", "", "]", "", ":", "")
	s = r.Replace(s)
	if utf8.RuneCountInString(s) > 31 {
		s = string([]rune(s)[:31])
	}
	return s
}

func EscreverXLSX(w io.Writer, abas []Planilha) error {
	z := zip.NewWriter(w)
	arq := func(nome, conteudo string) error {
		f, err := z.Create(nome)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, conteudo)
		return err
	}

	var ct strings.Builder
	ct.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)
	for i := range abas {
		fmt.Fprintf(&ct, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1)
	}
	ct.WriteString(`</Types>`)
	if err := arq("[Content_Types].xml", ct.String()); err != nil {
		return err
	}
	if err := arq("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`); err != nil {
		return err
	}

	var wb, rels strings.Builder
	wb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i, a := range abas {
		fmt.Fprintf(&wb, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlTexto(nomeAba(a.Nome)), i+1, i+1)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1)
	}
	wb.WriteString(`</sheets>`)
	var nomes strings.Builder
	for i, a := range abas {
		if len(a.Cabecalho) > 0 && len(a.Linhas) > 0 {
			fmt.Fprintf(&nomes, `<definedName name="_xlnm._FilterDatabase" localSheetId="%d" hidden="1">'%s'!$A$1:$%s$%d</definedName>`,
				i, xmlTexto(strings.ReplaceAll(nomeAba(a.Nome), "'", "''")), colunaLetra(len(a.Cabecalho)-1), len(a.Linhas)+1)
		}
	}
	if nomes.Len() > 0 {
		wb.WriteString(`<definedNames>` + nomes.String() + `</definedNames>`)
	}
	wb.WriteString(`</workbook>`)
	fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`, len(abas)+1)
	if err := arq("xl/workbook.xml", wb.String()); err != nil {
		return err
	}
	if err := arq("xl/_rels/workbook.xml.rels", rels.String()); err != nil {
		return err
	}
	// estilo 0 = normal, 1 = cabeçalho em negrito com fundo, 2 = número com 2 casas
	if err := arq("xl/styles.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`+
		`<numFmts count="1"><numFmt numFmtId="164" formatCode="0.00"/></numFmts>`+
		`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><sz val="11"/><color rgb="FFFFFFFF"/><name val="Calibri"/></font></fonts>`+
		`<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF1F4E79"/><bgColor indexed="64"/></patternFill></fill></fills>`+
		`<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>`+
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>`+
		`<cellXfs count="3"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1"/>`+
		`<xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/></cellXfs>`+
		`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`); err != nil {
		return err
	}

	for i, a := range abas {
		f, err := z.Create(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1))
		if err != nil {
			return err
		}
		var s strings.Builder
		s.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
		s.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`)
		if len(a.Cabecalho) > 0 {
			s.WriteString(`<cols>`)
			for c := range a.Cabecalho {
				w := 16.0
				if c < len(a.Larguras) && a.Larguras[c] > 0 {
					w = a.Larguras[c]
				}
				fmt.Fprintf(&s, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, c+1, c+1, w)
			}
			s.WriteString(`</cols>`)
		}
		s.WriteString(`<sheetData>`)
		linha := 1
		escreverLinha := func(cels []Celula, estilo int) {
			fmt.Fprintf(&s, `<row r="%d">`, linha)
			for c, v := range cels {
				ref := fmt.Sprintf("%s%d", colunaLetra(c), linha)
				switch x := v.(type) {
				case int:
					fmt.Fprintf(&s, `<c r="%s"><v>%d</v></c>`, ref, x)
				case int64:
					fmt.Fprintf(&s, `<c r="%s"><v>%d</v></c>`, ref, x)
				case float64:
					fmt.Fprintf(&s, `<c r="%s" s="2"><v>%.4f</v></c>`, ref, x)
				default:
					txt := fmt.Sprint(v)
					if txt == "" {
						continue
					}
					st := ""
					if estilo > 0 {
						st = fmt.Sprintf(` s="%d"`, estilo)
					}
					fmt.Fprintf(&s, `<c r="%s" t="inlineStr"%s><is><t xml:space="preserve">%s</t></is></c>`, ref, st, xmlTexto(txt))
				}
			}
			s.WriteString(`</row>`)
			linha++
		}
		if len(a.Cabecalho) > 0 {
			cab := make([]Celula, len(a.Cabecalho))
			for c, h := range a.Cabecalho {
				cab[c] = h
			}
			escreverLinha(cab, 1)
		}
		for _, l := range a.Linhas {
			escreverLinha(l, 0)
			if s.Len() > 1<<20 {
				if _, err := io.WriteString(f, s.String()); err != nil {
					return err
				}
				s.Reset()
			}
		}
		s.WriteString(`</sheetData>`)
		if len(a.Cabecalho) > 0 && len(a.Linhas) > 0 {
			fmt.Fprintf(&s, `<autoFilter ref="A1:%s%d"/>`, colunaLetra(len(a.Cabecalho)-1), linha-1)
		}
		s.WriteString(`</worksheet>`)
		if _, err := io.WriteString(f, s.String()); err != nil {
			return err
		}
	}
	return z.Close()
}
