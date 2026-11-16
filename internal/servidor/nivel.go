package servidor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"inventario/internal/modelo"
)

// Avaliação do nível ("saúde") de cada máquina por pontuação. Os limiares são configuráveis
// (guardados em config); as regras de pontos seguem o padrão combinado e podem ser ajustadas
// no código numa versão futura.

type ParamsNivel struct {
	OKMin      int // pontuação mínima para OK
	AlertaMin  int // pontuação mínima para Alerta
	RAMCritico int // RAM (GB) que força Crítico
}

func (b *Banco) ParamsNivel() ParamsNivel {
	p := ParamsNivel{OKMin: 6, AlertaMin: 4, RAMCritico: 4}
	if v, err := strconv.Atoi(b.Config("nivel_ok_min")); err == nil && v > 0 {
		p.OKMin = v
	}
	if v, err := strconv.Atoi(b.Config("nivel_alerta_min")); err == nil && v > 0 {
		p.AlertaMin = v
	}
	if v, err := strconv.Atoi(b.Config("nivel_ram_critico")); err == nil && v > 0 {
		p.RAMCritico = v
	}
	return p
}

type Avaliacao struct {
	Classe string // OK, Alerta, Crítico
	Pontos int
	RAMGb  int
	Itens  []string // explicação de cada item
}

var reGeracao = regexp.MustCompile(`i[3579][-\s]*([0-9]{3,5})`)

// geracaoIntel extrai a geração do Core a partir do número do modelo.
func geracaoIntel(cpu string) int {
	m := reGeracao.FindStringSubmatch(strings.ToLower(cpu))
	if m == nil {
		return 0
	}
	num := m[1]
	switch len(num) {
	case 5:
		g, _ := strconv.Atoi(num[:2]) // ex.: 10210 -> 10, 12700 -> 12
		return g
	case 4:
		// Modelos de 4 dígitos começados em "1" são 10ª a 14ª geração (dois primeiros
		// dígitos): i5-1035G1 -> 10, i5-1135G7 -> 11, i5-1235U -> 12. Os demais são
		// de geração 2ª a 9ª (um dígito): i5-8250U -> 8, i5-9400 -> 9.
		if num[0] == '1' {
			g, _ := strconv.Atoi(num[:2])
			return g
		}
		g, _ := strconv.Atoi(num[:1])
		return g
	case 3:
		return 1 // 1ª geração (ex.: i5-650)
	}
	return 0
}

var reRyzen = regexp.MustCompile(`ryzen\s*[3579]\s*([0-9]{4})`)

// serieRyzen extrai a série (primeiro dígito do modelo de 4 dígitos) de um AMD Ryzen.
// Ex.: "Ryzen 5 5600U" -> 5 (série 5000); "Ryzen 7 4700U" -> 4.
func serieRyzen(cpu string) int {
	m := reRyzen.FindStringSubmatch(strings.ToLower(cpu))
	if m == nil {
		return 0
	}
	s, _ := strconv.Atoi(m[1][:1])
	return s
}

// tierGeracao converte o número da geração (Intel) em pontos (0-3).
func tierGeracao(g int) int {
	switch {
	case g >= 8:
		return 3
	case g >= 6:
		return 2
	case g >= 4:
		return 1
	default:
		return 0
	}
}

// mapRyzen mapeia a série do Ryzen para uma geração Intel equivalente (aproximado),
// só para reaproveitar a mesma faixa de pontos.
func mapRyzen(serie int) int {
	switch {
	case serie >= 5:
		return 10 // 5000/6000/7000+ ~ Intel 10ª+ (3 pt)
	case serie == 4:
		return 8 // 4000 ~ 8ª (3 pt)
	case serie == 3:
		return 6 // 3000 ~ 6ª (2 pt)
	case serie == 2:
		return 4 // 2000 ~ 4ª (1 pt)
	default:
		return 0
	}
}

