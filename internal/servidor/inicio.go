package servidor

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// Página "Início" (1.9): visão geral em blocos para admin e técnico — chamados em aberto,
// prazos, fila, semana, parque de máquinas, o que pede atenção e atalhos.

type itemFila struct {
	C           Chamado
	Situacao    string // atrasado | vence | ok
	PrazoCurto  string // "1 h", "2 d", "atrasado"
	Responsavel string
}

type diaSemana struct {
	Rotulo string
	Qtd    int
	Altura int // px da coluna
	Hoje   bool
}

type barraH struct {
	Rotulo string
	Qtd    int
	Pct    int
}

func saudacao(t time.Time) string {
	switch h := t.Hour(); {
	case h < 12:
		return "Bom dia"
	case h < 18:
		return "Boa tarde"
	default:
		return "Boa noite"
	}
}

var diasSemana = []string{"domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"}
var diasCurtos = []string{"Dom", "Seg", "Ter", "Qua", "Qui", "Sex", "Sáb"}

func prazoCurto(c Chamado) string {
	prazo, ok := parseData(c.Prazo)
	if !ok {
		return "—"
	}
	d := time.Until(prazo)
	if d < 0 {
		return "atrasado"
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes())+1)
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	default:
		return fmt.Sprintf("%d d", int(d.Hours()/24))
	}
}

// ultimoBackup devolve a data do backup automático mais recente (ou "").
func (s *Servidor) ultimoBackup() string {
	if s.dirBackup == "" {
		return ""
	}
	ents, err := os.ReadDir(s.dirBackup)
	if err != nil {
		return ""
	}
	var mais time.Time
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "inventario_") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(mais) {
			mais = info.ModTime()
		}
	}
	if mais.IsZero() {
		return ""
	}
	return mais.UTC().Format(time.RFC3339)
}

