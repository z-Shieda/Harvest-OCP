package servidor

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"inventario/internal/modelo"
)

func (c *cliente) multipart(caminho string, campos map[string]string, fotos int) *httptest.ResponseRecorder {
	var corpo bytes.Buffer
	mw := multipart.NewWriter(&corpo)
	for k, v := range campos {
		_ = mw.WriteField(k, v)
	}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < fotos; i++ {
		fw, _ := mw.CreateFormFile("anexos", fmt.Sprintf("foto%d.png", i))
		_ = png.Encode(fw, img)
	}
	_ = mw.Close()
	r := httptest.NewRequest("POST", caminho, &corpo)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: cookieSessao, Value: c.cookie})
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	return w
}

func csrfDe(t *testing.T, b *Banco, c *cliente) string {
	s, err := b.SessaoPorToken(c.cookie)
	if err != nil {
		t.Fatal("sem sessão")
	}
	return s.CSRF
}

func semErro(t *testing.T, w *httptest.ResponseRecorder, oque string) {
	t.Helper()
	if loc := w.Header().Get("Location"); strings.Contains(loc, "erro=") {
		u, _ := url.Parse(loc)
		t.Fatalf("%s: %s", oque, u.Query().Get("erro"))
	}
	if w.Code >= 400 {
		t.Fatalf("%s: HTTP %d %s", oque, w.Code, w.Body.String())
	}
}

func paginaOK(t *testing.T, c *cliente, caminho string) string {
	t.Helper()
	w := c.req("GET", caminho, nil)
	if w.Code != 200 {
		t.Fatalf("GET %s: %d %s", caminho, w.Code, w.Header().Get("Location"))
	}
	if dir := os.Getenv("HTML_SAIDA"); dir != "" { // para conferir o visual das telas
		nome := strings.NewReplacer("/", "_", "?", "_", "=", "_", "&", "_").Replace(strings.Trim(caminho, "/"))
		_ = os.WriteFile(filepath.Join(dir, nome+".html"), w.Body.Bytes(), 0o644)
	}
	return w.Body.String()
}