// pontosGeracao retorna os pontos (0-3) e o rótulo da geração/modernidade do processador,
// reconhecendo Intel Core (i3/i5/i7/i9) e AMD Ryzen.
func pontosGeracao(cpu string) (int, string) {
	if g := geracaoIntel(cpu); g > 0 {
		return tierGeracao(g), fmt.Sprintf("Intel %dª geração", g)
	}
	if s := serieRyzen(cpu); s > 0 {
		return tierGeracao(mapRyzen(s)), fmt.Sprintf("AMD Ryzen série %d000", s)
	}
	return 0, "desconhecida"
}

func linhaIntel(cpu string) string {
	c := strings.ToLower(cpu)
	for _, l := range []string{"i9", "i7", "i5", "i3"} {
		if strings.Contains(c, l) {
			return l
		}
	}
	// AMD: trata Ryzen 3/5/7/9 de forma equivalente
	for _, l := range []string{"ryzen 9", "ryzen 7", "ryzen 5", "ryzen 3"} {
		if strings.Contains(c, l) {
			return "i" + string(l[len(l)-1]) // mapeia para i3/i5/i7/i9
		}
	}
	return ""
}

// Avaliar calcula a pontuação e a classe de uma máquina.
func (b *Banco) Avaliar(m *Maquina, p ParamsNivel) Avaliacao {
	var a Avaliacao
	// Geração / modernidade do processador: 0-3 (Intel Core ou AMD Ryzen)
	gp, rotGer := pontosGeracao(m.CPU)
	a.Pontos += gp
	a.Itens = append(a.Itens, fmt.Sprintf("Geração do processador: %s (%d pt)", rotGer, gp))

	// Linha do processador: i3=0, i5=1, i7/i9=2
	linha := linhaIntel(m.CPU)
	lp := 0
	switch linha {
	case "i7", "i9":
		lp = 2
	case "i5":
		lp = 1
	default:
		lp = 0
	}
	a.Pontos += lp
	rotLinha := linha
	if rotLinha == "" {
		rotLinha = "não reconhecida"
	}
	a.Itens = append(a.Itens, fmt.Sprintf("Linha do processador: %s (%d pt)", rotLinha, lp))

	// RAM: 0-3
	a.RAMGb = int(m.RAM / (1024 * 1024 * 1024))
	rp := 0
	switch {
	case a.RAMGb >= 16:
		rp = 3
	case a.RAMGb >= 10:
		rp = 2
	case a.RAMGb >= 6:
		rp = 1
	default:
		rp = 0
	}
	a.Pontos += rp
	a.Itens = append(a.Itens, fmt.Sprintf("Memória RAM: %d GB (%d pt)", a.RAMGb, rp))

	// Armazenamento: SSD e tamanho
	temSSD, tamSSDgb := melhorSSD(m.DadosJSON)
	sp := 0
	switch {
	case temSSD && tamSSDgb >= 200:
		sp = 2
	case temSSD:
		sp = 1
	default:
		sp = 0
	}
	a.Pontos += sp
	desc := "sem SSD"
	if temSSD {
		desc = fmt.Sprintf("SSD %d GB", tamSSDgb)
	}
	a.Itens = append(a.Itens, fmt.Sprintf("Armazenamento: %s (%d pt)", desc, sp))

	// Classificação
	switch {
	case a.RAMGb > 0 && a.RAMGb <= p.RAMCritico:
		a.Classe = "Crítico"
		a.Itens = append(a.Itens, fmt.Sprintf("RAM ≤ %d GB força Crítico", p.RAMCritico))
	case a.Pontos >= p.OKMin:
		a.Classe = "OK"
	case a.Pontos >= p.AlertaMin:
		a.Classe = "Alerta"
	default:
		a.Classe = "Crítico"
	}
	return a
}

// melhorSSD diz se há SSD e o tamanho (GB) do maior SSD.
func melhorSSD(dadosJSON string) (bool, int) {
	var r modelo.Relatorio
	if json.Unmarshal([]byte(dadosJSON), &r) != nil {
		return false, 0
	}
	tem := false
	maior := 0
	for _, d := range r.Discos {
		t := strings.ToUpper(d.Tipo + " " + d.Barramento)
		if strings.Contains(t, "SSD") || strings.Contains(t, "NVME") {
			tem = true
			gb := int(d.Tamanho / (1024 * 1024 * 1024))
			if gb > maior {
				maior = gb
			}
		}
	}
	return tem, maior
}
