package servidor

// Marca (1.19, "white-label"). O mesmo código gera duas edições:
//   - "ocupacional" (padrão): logo e cores da Ocupacional, como sempre;
//   - "generica" (compilar com -X inventario/internal/servidor.Edicao=generica): nome Harvest,
//     logo neutra e paleta neutra.
// Em qualquer edição o administrador troca nome, logos, ícone e cores em Configurações → Marca.
// A partir das duas cores o servidor calcula a paleta inteira (fundos, bordas, textos de destaque)
// dos temas claro e escuro, garantindo contraste legível.

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Edicao é gravada na compilação (ldflags). Vazio ou "ocupacional" = edição da Ocupacional.
var Edicao = "ocupacional"

func edicaoGenerica() bool { return Edicao == "generica" }

type Marca struct {
	Nome          string // nome da empresa/sistema mostrado nas telas
	Titulo        string // sufixo do <title>
	CorPri        string // cor de destaque (botões, aba ativa)
	CorSec        string // cor secundária (avatar, cabeçalho do e-mail, tom dos fundos)
	LogoClaro     string // URL da logo para fundo ESCURO
	LogoEscuro    string // URL da logo para fundo CLARO
	Favicon       string
	Simbolo       string // marca d'água do Início ("" = sem)
	Personalizada bool   // alguma cor/logo foi trocada pelo administrador
	Ocupacional   bool   // edição da Ocupacional (assinatura "T.I Ocupacional")
	Versao        string // carimbo para o navegador recarregar /marca.css e as logos
	TemLogoClaro  bool
	TemLogoEscuro bool
	TemFavicon    bool
}

func padraoMarca() Marca {
	if edicaoGenerica() {
		return Marca{Nome: "Harvest", Titulo: "Harvest", CorPri: "#E3B341", CorSec: "#1F2A37",
			LogoClaro: "/static/harvest-claro.svg", LogoEscuro: "/static/harvest-escuro.svg",
			Favicon: "/static/harvest-simbolo.svg", Simbolo: "/static/harvest-simbolo.svg"}
	}
	return Marca{Nome: "Ocupacional", Titulo: "Ocupacional TI", CorPri: "#ADDA23", CorSec: "#18424C",
		LogoClaro: "/static/logo-claro.svg", LogoEscuro: "/static/logo-cor.svg",
		Favicon: "/static/simbolo-cor.svg", Simbolo: "/static/simbolo-cor.svg", Ocupacional: true}
}

var (
	reHex       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	reEventoSVG = regexp.MustCompile(`\son[a-z]+\s*=`)
)

func (s *Servidor) marca() Marca {
	m := padraoMarca()
	m.Versao = s.b.Config("marca_versao")
	if n := strings.TrimSpace(s.b.Config("marca_nome")); n != "" {
		m.Nome, m.Titulo = n, n
	}
	if c := s.b.Config("marca_cor_pri"); reHex.MatchString(c) && !strings.EqualFold(c, m.CorPri) {
		m.CorPri, m.Personalizada = strings.ToUpper(c), true
	}
	if c := s.b.Config("marca_cor_sec"); reHex.MatchString(c) && !strings.EqualFold(c, m.CorSec) {
		m.CorSec, m.Personalizada = strings.ToUpper(c), true
	}
	arqs := s.b.arquivosMarca()
	v := "?v=" + m.Versao
	if arqs["logo_claro"] {
		m.LogoClaro, m.TemLogoClaro, m.Personalizada = "/marca/logo_claro"+v, true, true
	}
	if arqs["logo_escuro"] {
		m.LogoEscuro, m.TemLogoEscuro, m.Personalizada = "/marca/logo_escuro"+v, true, true
	}
	// só uma das logos enviada: usa a mesma nos dois temas
	if m.TemLogoClaro && !m.TemLogoEscuro {
		m.LogoEscuro = m.LogoClaro
	} else if m.TemLogoEscuro && !m.TemLogoClaro {
		m.LogoClaro = m.LogoEscuro
	}
	if arqs["favicon"] {
		m.Favicon, m.Simbolo, m.TemFavicon = "/marca/favicon"+v, "/marca/favicon"+v, true
	} else if m.TemLogoClaro || m.TemLogoEscuro {
		m.Simbolo = "" // logo própria sem ícone: tira a marca d'água da Ocupacional/Harvest
	}
	return m
}