func TestFluxoTermoDeEntrega(t *testing.T) {
	b := bancoTeste(t)
	s, err := Novo(b, "teste")
	if err != nil {
		t.Fatal(err)
	}
	var codigoEnviado string
	s.emailDireto = func(para []string, assunto, texto, html string) error {
		codigoEnviado = regexp.MustCompile(`\d{6}`).FindString(assunto)
		return nil
	}
	h := s.Rotas()
	_ = b.CriarAdmin("admin", "senha-admin-1", "Daniel Costa", "ti@ocupacional.com.br", "admin")
	ti := &cliente{t: t, h: h}
	ti.req("POST", "/login", url.Values{"usuario": {"admin"}, "senha": {"senha-admin-1"}})
	csrf := csrfDe(t, b, ti)
	semErro(t, ti.req("POST", "/admin/termos", url.Values{"csrf": {csrf}, "ativo": {"1"}, "permitir_http": {"1"}, "dominios": {"ocupacional.com.br"}, "emails_rh": {"rh@ocupacional.com.br"}}), "ligar")
	semErro(t, ti.req("POST", "/admin/termos/empresas", url.Values{"csrf": {csrf}, "razao_social": {"Ocupacional Ltda"}, "cnpj": {"11222333000181"}, "unidades": {"Matriz"}}), "empresa")

	// máquina do inventário com o agente 1.22 (usuário OCP\bia logado)
	rel := &modelo.Relatorio{AgentID: "ag-1", AgentVersion: "1.22.0", Hostname: "OCP-123", Fabricante: "Dell", Modelo: "Latitude 5440", Serial: "ABC123",
		SOName: "Windows 11 Pro", CPU: "Intel Core i5-1345U", RAMBytes: 16 << 30, UsuarioAtual: `OCP\bia`, UsuariosSessao: []string{`OCP\bia`},
		Redes: []modelo.Rede{{Nome: "Ethernet", MAC: "AA:BB:CC:DD:EE:01", IPv4: []string{"10.0.0.5"}, Conectada: true, Fisica: true}},
		Seguranca: &modelo.Seguranca{BitLocker: "ativo", AntivirusLido: true, Antivirus: []modelo.Antivirus{{Nome: "Bitdefender", Ativo: true, Atualizado: true}},
			AdminsLidos: true, AdminsLocais: []string{`OCP-123\Administrador`, `OCP\Domain Admins`}, UltimaAtualizacao: agora()}}
	res, err := b.Ingerir(rel, "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	b.GravarSeguranca(res.MaquinaID, rel)
	maq := res.MaquinaID
	paginaOK(t, ti, fmt.Sprintf("/maquinas/%d", maq))
	semErro(t, ti.req("POST", fmt.Sprintf("/maquinas/%d/editar", maq), url.Values{"csrf": {csrf}, "patrimonio": {"PAT-0042"}}), "patrimônio")
	semErro(t, ti.req("POST", fmt.Sprintf("/maquinas/%d/gestao", maq), url.Values{"csrf": {csrf}, "tipo_equip": {"notebook"}, "condicao": {"novo"}, "nf_numero": {"123"}, "nf_data": {"2026-01-10"}, "valor_referencia": {"4.500,00"}}), "gestão")

	// colaborador: convite, senha, ficha, validação com login de rede
	w := ti.req("POST", "/admin/colaboradores/convidar", url.Values{"csrf": {csrf}, "email": {"bia@ocupacional.com.br"}, "nome": {"Beatriz Lima"}})
	link := regexp.MustCompile(`/convite/[0-9a-f]{64}`).FindString(w.Body.String())
	col := &cliente{t: t, h: h}
	col.req("POST", link, url.Values{"senha": {"Bia-senha-forte"}, "confirma": {"Bia-senha-forte"}})
	ccsrf := csrfDe(t, b, col)
	ficha := url.Values{"csrf": {ccsrf}, "nome": {"Beatriz Lima"}, "cpf": {"529.982.247-25"}, "cargo": {"Analista"}, "setor": {"TI"},
		"empresa_id": {"1"}, "unidade": {"Matriz"}, "gestor": {"Daniel"}, "vinculo": {"clt"}, "regime": {"hibrido"}}
	semErro(t, col.req("POST", "/minha-ficha", ficha), "ficha")
	sessCol, _ := b.SessaoPorToken(col.cookie)
	bia := sessCol.Admin.ID
	ficha.Set("csrf", csrf)
	ficha.Set("acao", "validar")
	ficha.Set("login_rede", `OCP\bia`)
	semErro(t, ti.req("POST", fmt.Sprintf("/admin/colaboradores/%d/ficha", bia), ficha), "validar")

	// telas do editor
	paginaOK(t, ti, "/admin/termos/modelos")
	var entrega, anexo2 *TipoTermo
	for _, tp := range b.TiposTermo(false) {
		if tp.Chave == "entrega" {
			entrega = tp
		} else {
			anexo2 = tp
		}
	}
	paginaOK(t, ti, fmt.Sprintf("/admin/termos/modelos/%d", entrega.ID))
	paginaOK(t, ti, fmt.Sprintf("/admin/termos/versoes/%d", entrega.Publicada.ID))
	if w := ti.req("GET", fmt.Sprintf("/admin/termos/modelos/%d/previa.pdf?pessoa=%d&equipamento=m%d", entrega.ID, bia, maq), nil); w.Code != 200 || !strings.HasPrefix(w.Body.String(), "%PDF") {
		t.Fatalf("prévia: %d", w.Code)
	}
	paginaOK(t, ti, "/admin/termos/documentos")
	paginaOK(t, col, "/documentos/psi")

	// emitir: PJ e equipamento ausente são recusados
	paginaOK(t, ti, "/termos/novo")
	if w := ti.req("POST", "/termos/novo", url.Values{"csrf": {csrf}, "tipo": {fmt.Sprint(entrega.ID)}, "pessoa": {fmt.Sprint(bia)}}); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("sem equipamento deveria falhar")
	}
	semErro(t, ti.req("POST", "/termos/novo", url.Values{"csrf": {csrf}, "tipo": {fmt.Sprint(entrega.ID)}, "pessoa": {fmt.Sprint(bia)}, "equipamento": {fmt.Sprintf("m%d", maq)}}), "emitir")
	termos := b.Termos(FiltroTermos{AdminID: bia})
	if len(termos) != 1 || termos[0].ChamadoID == 0 || termos[0].Situacao != "rascunho" {
		t.Fatalf("termo emitido: %+v", termos)
	}
	tm := termos[0]
	if tm.Valores.Texto["equipamento_valor"] != "R$ 4.500,00" || tm.Valores.Texto["equipamento_patrimonio"] != "PAT-0042" {
		t.Fatalf("valores automáticos: %v", tm.Valores.Texto)
	}
	// um responsável por equipamento
	if w := ti.req("POST", "/termos/novo", url.Values{"csrf": {csrf}, "tipo": {fmt.Sprint(entrega.ID)}, "pessoa": {fmt.Sprint(bia)}, "equipamento": {fmt.Sprintf("m%d", maq)}}); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("termo duplicado deveria falhar")
	}
	pag := paginaOK(t, ti, fmt.Sprintf("/termos/%d/preencher", tm.ID))
	if !strings.Contains(pag, "agente: ativo") {
		t.Fatal("checklist de segurança deveria vir pré-marcada pelo agente")
	}
	// preencher: checklists completas, 3 fotos (mínimo 4) → envio recusado
	campos := map[string]string{"csrf": csrf}
	v, _ := b.Versao(tm.VersaoID)
	for _, c := range v.CamposDe("ti") {
		for i := range c.Itens {
			campos[fmt.Sprintf("cl_%s_%d", c.Chave, i)] = c.ColunasChecklist()[0]
		}
	}
	campos["acao"] = "enviar"
	w = ti.multipart(fmt.Sprintf("/termos/%d/preencher", tm.ID), campos, 3)
	if !strings.Contains(w.Header().Get("Location"), "erro=") || !strings.Contains(w.Header().Get("Location"), "foto") {
		t.Fatalf("envio com 3 fotos deveria falhar: %s", w.Header().Get("Location"))
	}
	semErro(t, ti.multipart(fmt.Sprintf("/termos/%d/preencher", tm.ID), campos, 1), "enviar com 4 fotos")
	tm, _ = b.Termo(tm.ID)
	if tm.Situacao != "aguardando" || tm.Valores.Texto["fotos_entrega"] != "4" {
		t.Fatalf("depois do envio: %s fotos=%s", tm.Situacao, tm.Valores.Texto["fotos_entrega"])
	}
	paginaOK(t, ti, fmt.Sprintf("/termos/%d", tm.ID))
	paginaOK(t, ti, "/termos")

	// colaborador: cai em Meus termos; ninguém assina o termo de outro
	if w := col.req("GET", "/", nil); w.Header().Get("Location") != "/meus-termos" {
		t.Fatalf("redirecionamento para Meus termos: %s", w.Header().Get("Location"))
	}
	paginaOK(t, col, "/meus-termos")
	paginaOK(t, col, fmt.Sprintf("/meus-termos/%d/assinar", tm.ID))
	assinar := url.Values{"csrf": {ccsrf}, "acao": {"codigo"}, "aceite_0": {"1"}, "aceite_1": {"1"}}
	// híbrido sem local de uso → recusado
	if w := col.req("POST", fmt.Sprintf("/meus-termos/%d/assinar", tm.ID), assinar); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("regime híbrido exige local de uso")
	}
	assinar.Set("c_local_uso", "Rua Exemplo, 100 - Belo Horizonte")
	assinar.Del("aceite_1")
	if w := col.req("POST", fmt.Sprintf("/meus-termos/%d/assinar", tm.ID), assinar); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("sem todos os aceites deveria falhar")
	}
	assinar.Set("aceite_1", "1")
	col.ip = "10.0.0.5:5000" // o navegador está na OCP-123
	semErro(t, col.req("POST", fmt.Sprintf("/meus-termos/%d/assinar", tm.ID), assinar), "pedir código")
	if len(codigoEnviado) != 6 {
		t.Fatal("código não enviado")
	}
	assinar.Set("acao", "assinar")
	assinar.Set("codigo", "000000")
	if codigoEnviado == "000000" {
		assinar.Set("codigo", "111111")
	}
	if w := col.req("POST", fmt.Sprintf("/meus-termos/%d/assinar", tm.ID), assinar); !strings.Contains(w.Header().Get("Location"), "incorreto") {
		t.Fatalf("código errado deveria falhar: %s", w.Header().Get("Location"))
	}
	// T.I não assina o termo de outra pessoa
	if w := ti.req("POST", fmt.Sprintf("/meus-termos/%d/assinar", tm.ID), url.Values{"csrf": {csrf}, "acao": {"assinar"}, "codigo": {codigoEnviado}}); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("outra pessoa não pode assinar")
	}
	assinar.Set("codigo", codigoEnviado)
	semErro(t, col.req("POST", fmt.Sprintf("/meus-termos/%d/assinar", tm.ID), assinar), "assinar")
	tm, _ = b.Termo(tm.ID)
	if tm.Situacao != "assinado" {
		t.Fatalf("situação: %s", tm.Situacao)
	}
	as := b.AssinaturasDoTermo(tm.ID)
	if len(as) != 1 || as[0].Grau != "confirmada" || as[0].Maquina != "OCP-123" || len(as[0].Hash) != 64 {
		t.Fatalf("assinatura: %+v", as)
	}
	pdfB, cod, err := b.PDFDaAssinatura(tm.ID)
	if err != nil || !bytes.HasPrefix(pdfB, []byte("%PDF")) || cod != as[0].Codigo {
		t.Fatal("PDF da assinatura")
	}
	// código reutilizado não vale
	if _, err := b.ConferirCodigo(bia, tm.ID, codigoEnviado); err == nil {
		t.Fatal("código de uso único")
	}
	// cópia para a pessoa e para o RH, com o PDF
	var copia, rh bool
	for _, e := range b.EnviosFila(50, 0) {
		if e.TermoID == tm.ID && e.Tipo == "copia" && e.AnexoNome != "" {
			copia = true
		}
		if e.TermoID == tm.ID && e.Tipo == "rh" && strings.Contains(e.Assunto, "[Termo assinado] TRE-v1.0") && strings.Contains(e.Assunto, "PAT-0042") {
			rh = true
		}
	}
	if !copia || !rh {
		t.Fatalf("envios: cópia=%v rh=%v", copia, rh)
	}
	// verificação pública
	if pg := paginaOK(t, &cliente{t: t, h: h}, "/verificar/"+cod); !strings.Contains(pg, "Termo válido") {
		t.Fatal("verificar deveria confirmar")
	}
	if w := col.req("GET", fmt.Sprintf("/meus-termos/%d/pdf", tm.ID), nil); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), pdfB) {
		t.Fatal("o colaborador baixa o mesmo PDF assinado")
	}
	paginaOK(t, col, fmt.Sprintf("/meus-termos/%d/assinar", tm.ID))
	paginaOK(t, ti, fmt.Sprintf("/maquinas/%d", maq))

	// Anexo II para todos (lote) e revisão com reassinatura
	semErro(t, ti.req("POST", "/termos/lote", url.Values{"csrf": {csrf}, "tipo": {fmt.Sprint(anexo2.ID)}}), "lote")
	if l := b.Termos(FiltroTermos{AdminID: bia, TipoID: anexo2.ID}); len(l) != 1 || l[0].Situacao != "aguardando" {
		t.Fatalf("lote: %+v", l)
	}
	semErro(t, ti.req("POST", fmt.Sprintf("/admin/termos/modelos/%d/rascunho", entrega.ID), url.Values{"csrf": {csrf}, "abrir": {"1"}}), "abrir rascunho")
	rasc := b.rascunhoDoTipo(entrega.ID)
	semErro(t, ti.req("POST", fmt.Sprintf("/admin/termos/modelos/%d/rascunho", entrega.ID), url.Values{"csrf": {csrf}, "sigla": {"TRE-v1.1"},
		"corpo": {rasc.Corpo + "\nTexto novo da revisão."}, "campos": {rasc.CamposDef}, "aceites": {rasc.AceitesDef}, "exige_reassinatura": {"1"}}), "salvar rascunho")
	if w := ti.req("POST", fmt.Sprintf("/admin/termos/modelos/%d/publicar", entrega.ID), url.Values{"csrf": {csrf}}); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("publicar sem prévia deveria falhar")
	}
	ti.req("GET", fmt.Sprintf("/admin/termos/modelos/%d/previa.pdf", entrega.ID), nil)
	semErro(t, ti.req("POST", fmt.Sprintf("/admin/termos/modelos/%d/publicar", entrega.ID), url.Values{"csrf": {csrf}}), "publicar")
	antigo, _ := b.Termo(tm.ID)
	if antigo.Situacao != "substituido" || antigo.SubstituidoPor == 0 {
		t.Fatalf("revisão com reassinatura: %s", antigo.Situacao)
	}
	novo, _ := b.Termo(antigo.SubstituidoPor)
	if novo.Situacao != "aguardando" || novo.Sigla != "TRE-v1.1" || novo.Valores.Texto["local_uso"] != "" {
		t.Fatalf("termo reemitido: %s %s", novo.Situacao, novo.Sigla)
	}
}
