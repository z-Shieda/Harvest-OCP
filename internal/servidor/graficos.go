package servidor

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

// Gera gráficos simples em SVG, embutidos direto na página (sem bibliotecas externas).
// Usa as cores do tema via variáveis CSS para funcionar no claro e no escuro.

type Fatia struct {
	Rotulo string
	Valor  int
	Cor    string // cor fixa (dados), ex.: var(--verde)
}

func esc(s string) string { return template.HTMLEscapeString(s) }

// Rosca (donut) com legenda ao lado. Bom para proporções (ex.: status das máquinas).
func grafRosca(fatias []Fatia) template.HTML {
	total := 0
	for _, f := range fatias {
		total += f.Valor
	}
	var b strings.Builder
	b.WriteString(`<div style="display:flex;align-items:center;gap:20px;flex-wrap:wrap">`)
	const r, cx, cy = 52.0, 70.0, 70.0
	circ := 2 * math.Pi * r
	b.WriteString(fmt.Sprintf(`<svg width="140" height="140" viewBox="0 0 140 140" role="img" aria-label="Gráfico de rosca">`))
	b.WriteString(fmt.Sprintf(`<circle cx="%g" cy="%g" r="%g" fill="none" stroke="var(--borda)" stroke-width="18"/>`, cx, cy, r))
	if total > 0 {
		offset := 0.0
		for _, f := range fatias {
			if f.Valor == 0 {
				continue
			}
			seg := circ * float64(f.Valor) / float64(total)
			b.WriteString(fmt.Sprintf(`<circle cx="%g" cy="%g" r="%g" fill="none" stroke="%s" stroke-width="18" stroke-dasharray="%g %g" stroke-dashoffset="%g" transform="rotate(-90 %g %g)"/>`,
				cx, cy, r, f.Cor, seg, circ-seg, -offset, cx, cy))
			offset += seg
		}
	}
	b.WriteString(fmt.Sprintf(`<text x="%g" y="%g" text-anchor="middle" font-size="30" font-weight="700" fill="var(--texto)">%d</text>`, cx, cy+4, total))
	b.WriteString(fmt.Sprintf(`<text x="%g" y="%g" text-anchor="middle" font-size="11" fill="var(--sub)">total</text>`, cx, cy+22))
	b.WriteString(`</svg>`)
	// legenda
	b.WriteString(`<div style="display:flex;flex-direction:column;gap:6px">`)
	for _, f := range fatias {
		pct := 0
		if total > 0 {
			pct = f.Valor * 100 / total
		}
		b.WriteString(fmt.Sprintf(`<div style="display:flex;align-items:center;gap:8px;font-size:14px"><span style="width:12px;height:12px;border-radius:3px;background:%s;display:inline-block"></span><b>%d</b> <span style="color:var(--sub)">%s (%d%%)</span></div>`,
			f.Cor, f.Valor, esc(f.Rotulo), pct))
	}
	b.WriteString(`</div></div>`)
	return template.HTML(b.String())
}

// Barras horizontais com rótulo e valor. Bom para rankings (SO, setor).
func grafBarras(fatias []Fatia, cor string) template.HTML {
	max := 0
	for _, f := range fatias {
		if f.Valor > max {
			max = f.Valor
		}
	}
	if max == 0 {
		max = 1
	}
	var b strings.Builder
	b.WriteString(`<div style="display:flex;flex-direction:column;gap:10px">`)
	for _, f := range fatias {
		larg := f.Valor * 100 / max
		c := cor
		if f.Cor != "" {
			c = f.Cor
		}
		b.WriteString(`<div>`)
		b.WriteString(fmt.Sprintf(`<div style="display:flex;justify-content:space-between;font-size:13px;margin-bottom:2px"><span>%s</span><b>%d</b></div>`, esc(f.Rotulo), f.Valor))
		b.WriteString(fmt.Sprintf(`<div style="height:10px;background:var(--linha);border:1px solid var(--borda);border-radius:5px;overflow:hidden"><div style="height:100%%;width:%d%%;background:%s"></div></div>`, larg, c))
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}