func (s *Servidor) paginaInicio(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	agoraLocal := time.Now()

	// ---------- chamados ----------
	abertos, _ := s.b.Chamados(FiltroChamados{Status: "abertos"})
	semResp, atrasados, vencendo := 0, 0, 0
	var fila []itemFila
	for _, c := range abertos {
		if c.AtribuidoID == 0 {
			semResp++
		}
		sit := c.SituacaoPrazo()
		switch sit {
		case "atrasado":
			atrasados++
		case "vence":
			vencendo++
		}
		if len(fila) < 5 {
			resp := c.AtribuidoNome
			if resp == "" {
				resp = "sem responsável"
			}
			fila = append(fila, itemFila{C: c, Situacao: sit, PrazoCurto: prazoCurto(c), Responsavel: resp})
		}
	}
	// a fila mostra primeiro o que está atrasado/vencendo
	peso := map[string]int{"atrasado": 0, "vence": 1}
	sort.SliceStable(fila, func(i, j int) bool {
		pi, ok := peso[fila[i].Situacao]
		if !ok {
			pi = 2
		}
		pj, ok := peso[fila[j].Situacao]
		if !ok {
			pj = 2
		}
		return pi < pj
	})
	var detalhes []string
	if semResp > 0 {
		detalhes = append(detalhes, plural(semResp, "sem responsável", "sem responsável"))
	}
	if atrasados > 0 {
		detalhes = append(detalhes, plural(atrasados, "atrasado", "atrasados"))
	}
	if vencendo > 0 {
		detalhes = append(detalhes, plural(vencendo, "vence logo", "vencem logo"))
	}
	if len(detalhes) == 0 {
		if len(abertos) == 0 {
			detalhes = append(detalhes, "nenhum chamado esperando a TI")
		} else {
			detalhes = append(detalhes, "todos com responsável e dentro do prazo")
		}
	}

	// últimos 30 dias: resolvidos, % no prazo, tempo médio, por categoria
	desde := agoraLocal.UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	periodo, _ := s.b.Chamados(FiltroChamados{Desde: desde})
	resolvidos, noPrazo, comPrazo := 0, 0, 0
	var somaH float64
	cat := map[string]int{}
	for _, c := range periodo {
		if c.CriadoEm >= desde {
			cat[vazioTraco(c.Categoria)]++
		}
		if c.Aberto() || c.ResolvidoEm < desde {
			continue
		}
		resolvidos++
		if ini, ok := parseData(c.CriadoEm); ok {
			if fim, ok := parseData(c.ResolvidoEm); ok {
				somaH += fim.Sub(ini).Hours()
			}
		}
		switch c.SituacaoPrazo() {
		case "cumprido":
			noPrazo++
			comPrazo++
		case "estourado":
			comPrazo++
		}
	}
	pctPrazo := -1
	if comPrazo > 0 {
		pctPrazo = noPrazo * 100 / comPrazo
	}
	media := "—"
	if resolvidos > 0 {
		media = textoHoras(somaH / float64(resolvidos))
	}
	var categorias []barraH
	for k, v := range cat {
		categorias = append(categorias, barraH{Rotulo: k, Qtd: v})
	}
	sort.Slice(categorias, func(i, j int) bool {
		if categorias[i].Qtd != categorias[j].Qtd {
			return categorias[i].Qtd > categorias[j].Qtd
		}
		return categorias[i].Rotulo < categorias[j].Rotulo
	})
	if len(categorias) > 6 {
		categorias = categorias[:6]
	}
	for i := range categorias {
		categorias[i].Pct = categorias[i].Qtd * 100 / categorias[0].Qtd
	}

	// semana atual (segunda a domingo), chamados abertos por dia
	loc := time.Local
	hoje := time.Date(agoraLocal.Year(), agoraLocal.Month(), agoraLocal.Day(), 0, 0, 0, 0, loc)
	seg := hoje.AddDate(0, 0, -((int(hoje.Weekday()) + 6) % 7))
	semana := make([]diaSemana, 7)
	for i := range semana {
		d := seg.AddDate(0, 0, i)
		semana[i] = diaSemana{Rotulo: diasCurtos[d.Weekday()], Hoje: d.Equal(hoje)}
	}
	daSemana, _ := s.b.Chamados(FiltroChamados{Desde: seg.UTC().Format(time.RFC3339)})
	maxDia := 1
	for _, c := range daSemana {
		if t, ok := parseData(c.CriadoEm); ok {
			if i := int(t.In(loc).Sub(seg).Hours() / 24); i >= 0 && i < 7 {
				semana[i].Qtd++
				if semana[i].Qtd > maxDia {
					maxDia = semana[i].Qtd
				}
			}
		}
	}
	for i := range semana {
		semana[i].Altura = 4 + semana[i].Qtd*140/maxDia
	}

	// ---------- parque ----------
	maqs, _ := s.b.Maquinas("")
	maqs = contaveis(maqs) // máquinas ignoradas não entram nos números
	res := s.b.Resumo()
	pn := s.b.ParamsNivel()
	nOK, nAlerta, nCritico := 0, 0, 0
	for _, m := range maqs {
		switch s.b.Avaliar(m, pn).Classe {
		case "OK":
			nOK++
		case "Alerta":
			nAlerta++
		default:
			nCritico++
		}
	}
	prog := s.programaMonitorado()
	semMonitorado, genericos, desatualizados, semAnyDesk := 0, 0, 0, 0
	padroes := s.b.Config("alerta_generico_padroes")
	for _, m := range maqs {
		if monitorAusente(m.MonitorSituacao) {
			semMonitorado++
		}
		if nomeGenerico(m.Hostname, padroes) {
			genericos++
		}
		if anyDeskAusente(m.AnyDeskSituacao) {
			semAnyDesk++
		}
		if serieVersao(strings.TrimSpace(m.VersaoAgente)) != serieVersao(s.VersaoAgente) {
			desatualizados++
		}
	}
	est := s.b.EstatisticasPainel()
	emDia := 0
	if len(maqs) > 0 {
		emDia = res.Ativas24h * 100 / len(maqs)
	}

	s.render(w, r, "inicio", "Início", "inicio", sess, map[string]any{
		"Saudacao": saudacao(agoraLocal), "Nome": primeiroNome(sess.Admin),
		"Data":    fmt.Sprintf("%s, %s", diasSemana[agoraLocal.Weekday()], agoraLocal.Format("02/01")),
		"Abertos": len(abertos), "Detalhes": strings.Join(detalhes, " · "),
		"Fila": fila, "Atrasados": atrasados,
		"Resolvidos": resolvidos, "PctPrazo": pctPrazo, "Media": media,
		"Categorias": categorias, "Semana": semana,
		"Versao": s.Versao, "Endereco": s.baseURL(r), "UltimoBackup": s.ultimoBackup(), "EmDia": emDia,
		"Maquinas": len(maqs), "Ignoradas": s.b.QtdIgnoradas(), "AD": s.coberturaAD(30), "NOK": nOK, "NAlerta": nAlerta, "NCritico": nCritico,
		"MACDup": res.MACDuplicados, "SemMonitorado": semMonitorado, "Monitorado": prog, "Desatualizados": desatualizados,
		"DiscoCheio": est.DiscoCheio, "Genericos": genericos, "SemAnyDesk": semAnyDesk,
		"EhAdmin": sess.Admin.EhAdmin(), "PodeInventario": sess.Admin.PodeInventario(),
		"LinkPublico": s.baseURL(r) + "/abrir-chamado", "PublicoAtivo": s.publicoAtivo(),
	})
}

func plural(n int, um, varios string) string {
	if n == 1 {
		return "1 " + um
	}
	return fmt.Sprintf("%d %s", n, varios)
}

func primeiroNome(a Admin) string {
	n := strings.TrimSpace(a.Nome)
	if n == "" {
		return a.Usuario
	}
	if i := strings.IndexByte(n, ' '); i > 0 {
		return n[:i]
	}
	return n
}
