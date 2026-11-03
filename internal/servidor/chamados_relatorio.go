package servidor

import (
	"encoding/csv"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Relatórios de chamados (admin/técnico): volume, tempo de resolução, prazo (SLA),
// distribuição por categoria/setor/prioridade e desempenho por responsável.

type linhaTecnico struct {
	Nome                         string
	Resolvidos, NoPrazo, Abertos int
	TempoMedio                   string
	PctPrazo                     int
	somaHoras                    float64
}

func (s *Servidor) paginaRelatorioChamados(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	dias, _ := strconv.Atoi(r.URL.Query().Get("dias"))
	if dias != 7 && dias != 30 && dias != 90 && dias != 365 {
		dias = 30
	}
	desdeT := time.Now().UTC().AddDate(0, 0, -dias)
	desde := desdeT.Format(time.RFC3339)

	periodo, _ := s.b.Chamados(FiltroChamados{Desde: desde})
	abertosAgora, _ := s.b.Chamados(FiltroChamados{Status: "abertos"})

	var criados, resolvidos []Chamado
	for _, c := range periodo {
		if c.CriadoEm >= desde {
			criados = append(criados, c)
		}
		if c.ResolvidoEm != "" && c.ResolvidoEm >= desde && !c.Aberto() {
			resolvidos = append(resolvidos, c)
		}
	}

	// tempo de resolução e prazo
	var horas []float64
	noPrazo, comPrazo := 0, 0
	tecs := map[string]*linhaTecnico{}
	tec := func(nome string) *linhaTecnico {
		if nome == "" {
			nome = "(sem responsável)"
		}
		if tecs[nome] == nil {
			tecs[nome] = &linhaTecnico{Nome: nome}
		}
		return tecs[nome]
	}
	for _, c := range resolvidos {
		ini, ok1 := parseData(c.CriadoEm)
		fim, ok2 := parseData(c.ResolvidoEm)
		if !ok1 || !ok2 {
			continue
		}
		h := fim.Sub(ini).Hours()
		horas = append(horas, h)
		t := tec(c.AtribuidoNome)
		t.Resolvidos++
		t.somaHoras += h
		switch c.SituacaoPrazo() {
		case "cumprido":
			noPrazo++
			comPrazo++
			t.NoPrazo++
		case "estourado":
			comPrazo++
		}
	}
	atrasados := 0
	for _, c := range abertosAgora {
		tec(c.AtribuidoNome).Abertos++
		if c.SituacaoPrazo() == "atrasado" {
			atrasados++
		}
	}
	var listaTec []*linhaTecnico
	for _, t := range tecs {
		if t.Resolvidos > 0 {
			t.TempoMedio = textoHoras(t.somaHoras / float64(t.Resolvidos))
			t.PctPrazo = t.NoPrazo * 100 / t.Resolvidos
		}
		listaTec = append(listaTec, t)
	}
	sort.Slice(listaTec, func(i, j int) bool {
		if listaTec[i].Resolvidos != listaTec[j].Resolvidos {
			return listaTec[i].Resolvidos > listaTec[j].Resolvidos
		}
		return listaTec[i].Nome < listaTec[j].Nome
	})

	media, mediana := "—", "—"
	if len(horas) > 0 {
		soma := 0.0
		for _, h := range horas {
			soma += h
		}
		media = textoHoras(soma / float64(len(horas)))
		sort.Float64s(horas)
		mediana = textoHoras(horas[len(horas)/2])
	}
	pctPrazo := -1
	if comPrazo > 0 {
		pctPrazo = noPrazo * 100 / comPrazo
	}

	// distribuições (chamados abertos no período)
	contar := func(chave func(Chamado) string) []Fatia {
		m := map[string]int{}
		for _, c := range criados {
			m[chave(c)]++
		}
		var l []Fatia
		for k, v := range m {
			l = append(l, Fatia{Rotulo: k, Valor: v})
		}
		sort.Slice(l, func(i, j int) bool {
			if l[i].Valor != l[j].Valor {
				return l[i].Valor > l[j].Valor
			}
			return l[i].Rotulo < l[j].Rotulo
		})
		if len(l) > 12 {
			outros := 0
			for _, f := range l[11:] {
				outros += f.Valor
			}
			l = append(l[:11], Fatia{Rotulo: "Outros", Valor: outros})
		}
		return l
	}
	porCategoria := contar(func(c Chamado) string { return vazioTraco(c.Categoria) })
	porSetor := contar(func(c Chamado) string {
		if strings.TrimSpace(c.SolicitanteSetor) != "" {
			return c.SolicitanteSetor
		}
		return "(sem setor)"
	})
	porPrioridade := []Fatia{}
	cores := map[string]string{"urgente": "var(--vermelho)", "alta": "var(--amarelo)", "normal": "var(--teal)", "baixa": "#6F8682"}
	for _, p := range []string{"urgente", "alta", "normal", "baixa"} {
		n := 0
		for _, c := range criados {
			if c.Prioridade == p {
				n++
			}
		}
		porPrioridade = append(porPrioridade, Fatia{Rotulo: rotuloPrioridade(p), Valor: n, Cor: cores[p]})
	}
	origemPublica := 0
	for _, c := range criados {
		if c.Publico() {
			origemPublica++
		}
	}

	s.render(w, r, "chamados_relatorio", "Relatórios de chamados", "chamados_relatorio", sess, map[string]any{
		"Dias": dias, "Criados": len(criados), "Resolvidos": len(resolvidos), "EmAberto": len(abertosAgora),
		"Atrasados": atrasados, "Media": media, "Mediana": mediana, "PctPrazo": pctPrazo, "ComPrazo": comPrazo,
		"OrigemPublica": origemPublica,
		"GrafCategoria": grafBarras(porCategoria, "var(--pri)"), "GrafSetor": grafBarras(porSetor, "var(--pri)"),
		"GrafPrioridade": grafBarras(porPrioridade, "var(--pri)"),
		"GrafSemanas":    grafColunasDuplas(seriePeriodo(criados, resolvidos, desdeT, dias)),
		"Tecnicos":       listaTec,
		"SLA":            s.slaAtual(),
	})
}

func textoHoras(h float64) string {
	switch {
	case h < 1:
		return fmt.Sprintf("%d min", int(h*60+0.5))
	case h < 48:
		return fmt.Sprintf("%.1f h", h)
	default:
		return fmt.Sprintf("%.1f dias", h/24)
	}
}

type pontoSerie struct {
	Rotulo string
	A, B   int // abertos, resolvidos
}

// seriePeriodo agrupa por dia (7 dias), semana (30/90) ou mês (365).
func seriePeriodo(criados, resolvidos []Chamado, desde time.Time, dias int) []pontoSerie {
	loc := time.Local
	inicio := desde.In(loc)
	var passo func(time.Time) time.Time
	var rotulo func(time.Time) string
	switch {
	case dias <= 7:
		inicio = time.Date(inicio.Year(), inicio.Month(), inicio.Day(), 0, 0, 0, 0, loc)
		passo = func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }
		rotulo = func(t time.Time) string { return t.Format("02/01") }
	case dias <= 90:
		d := int(inicio.Weekday()+6) % 7 // segunda-feira
		inicio = time.Date(inicio.Year(), inicio.Month(), inicio.Day()-d, 0, 0, 0, 0, loc)
		passo = func(t time.Time) time.Time { return t.AddDate(0, 0, 7) }
		rotulo = func(t time.Time) string { return t.Format("02/01") }
	default:
		inicio = time.Date(inicio.Year(), inicio.Month(), 1, 0, 0, 0, 0, loc)
		passo = func(t time.Time) time.Time { return t.AddDate(0, 1, 0) }
		meses := []string{"jan", "fev", "mar", "abr", "mai", "jun", "jul", "ago", "set", "out", "nov", "dez"}
		rotulo = func(t time.Time) string { return meses[t.Month()-1] + "/" + t.Format("06") }
	}
	var pts []pontoSerie
	var limites []time.Time
	for t := inicio; t.Before(time.Now()); t = passo(t) {
		limites = append(limites, t)
		pts = append(pts, pontoSerie{Rotulo: rotulo(t)})
	}
	idx := func(s string) int {
		t, ok := parseData(s)
		if !ok {
			return -1
		}
		t = t.In(loc)
		for i := len(limites) - 1; i >= 0; i-- {
			if !t.Before(limites[i]) {
				return i
			}
		}
		return -1
	}
	for _, c := range criados {
		if i := idx(c.CriadoEm); i >= 0 {
			pts[i].A++
		}
	}
	for _, c := range resolvidos {
		if i := idx(c.ResolvidoEm); i >= 0 {
			pts[i].B++
		}
	}
	return pts
}