func (b *Banco) arquivosMarca() map[string]bool {
	r := map[string]bool{}
	rows, err := b.db.Query(`SELECT chave FROM marca_arquivos`)
	if err != nil {
		return r
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if rows.Scan(&k) == nil {
			r[k] = true
		}
	}
	return r
}

// ---------- cores ----------

type rgb struct{ r, g, b float64 } // 0–1

func hexParaRGB(h string) rgb {
	v, _ := strconv.ParseUint(strings.TrimPrefix(h, "#"), 16, 32)
	return rgb{float64(v>>16&255) / 255, float64(v>>8&255) / 255, float64(v&255) / 255}
}

func (c rgb) hex() string {
	f := func(x float64) int { return int(math.Round(math.Max(0, math.Min(1, x)) * 255)) }
	return fmt.Sprintf("#%02X%02X%02X", f(c.r), f(c.g), f(c.b))
}

func (c rgb) hsl() (h, s, l float64) {
	mx, mn := math.Max(c.r, math.Max(c.g, c.b)), math.Min(c.r, math.Min(c.g, c.b))
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case c.r:
		h = (c.g - c.b) / d
		if c.g < c.b {
			h += 6
		}
	case c.g:
		h = (c.b-c.r)/d + 2
	default:
		h = (c.r-c.g)/d + 4
	}
	return h * 60, s, l
}

