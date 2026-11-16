package servidor

import (
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func bancoTeste(t *testing.T) *Banco {
	t.Helper()
	b, err := AbrirBanco(filepath.Join(t.TempDir(), "teste.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Fechar() })
	return b
}

func TestCPFeCNPJ(t *testing.T) {
	for _, c := range []string{"52998224725", "11144477735"} {
		if !cpfValido(c) {
			t.Errorf("CPF %s deveria ser válido", c)
		}
	}
	for _, c := range []string{"52998224724", "11111111111", "123", ""} {
		if cpfValido(c) {
			t.Errorf("CPF %s deveria ser inválido", c)
		}
	}
	if !cnpjValido("11222333000181") || cnpjValido("11222333000182") || cnpjValido("00000000000000") {
		t.Error("validação de CNPJ errada")
	}
	if formatarCPF("52998224725") != "529.982.247-25" || formatarCNPJ("11222333000181") != "11.222.333/0001-81" {
		t.Error("formatação errada")
	}
}

func TestDominioAceito(t *testing.T) {
	d := []string{"ocupacional.com.br"}
	casos := map[string]bool{
		"joao@ocupacional.com.br":    true,
		"JOAO@Ocupacional.com.br":    true,
		"joao@gmail.com":             false,
		"joao@rh.ocupacional.com.br": false,
		"joao@ocupacional.com.br.x":  false,
		"@ocupacional.com.br":        false,
	}
	for e, esperado := range casos {
		if dominioAceito(e, d) != esperado {
			t.Errorf("dominioAceito(%q) != %v", e, esperado)
		}
	}
	if got := listaConfig("A.com; b.com,\n a.com "); strings.Join(got, ",") != "a.com,b.com" {
		t.Errorf("listaConfig: %v", got)
	}
}

func TestConvite(t *testing.T) {
	b := bancoTeste(t)
	dom := []string{"ocupacional.com.br"}
	if _, _, _, err := b.CriarConvite("fulano@gmail.com", "", "ti", 7, dom); err == nil {
		t.Fatal("deveria recusar domínio fora da lista")
	}
	id, token, renovado, err := b.CriarConvite("Fulano.Silva@ocupacional.com.br", "Fulano Silva", "ti", 7, dom)
	if err != nil || renovado || token == "" {
		t.Fatalf("criar convite: %v", err)
	}
	// o banco guarda só o hash
	var guardado string
	_ = b.db.QueryRow(`SELECT convite_token_hash FROM admins WHERE id=?`, id).Scan(&guardado)
	if guardado == token || guardado != hashConvite(token) {
		t.Fatal("o token não deveria ficar em claro no banco")
	}
	// sem senha, não entra
	if _, err := b.Entrar("fulano.silva@ocupacional.com.br", ""); err == nil {
		t.Fatal("conta sem senha não pode entrar")
	}
	// renovar invalida o anterior
	_, token2, renovado, err := b.CriarConvite("fulano.silva@ocupacional.com.br", "", "ti", 7, dom)
	if err != nil || !renovado {
		t.Fatalf("renovar: %v", err)
	}
	if _, err := b.ContaDoConvite(token); err == nil {
		t.Fatal("convite antigo deveria ter perdido a validade")
	}
	if _, err := b.AceitarConvite(token2, "curta"); err == nil {
		t.Fatal("senha curta deveria ser recusada")
	}
	if _, err := b.AceitarConvite(token2, "12345678"); err == nil {
		t.Fatal("senha óbvia deveria ser recusada")
	}
	if _, err := b.AceitarConvite(token2, "Ab!senha-forte-1"); err != nil {
		t.Fatalf("aceitar: %v", err)
	}
	if _, err := b.AceitarConvite(token2, "Ab!senha-forte-2"); !errors.Is(err, errConviteInvalido) {
		t.Fatalf("convite de uso único: %v", err)
	}
	if _, err := b.Entrar("FULANO.SILVA@ocupacional.com.br", "Ab!senha-forte-1"); err != nil {
		t.Fatalf("entrar pelo e-mail: %v", err)
	}
	// com senha, não dá para convidar de novo
	if _, _, _, err := b.CriarConvite("fulano.silva@ocupacional.com.br", "", "ti", 7, dom); err == nil {
		t.Fatal("conta com senha não recebe convite")
	}
	// conta desativada não entra e perde as sessões
	sess, _ := b.Entrar("fulano.silva@ocupacional.com.br", "Ab!senha-forte-1")
	if err := b.DesativarConta(id, false); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Entrar("fulano.silva@ocupacional.com.br", "Ab!senha-forte-1"); err == nil {
		t.Fatal("conta desativada não pode entrar")
	}
	if _, err := b.SessaoPorToken(sess.Token); err == nil {
		t.Fatal("sessão de conta desativada deveria cair")
	}
}

func TestConviteExpirado(t *testing.T) {
	b := bancoTeste(t)
	id, token, _, err := b.CriarConvite("ciclano@ocupacional.com.br", "", "ti", 7, []string{"ocupacional.com.br"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = b.db.Exec(`UPDATE admins SET convite_expira=? WHERE id=?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), id)
	if _, err := b.AceitarConvite(token, "Ab!senha-forte-1"); !errors.Is(err, errConviteInvalido) {
		t.Fatalf("convite expirado deveria ser recusado: %v", err)
	}
	f, _ := b.Ficha(id)
	if f.Situacao() != "convite_expirado" {
		t.Fatalf("situação: %s", f.Situacao())
	}
}

func TestFicha(t *testing.T) {
	b := bancoTeste(t)
	emp, err := b.SalvarEmpresa(0, "Ocupacional Ltda", "11.222.333/0001-81", "Matriz\nFilial\nmatriz", true)
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := b.Empresa(emp); len(e.Unidades) != 2 {
		t.Fatalf("unidades sem repetição: %v", e.Unidades)
	}
	if _, err := b.SalvarEmpresa(0, "X", "11.222.333/0001-82", "", true); err == nil {
		t.Fatal("CNPJ inválido deveria ser recusado")
	}
	id, _, _, _ := b.CriarConvite("maria@ocupacional.com.br", "", "ti", 7, []string{"ocupacional.com.br"})
	d := DadosFicha{Nome: "Maria  Souza", CPF: "529.982.247-25", Cargo: "Analista", Setor: "TI", EmpresaID: emp}
	completa, err := b.SalvarFicha(id, d, false)
	if err != nil || completa {
		t.Fatalf("ficha parcial: completa=%v err=%v", completa, err)
	}
	f, _ := b.Ficha(id)
	if f.Nome != "Maria Souza" || f.CPF != "52998224725" {
		t.Fatalf("normalização: %+v", f)
	}
	if err := b.ValidarFicha(id, "", "ti"); err == nil {
		t.Fatal("não pode validar ficha incompleta")
	}
	if _, err := b.SalvarFicha(id, DadosFicha{CPF: "111.111.111-11"}, false); err == nil {
		t.Fatal("CPF inválido deveria ser recusado")
	}
	d.Unidade, d.Gestor, d.Vinculo, d.Regime = "Unidade que não existe", "Chefe", "clt", "hibrido"
	if _, err := b.SalvarFicha(id, d, false); err == nil {
		t.Fatal("unidade fora da lista deveria ser recusada")
	}
	d.Unidade = "Filial"
	if completa, err = b.SalvarFicha(id, d, false); err != nil || !completa {
		t.Fatalf("ficha completa: %v %v", completa, err)
	}
	// login de rede precisa existir no inventário
	if err := b.ValidarFicha(id, `OCP\maria`, "ti"); err == nil {
		t.Fatal("login de rede fora do inventário deveria ser recusado")
	}
	_, _ = b.db.Exec(`INSERT INTO usuarios(login) VALUES('OCP\maria')`)
	if s := b.SugerirLoginRede("maria@ocupacional.com.br"); len(s) != 1 || s[0].Login != `OCP\maria` {
		t.Fatalf("sugestão: %+v", s)
	}
	if err := b.ValidarFicha(id, `ocp\MARIA`, "ti"); err != nil {
		t.Fatal(err)
	}
	f, _ = b.Ficha(id)
	if !f.Validada() || f.LoginRede != `OCP\maria` || f.Situacao() != "convite" {
		t.Fatalf("validação: %+v", f)
	}
	// salvar sem mudar nada mantém a validação; mudar derruba
	if _, err := b.SalvarFicha(id, d, false); err != nil {
		t.Fatal(err)
	}
	if f, _ = b.Ficha(id); !f.Validada() {
		t.Fatal("salvar sem mudança não deveria derrubar a validação")
	}
	d.Cargo = "Coordenadora"
	_, _ = b.SalvarFicha(id, d, false)
	if f, _ = b.Ficha(id); f.Validada() {
		t.Fatal("mudança feita pelo colaborador deveria pedir nova validação")
	}
	// CPF repetido em outra conta
	id2, _, _, _ := b.CriarConvite("joao@ocupacional.com.br", "", "ti", 7, []string{"ocupacional.com.br"})
	if _, err := b.SalvarFicha(id2, DadosFicha{CPF: "52998224725"}, false); err == nil {
		t.Fatal("CPF de outra conta deveria ser recusado")
	}
}

func TestMensagemComAnexo(t *testing.T) {
	pdf := []byte("%PDF-1.4 conteúdo de teste com acentuação ç ã")
	msg := montarMensagem("ti@ocupacional.com.br", []string{"rh@ocupacional.com.br"}, "[Termo assinado] TRE-v1.0 · Nº 0042",
		"texto puro", "<p>html</p>", []AnexoEmail{{Nome: "Termo Nº 0042.pdf", Tipo: "application/pdf", Dados: pdf}})
	m, err := mail.ReadMessage(strings.NewReader(string(msg)))
	if err != nil {
		t.Fatal(err)
	}
	tipo, params, _ := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if tipo != "multipart/mixed" {
		t.Fatalf("tipo: %s", tipo)
	}
	assunto, _ := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if !strings.Contains(assunto, "Nº 0042") {
		t.Fatalf("assunto: %s", assunto)
	}
	mr := multipart.NewReader(m.Body, params["boundary"])
	p1, err := mr.NextPart()
	if err != nil || !strings.HasPrefix(p1.Header.Get("Content-Type"), "multipart/alternative") {
		t.Fatalf("1ª parte: %v %s", err, p1.Header.Get("Content-Type"))
	}
	p2, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if p2.Header.Get("Content-Type") == "" || !strings.Contains(p2.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("anexo: %v", p2.Header)
	}
	bruto, _ := io.ReadAll(p2) // multipart.Part não decodifica base64 sozinho
	dados, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.ReplaceAll(string(bruto), "\r\n", ""), "\n", ""))
	if err != nil || string(dados) != string(pdf) {
		t.Fatalf("anexo corrompido: %v", err)
	}
	if _, err := mr.NextPart(); err != io.EOF {
		t.Fatal("deveria ter só 2 partes")
	}
	// sem anexo continua multipart/alternative (como antes da 1.23)
	m2, _ := mail.ReadMessage(strings.NewReader(string(montarMensagem("a@b.com", []string{"c@d.com"}, "x", "t", "h", nil))))
	if tipo, _, _ := mime.ParseMediaType(m2.Header.Get("Content-Type")); tipo != "multipart/alternative" {
		t.Fatalf("sem anexo: %s", tipo)
	}
}

type destinoFalso struct {
	falhar bool
	envios int
}

func (d *destinoFalso) Nome() string { return "falso" }
func (d *destinoFalso) Enviar(e *EnvioFila) error {
	d.envios++
	if d.falhar {
		return errors.New("535 authentication unsuccessful")
	}
	return nil
}

func TestFila(t *testing.T) {
	b := bancoTeste(t)
	if _, err := b.Enfileirar(EnvioFila{Para: "sem-arroba", Assunto: "x"}); err == nil {
		t.Fatal("destinatário inválido deveria ser recusado")
	}
	id, err := b.Enfileirar(EnvioFila{Destino: "rh", Tipo: "rh", Para: "rh@ocupacional.com.br", Assunto: "a", Texto: "t", HTML: "h", AnexoNome: "x.pdf", Anexo: []byte("pdf")})
	if err != nil {
		t.Fatal(err)
	}
	d := &destinoFalso{falhar: true}
	dest := map[string]DestinoTermo{"rh": d}
	agoraT := time.Now()
	for i := 1; i <= maxTentativasFila; i++ {
		ok, f := b.processarFila(dest, agoraT)
		if ok != 0 || f != 1 {
			t.Fatalf("tentativa %d: ok=%d falhas=%d", i, ok, f)
		}
		// antes de 15 minutos não tenta de novo
		if ok, f := b.processarFila(dest, agoraT); ok+f != 0 {
			t.Fatalf("tentou antes do intervalo")
		}
		agoraT = agoraT.Add(intervaloFila + time.Second)
		_, _ = b.db.Exec(`UPDATE termo_envios SET proxima_em=? WHERE id=? AND situacao='pendente'`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339), id)
	}
	l := b.EnviosFila(10, 0)
	if len(l) != 1 || l[0].Situacao != "erro" || l[0].Tentativas != maxTentativasFila || !strings.Contains(l[0].Erro, "usuário ou senha") {
		t.Fatalf("depois de 5 falhas: %+v", l)
	}
	if b.QtdEnviosComErro() != 1 {
		t.Fatal("contagem de erros")
	}
	// Reenviar zera e o envio sai
	if err := b.ReenviarEnvio(id); err != nil {
		t.Fatal(err)
	}
	d.falhar = false
	if ok, _ := b.processarFila(dest, time.Now()); ok != 1 {
		t.Fatal("reenvio deveria sair")
	}
	if l := b.EnviosFila(10, 0); l[0].Situacao != "enviado" {
		t.Fatalf("situação: %s", l[0].Situacao)
	}
	// destino sem integração (LuzIA) falha com mensagem clara
	_, _ = b.Enfileirar(EnvioFila{Destino: "luzia_api", Para: "", Assunto: "x"})
	if _, f := b.processarFila(dest, time.Now()); f != 1 {
		t.Fatal("destino desconhecido deveria falhar")
	}
}

func TestConviteNaFilaNaoGuardaLink(t *testing.T) {
	b := bancoTeste(t)
	id, token, _, _ := b.CriarConvite("ana@ocupacional.com.br", "", "ti", 7, []string{"ocupacional.com.br"})
	eid, _ := b.Enfileirar(EnvioFila{AdminID: id, Tipo: "convite", Para: "ana@ocupacional.com.br", Assunto: "c", Texto: "link /convite/" + token, HTML: token})
	d := &destinoFalso{}
	b.processarFila(map[string]DestinoTermo{"pessoa": d}, time.Now())
	var texto string
	_ = b.db.QueryRow(`SELECT texto FROM termo_envios WHERE id=?`, eid).Scan(&texto)
	if strings.Contains(texto, token) {
		t.Fatal("o link do convite não deveria ficar guardado depois do envio")
	}
	if err := b.ReenviarEnvio(eid); err == nil {
		t.Fatal("convite sem corpo não deveria ser reenviado")
	}
}

// ---------------- fluxo HTTP ----------------

type cliente struct {
	t      *testing.T
	h      http.Handler
	cookie string
	ip     string // RemoteAddr opcional (ex.: "10.0.0.5:5000")
}

func (c *cliente) req(metodo, caminho string, form url.Values) *httptest.ResponseRecorder {
	var corpo io.Reader
	if form != nil {
		corpo = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(metodo, caminho, corpo)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c.cookie != "" {
		r.AddCookie(&http.Cookie{Name: cookieSessao, Value: c.cookie})
	}
	if c.ip != "" {
		r.RemoteAddr = c.ip
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	for _, ck := range w.Result().Cookies() {
		if ck.Name == cookieSessao {
			c.cookie = ck.Value
		}
	}
	return w
}

func TestFluxoConviteHTTP(t *testing.T) {
	b := bancoTeste(t)
	s, err := Novo(b, "teste")
	if err != nil {
		t.Fatalf("templates: %v", err)
	}
	h := s.Rotas()
	if err := b.CriarAdmin("admin", "senha-admin-1", "Admin", "", "admin"); err != nil {
		t.Fatal(err)
	}
	ti := &cliente{t: t, h: h}
	ti.req("POST", "/login", url.Values{"usuario": {"admin"}, "senha": {"senha-admin-1"}})
	sess, err := b.SessaoPorToken(ti.cookie)
	if err != nil {
		t.Fatal("login do admin falhou")
	}
	csrf := sess.CSRF

	// módulo desligado: /termos manda para a configuração
	if w := ti.req("GET", "/termos", nil); w.Code != http.StatusSeeOther || !strings.HasPrefix(w.Header().Get("Location"), "/admin/termos") {
		t.Fatalf("módulo desligado: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := ti.req("GET", "/admin/termos", nil); w.Code != 200 {
		t.Fatalf("/admin/termos: %d", w.Code)
	}
	// ligar em HTTP sem "permitir HTTP" é recusado
	w := ti.req("POST", "/admin/termos", url.Values{"csrf": {csrf}, "ativo": {"1"}, "dominios": {"ocupacional.com.br"}})
	if !strings.Contains(w.Header().Get("Location"), "erro=") || b.ConfigTermos().Ativo {
		t.Fatal("deveria recusar ligar sem HTTPS")
	}
	ti.req("POST", "/admin/termos", url.Values{"csrf": {csrf}, "ativo": {"1"}, "permitir_http": {"1"}, "dominios": {"ocupacional.com.br"}})
	if c := b.ConfigTermos(); !c.Ativo || !c.PermitirHTTP {
		t.Fatal("deveria ligar com permitir HTTP")
	}
	ti.req("POST", "/admin/termos/empresas", url.Values{"csrf": {csrf}, "razao_social": {"Ocupacional Ltda"}, "cnpj": {"11222333000181"}, "unidades": {"Matriz"}})
	emp := b.Empresas(true)
	if len(emp) != 1 {
		t.Fatal("empresa não cadastrada")
	}
	for _, pg := range []string{"/termos", "/admin/colaboradores", "/admin/termos", "/minha-ficha"} {
		if w := ti.req("GET", pg, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "modo de teste") {
			t.Fatalf("%s: %d (faixa HTTP ausente?)", pg, w.Code)
		}
	}
	// convite: domínio errado é recusado, certo mostra o link
	if w := ti.req("POST", "/admin/colaboradores/convidar", url.Values{"csrf": {csrf}, "email": {"x@gmail.com"}}); !strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatal("domínio fora da lista deveria ser recusado")
	}
	w = ti.req("POST", "/admin/colaboradores/convidar", url.Values{"csrf": {csrf}, "email": {"bia@ocupacional.com.br"}, "nome": {"Bia"}})
	link := regexp.MustCompile(`/convite/[0-9a-f]{64}`).FindString(w.Body.String())
	if w.Code != 200 || link == "" {
		t.Fatalf("convite: %d, link não exibido", w.Code)
	}

	// colaborador: abre o link, cria a senha, cai em Minha ficha
	col := &cliente{t: t, h: h}
	if w := col.req("GET", link, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "bia@ocupacional.com.br") {
		t.Fatalf("página do convite: %d", w.Code)
	}
	w = col.req("POST", link, url.Values{"senha": {"Bia-senha-forte"}, "confirma": {"Bia-senha-forte"}})
	if w.Header().Get("Location") == "" || !strings.HasPrefix(w.Header().Get("Location"), "/minha-ficha") || col.cookie == "" {
		t.Fatalf("aceitar convite: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := col.req("GET", link, nil); !strings.Contains(w.Body.String(), "não vale mais") {
		t.Fatal("o link deveria ser de uso único")
	}
	// perfil Usuário com ficha incompleta: "/" leva para Minha ficha
	if w := col.req("GET", "/", nil); w.Header().Get("Location") != "/minha-ficha" {
		t.Fatalf("redirecionamento: %s", w.Header().Get("Location"))
	}
	// não acessa telas da T.I
	if w := col.req("GET", "/admin/colaboradores", nil); w.Code != http.StatusSeeOther || strings.HasPrefix(w.Header().Get("Location"), "/admin/colaboradores") {
		t.Fatal("colaborador não pode ver a lista")
	}
	cs, _ := b.SessaoPorToken(col.cookie)
	w = col.req("POST", "/minha-ficha", url.Values{"csrf": {cs.CSRF}, "nome": {"Beatriz Lima"}, "cpf": {"529.982.247-25"}, "cargo": {"Analista"},
		"setor": {"TI"}, "empresa_id": {itoa(emp[0].ID)}, "unidade": {"Matriz"}, "gestor": {"Chefe"}, "vinculo": {"clt"}, "regime": {"presencial"}})
	if strings.Contains(w.Header().Get("Location"), "erro=") {
		t.Fatalf("salvar ficha: %s", w.Header().Get("Location"))
	}
	if w := col.req("GET", "/", nil); w.Header().Get("Location") != "/chamados" {
		t.Fatalf("com a ficha completa deveria ir aos chamados: %s", w.Header().Get("Location"))
	}
	f, _ := b.Ficha(cs.Admin.ID)
	if f.Situacao() != "a_validar" {
		t.Fatalf("situação: %s", f.Situacao())
	}
	// T.I valida
	if w := ti.req("GET", "/admin/colaboradores/"+itoa(f.AdminID), nil); w.Code != 200 {
		t.Fatalf("ficha: %d", w.Code)
	}
	w = ti.req("POST", "/admin/colaboradores/"+itoa(f.AdminID)+"/ficha", url.Values{"csrf": {csrf}, "acao": {"validar"}, "nome": {"Beatriz Lima"}, "cpf": {"52998224725"},
		"cargo": {"Analista"}, "setor": {"TI"}, "empresa_id": {itoa(emp[0].ID)}, "unidade": {"Matriz"}, "gestor": {"Chefe"}, "vinculo": {"clt"}, "regime": {"presencial"}})
	if f, _ = b.Ficha(f.AdminID); !f.Validada() {
		t.Fatalf("validar: %s", w.Header().Get("Location"))
	}
	// desligar o módulo esconde tudo e mantém os dados
	ti.req("POST", "/admin/termos", url.Values{"csrf": {csrf}, "dominios": {"ocupacional.com.br"}})
	if w := col.req("GET", "/minha-ficha", nil); w.Code != http.StatusSeeOther {
		t.Fatal("com o módulo desligado, Minha ficha não deveria abrir")
	}
	if f, _ = b.Ficha(f.AdminID); !f.Validada() {
		t.Fatal("desligar não pode apagar dados")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