// grafColunasDuplas: colunas lado a lado (abertos x resolvidos) por período, em SVG.
func grafColunasDuplas(pts []pontoSerie) template.HTML {
	max := 1
	for _, p := range pts {
		if p.A > max {
			max = p.A
		}
		if p.B > max {
			max = p.B
		}
	}
	n := len(pts)
	if n == 0 {
		return template.HTML(`<p class="vazio">Sem dados no período.</p>`)
	}
	const alt, base, margem = 160.0, 22.0, 26.0
	larg := 46.0
	if n > 16 {
		larg = 30
	}
	total := margem + float64(n)*larg + 8
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<div style="overflow-x:auto"><svg width="%g" height="%g" viewBox="0 0 %g %g" role="img" aria-label="Chamados abertos e resolvidos por período" style="max-width:100%%;height:auto;min-width:%gpx">`,
		total, alt+base, total, alt+base, float64(n)*22+margem))
	// linhas de grade
	for _, f := range []float64{0, 0.5, 1} {
		y := alt - f*(alt-14)
		b.WriteString(fmt.Sprintf(`<line x1="%g" x2="%g" y1="%g" y2="%g" stroke="var(--borda)" stroke-width="1"/>`, margem, total, y, y))
		b.WriteString(fmt.Sprintf(`<text x="%g" y="%g" font-size="10" text-anchor="end" fill="var(--sub)">%d</text>`, margem-4, y+3, int(f*float64(max)+0.5)))
	}
	col := (larg - 10) / 2
	for i, p := range pts {
		x := margem + float64(i)*larg + 4
		for j, v := range []int{p.A, p.B} {
			h := float64(v) / float64(max) * (alt - 14)
			cor := "var(--teal)"
			if j == 1 {
				cor = "var(--pri)"
			}
			b.WriteString(fmt.Sprintf(`<rect x="%g" y="%g" width="%g" height="%g" rx="2" fill="%s"><title>%s: %d %s</title></rect>`,
				x+float64(j)*(col+1), alt-h, col, h, cor, esc(p.Rotulo), v, map[int]string{0: "abertos", 1: "resolvidos"}[j]))
		}
		b.WriteString(fmt.Sprintf(`<text x="%g" y="%g" font-size="10" text-anchor="middle" fill="var(--sub)">%s</text>`, x+col, alt+14, esc(p.Rotulo)))
	}
	b.WriteString(`</svg></div>`)
	b.WriteString(`<div style="display:flex;gap:16px;font-size:13px;margin-top:6px"><span><span style="display:inline-block;width:10px;height:10px;border-radius:2px;background:var(--teal)"></span> Abertos</span><span><span style="display:inline-block;width:10px;height:10px;border-radius:2px;background:var(--pri)"></span> Resolvidos</span></div>`)
	return template.HTML(b.String())
}

// exportarChamadosCSV: todos os chamados (respeita o filtro da lista), para Excel.
func (s *Servidor) exportarChamadosCSV(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	f := s.filtroDaURL(r, sess)
	if r.URL.Query().Get("status") == "" {
		f.Status = ""
	}
	lista, _ := s.b.Chamados(f)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="chamados-`+time.Now().Format("2006-01-02")+`.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	_ = cw.Write([]string{"Número", "Título", "Status", "Prioridade", "Categoria", "Solicitante", "Setor", "E-mail", "Origem",
		"Responsável", "Máquina", "Aberto em", "Prazo", "Resolvido em", "Situação do prazo", "Tempo até resolver (h)", "Comentários", "Anexos"})
	for _, c := range lista {
		tempo := ""
		if ini, ok := parseData(c.CriadoEm); ok {
			if fim, ok := parseData(c.ResolvidoEm); ok {
				tempo = strings.Replace(fmt.Sprintf("%.1f", fim.Sub(ini).Hours()), ".", ",", 1)
			}
		}
		origem := "Painel"
		if c.Publico() {
			origem = "Página sem login"
		}
		_ = cw.Write([]string{strconv.FormatInt(c.ID, 10), c.Titulo, c.RotuloStatus(), c.RotuloPrioridade(), c.Categoria,
			c.Solicitante(), c.SolicitanteSetor, c.SolicitanteEmail, origem, c.AtribuidoNome, c.MaquinaHostname,
			formatarData(c.CriadoEm), formatarData(c.Prazo), formatarData(c.ResolvidoEm), c.TextoPrazo(), tempo,
			strconv.Itoa(c.QtdComentarios), strconv.Itoa(c.QtdAnexos)})
	}
	cw.Flush()
	s.b.Auditar(sess.Admin.Usuario, "exportar chamados", fmt.Sprintf("%d chamados", len(lista)))
}

// ---------------- configurações de chamados (Administração) ----------------

type itemSLA struct {
	Prioridade, Rotulo string
	Horas              int
}

func (s *Servidor) slaAtual() []itemSLA {
	var l []itemSLA
	for _, p := range []string{"urgente", "alta", "normal", "baixa"} {
		l = append(l, itemSLA{Prioridade: p, Rotulo: rotuloPrioridade(p), Horas: s.b.HorasSLA(p)})
	}
	return l
}

func (s *Servidor) acaoConfigChamados(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	var mudou []string
	for _, p := range []string{"urgente", "alta", "normal", "baixa"} {
		v := strings.TrimSpace(r.FormValue("sla_" + p))
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 24*90 {
			redirecionar(w, r, "/admin#chamados", "", fmt.Errorf("prazo de %s inválido: use horas entre 1 e 2160", rotuloPrioridade(p)))
			return
		}
		if n != s.b.HorasSLA(p) {
			_ = s.b.DefinirConfig("sla_"+p, strconv.Itoa(n))
			mudou = append(mudou, fmt.Sprintf("%s=%dh", p, n))
		}
	}
	pub := "0"
	if r.FormValue("publico") == "1" {
		pub = "1"
	}
	_ = s.b.DefinirConfig("chamados_publico", pub)
	avisar := "0"
	if r.FormValue("avisar") == "1" {
		avisar = "1"
	}
	_ = s.b.DefinirConfig("avisar_chamados", avisar)
	u := strings.TrimSpace(r.FormValue("webhook_chamados"))
	if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		redirecionar(w, r, "/admin#chamados", "", fmt.Errorf("o webhook deve começar com http:// ou https://"))
		return
	}
	_ = s.b.DefinirConfig("webhook_chamados", u)
	s.b.Auditar(sess.Admin.Usuario, "configurar chamados", fmt.Sprintf("página pública=%s, avisos=%s, prazos: %s", pub, avisar, strings.Join(mudou, " ")))
	msg := "Configurações de chamados salvas."
	if len(mudou) > 0 {
		msg += " Os novos prazos valem para chamados abertos daqui em diante (e quando a prioridade de um chamado mudar)."
	}
	redirecionar(w, r, "/admin#chamados", msg, nil)
}

func (s *Servidor) acaoTestarWebhookChamados(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	u := strings.TrimSpace(s.b.Config("webhook_chamados"))
	if u == "" {
		u = s.b.Config("webhook_url")
	}
	if u == "" {
		redirecionar(w, r, "/admin#chamados", "", fmt.Errorf("nenhum webhook configurado"))
		return
	}
	err := s.postarWebhook(u, "Chamados: mensagem de teste enviada por "+sess.Admin.Usuario)
	redirecionar(w, r, "/admin#chamados", "Mensagem de teste enviada.", err)
}