func doHSL(h, s, l float64) rgb {
	if s == 0 {
		return rgb{l, l, l}
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	f := func(t float64) float64 {
		t = math.Mod(t+1, 1)
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	h /= 360
	return rgb{f(h + 1.0/3), f(h), f(h - 1.0/3)}
}

func hsl(h, s, l float64) string { return doHSL(h, s, l).hex() }

func (c rgb) luminancia() float64 {
	f := func(x float64) float64 {
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.r) + 0.7152*f(c.g) + 0.0722*f(c.b)
}

func contraste(a, b rgb) float64 {
	la, lb := a.luminancia(), b.luminancia()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// ajustarContraste clareia (ou escurece) a cor até ter o contraste mínimo com o fundo.
func ajustarContraste(cor, fundo rgb, minimo float64, clarear bool) rgb {
	h, s, l := cor.hsl()
	for i := 0; i < 60 && contraste(cor, fundo) < minimo; i++ {
		if clarear {
			l = math.Min(1, l+0.02)
		} else {
			l = math.Max(0, l-0.02)
		}
		cor = doHSL(h, s, l)
	}
	return cor
}

// textoSobre: quase-preto ou quase-branco, o que tiver mais contraste com a cor.
func textoSobre(c rgb) string {
	h, _, _ := c.hsl()
	escuro, claro := doHSL(h, 0.35, 0.08), doHSL(h, 0.2, 0.97)
	if contraste(escuro, c) >= contraste(claro, c) {
		return escuro.hex()
	}
	return claro.hex()
}

// cssMarca: variáveis dos temas escuro e claro a partir das duas cores.
func cssMarca(m Marca) string {
	p, sec := hexParaRGB(m.CorPri), hexParaRGB(m.CorSec)
	h, sS, _ := sec.hsl()
	ts := math.Min(sS, 0.22) // fundos levemente tingidos pela cor secundária
	_, _, lp := p.hsl()

	fundoE := doHSL(h, ts, 0.055)
	fundoC := doHSL(h, ts*0.7, 0.955)
	priTE := ajustarContraste(p, fundoE, 4.5, true)
	priTC := ajustarContraste(p, fundoC, 4.5, false)
	ph, ps, _ := p.hsl()
	pri2 := doHSL(ph, ps, math.Min(0.92, lp+0.08))
	if lp > 0.75 {
		pri2 = doHSL(ph, ps, lp-0.08)
	}
	sobre := textoSobre(p)
	sh, ss, sl := sec.hsl()
	teal := doHSL(sh, math.Min(ss, 0.45), math.Max(sl, 0.58))

	var b strings.Builder
	fmt.Fprintf(&b, `/* gerado a partir da marca: %s / %s */
:root{
  --fundo:%s;--cartao:%s;--cartao2:%s;--linha:%s;--borda:%s;--borda2:%s;
  --texto:%s;--sub:%s;--fraco:%s;--menu-t:%s;--forte:#fff;--topo:%s;
  --pri:%s;--pri2:%s;--sobre-pri:%s;--pri-t:%s;--pri-t2:%s;
  --petroleo:%s;--teal:%s;--verde:#3FB950;--verde-fundo:rgba(63,185,80,.12);
}
html[data-tema="claro"]{
  --fundo:%s;--cartao:%s;--cartao2:%s;--linha:%s;--borda:%s;--borda2:%s;
  --texto:%s;--sub:%s;--fraco:%s;--menu-t:%s;--forte:%s;--topo:%s;
  --pri:%s;--pri2:%s;--sobre-pri:%s;--pri-t:%s;--pri-t2:%s;
  --petroleo:%s;--teal:%s;--verde:#1A7F37;--verde-fundo:rgba(26,127,55,.10);
}
.avatar,html[data-tema="claro"] .avatar{color:%s}
.heroi .detalhe{color:%s;opacity:.85}
.selo.verde{background:var(--verde);border-color:var(--verde);color:#08210F}
`,
		m.CorPri, m.CorSec,
		fundoE.hex(), hsl(h, ts, 0.09), hsl(h, ts*0.9, 0.125), hsl(h, ts*0.9, 0.125), hsl(h, ts*0.8, 0.165), hsl(h, ts*0.7, 0.215),
		hsl(h, ts*0.7, 0.93), hsl(h, ts*0.6, 0.62), hsl(h, ts*0.5, 0.48), hsl(h, ts*0.6, 0.74), fundoE.hex(),
		m.CorPri, pri2.hex(), sobre, priTE.hex(), ajustarContraste(pri2, fundoE, 4.5, true).hex(),
		m.CorSec, teal.hex(),
		fundoC.hex(), hsl(h, ts*0.6, 0.995), hsl(h, ts*0.8, 0.94), hsl(h, ts*0.7, 0.915), hsl(h, ts*0.6, 0.88), hsl(h, ts*0.5, 0.8),
		hsl(h, math.Max(ts, 0.25), 0.1), hsl(h, ts*0.5, 0.38), hsl(h, ts*0.4, 0.54), hsl(h, ts*0.5, 0.32), hsl(h, 0.35, 0.09), fundoC.hex(),
		m.CorPri, pri2.hex(), sobre, priTC.hex(), doHSL(ph, ps, math.Max(0, mustL(priTC)-0.06)).hex(),
		m.CorSec, doHSL(sh, math.Min(ss, 0.45), math.Min(sl, 0.4)).hex(),
		textoSobre(sec), sobre)
	return b.String()
}

func mustL(c rgb) float64 { _, _, l := c.hsl(); return l }

// ---------- rotas ----------

func (s *Servidor) servirCSSMarca(w http.ResponseWriter, r *http.Request) {
	m := s.marca()
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if !m.Personalizada && !edicaoGenerica() {
		_, _ = io.WriteString(w, "/* marca padrão da Ocupacional: estilo.css */\n")
		return
	}
	_, _ = io.WriteString(w, cssMarca(m))
}

func (s *Servidor) servirArquivoMarca(w http.ResponseWriter, r *http.Request) {
	var tipo string
	var dados []byte
	if err := s.b.db.QueryRow(`SELECT tipo, dados FROM marca_arquivos WHERE chave=?`, r.PathValue("arquivo")).Scan(&tipo, &dados); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", tipo)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	// um SVG aberto direto no navegador não pode rodar nada
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	_, _ = w.Write(dados)
}

const maxArquivoMarca = 1 << 20 // 1 MB

func tipoImagemMarca(b []byte) (string, error) {
	t := http.DetectContentType(b)
	switch {
	case t == "image/png", t == "image/jpeg", t == "image/gif", t == "image/webp", t == "image/x-icon", t == "image/vnd.microsoft.icon":
		return t, nil
	case bytes.Contains(bytes.ToLower(b[:min(len(b), 4096)]), []byte("<svg")):
		low := bytes.ToLower(b)
		if bytes.Contains(low, []byte("<script")) || bytes.Contains(low, []byte("javascript:")) || reEventoSVG.Match(low) {
			return "", fmt.Errorf("SVG com script não é aceito")
		}
		return "image/svg+xml", nil
	}
	return "", fmt.Errorf("formato não aceito (use PNG, JPG, SVG, WEBP ou ICO)")
}

func (s *Servidor) acaoMarca(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	// corpo já limitado e lido no middleware (protegido) antes da checagem de CSRF
	if r.FormValue("padrao") != "" {
		for _, k := range []string{"marca_nome", "marca_cor_pri", "marca_cor_sec"} {
			_ = s.b.DefinirConfig(k, "")
		}
		_, _ = s.b.db.Exec(`DELETE FROM marca_arquivos`)
		_ = s.b.DefinirConfig("marca_versao", strconv.FormatInt(time.Now().Unix(), 36))
		s.b.Auditar(sess.Admin.Usuario, "marca: voltar ao padrão", "")
		redirecionar(w, r, "/configuracoes#marca", "Marca de volta ao padrão.", nil)
		return
	}
	nome := strings.TrimSpace(r.FormValue("nome"))
	if len([]rune(nome)) > 60 {
		nome = string([]rune(nome)[:60])
	}
	_ = s.b.DefinirConfig("marca_nome", nome)
	for campo, chave := range map[string]string{"cor_pri": "marca_cor_pri", "cor_sec": "marca_cor_sec"} {
		c := strings.TrimSpace(r.FormValue(campo))
		if c != "" && !reHex.MatchString(c) {
			redirecionar(w, r, "/configuracoes#marca", "", fmt.Errorf("cor inválida: %s (use #RRGGBB)", c))
			return
		}
		_ = s.b.DefinirConfig(chave, strings.ToUpper(c))
	}
	for _, k := range []string{"logo_claro", "logo_escuro", "favicon"} {
		if r.FormValue("remover_"+k) == "1" {
			_, _ = s.b.db.Exec(`DELETE FROM marca_arquivos WHERE chave=?`, k)
			continue
		}
		f, _, err := r.FormFile(k)
		if err != nil {
			continue // não enviado
		}
		dados, err := io.ReadAll(io.LimitReader(f, maxArquivoMarca+1))
		f.Close()
		if err == nil && len(dados) > maxArquivoMarca {
			err = fmt.Errorf("%s: maior que 1 MB", k)
		}
		var tipo string
		if err == nil {
			tipo, err = tipoImagemMarca(dados)
		}
		if err != nil {
			redirecionar(w, r, "/configuracoes#marca", "", err)
			return
		}
		if _, err := s.b.db.Exec(`INSERT INTO marca_arquivos(chave,tipo,dados,atualizado) VALUES(?,?,?,?)
			ON CONFLICT(chave) DO UPDATE SET tipo=excluded.tipo, dados=excluded.dados, atualizado=excluded.atualizado`, k, tipo, dados, agora()); err != nil {
			redirecionar(w, r, "/configuracoes#marca", "", err)
			return
		}
	}
	_ = s.b.DefinirConfig("marca_versao", strconv.FormatInt(time.Now().Unix(), 36))
	s.b.Auditar(sess.Admin.Usuario, "marca", fmt.Sprintf("nome=%q pri=%s sec=%s", nome, r.FormValue("cor_pri"), r.FormValue("cor_sec")))
	redirecionar(w, r, "/configuracoes#marca", "Marca salva.", nil)
}

// NomeProduto é o nome mostrado no console/serviço: "Harvest" na edição genérica.
func NomeProduto() string {
	if edicaoGenerica() {
		return "Harvest"
	}
	return "Inventário"
}
