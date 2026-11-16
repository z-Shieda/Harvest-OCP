package servidor

// Módulo Termos (1.23) — telas e ações da fase 1 (Base):
//   /termos                      painel (fichas a validar, convites, envios com erro)
//   /admin/termos                configuração do módulo, empresas e unidades, fila de e-mails
//   /admin/colaboradores         convidar, ver e validar fichas, desativar conta
//   /convite/{token}             definir a senha (pública, uso único)
//   /minha-ficha                 o colaborador completa a ficha
// Especificação: docs/SDD-1.23-TERMOS.md.

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
)

// conexaoSegura: HTTPS direto ou atrás de um proxy que termina o TLS (Railway, nginx).
func conexaoSegura(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

var errTermosDesligado = errors.New("o módulo Termos está desligado (Administração → Termos)")
var errTermosSemHTTPS = errors.New("o módulo Termos exige HTTPS: abra o painel por https:// (ou, só para testes, marque \"permitir HTTP\" em Administração → Termos)")

// termosLiberado diz se as telas do módulo podem ser usadas nesta requisição.
func (s *Servidor) termosLiberado(r *http.Request) error {
	c := s.b.ConfigTermos()
	if !c.Ativo {
		return errTermosDesligado
	}
	if !conexaoSegura(r) && !c.PermitirHTTP {
		return errTermosSemHTTPS
	}
	return nil
}

// comTermos protege uma tela do módulo (logado).
func (s *Servidor) comTermos(h func(http.ResponseWriter, *http.Request, *Sessao)) func(http.ResponseWriter, *http.Request, *Sessao) {
	return func(w http.ResponseWriter, r *http.Request, sess *Sessao) {
		if err := s.termosLiberado(r); err != nil {
			destino := "/"
			if sess.Admin.EhAdmin() {
				destino = "/admin/termos"
			}
			redirecionar(w, r, destino, "", err)
			return
		}
		h(w, r, sess)
	}
}

// fichaPendente: o colaborador logado ainda precisa completar a ficha (para o redirecionamento
// do perfil Usuário ao entrar).
func (s *Servidor) fichaPendente(r *http.Request, sess *Sessao) bool {
	if s.termosLiberado(r) != nil {
		return false
	}
	f, err := s.b.Ficha(sess.Admin.ID)
	return err == nil && !f.Preenchida()
}

// ---------------- configuração /admin/termos ----------------

func (s *Servidor) paginaConfigTermos(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	c := s.b.ConfigTermos()
	s.render(w, r, "termos_config", "Configurar termos", "termos_config", sess, map[string]any{
		"Config":   c,
		"Dominios": strings.Join(c.Dominios, ", "),
		"EmailsRH": strings.Join(c.EmailsRH, "\n"),
		"Seguro":   conexaoSegura(r),
		"Empresas": s.b.Empresas(false),
		"Envios":   s.b.EnviosFila(40, 0),
		"SMTPOK":   s.b.ConfigSMTP().Configurado(),
		"Liberado": s.termosLiberado(r) == nil,
	})
}

func (s *Servidor) acaoConfigTermos(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	antes := s.b.ConfigTermos()
	c := ConfigTermos{
		Ativo:        r.FormValue("ativo") == "1",
		PermitirHTTP: r.FormValue("permitir_http") == "1",
		Dominios:     listaConfig(r.FormValue("dominios")),
		EmailsRH:     listaConfig(r.FormValue("emails_rh")),
		ConviteDias:  numeroConfig(r.FormValue("convite_dias"), 7, 1, 30),
		LembreteDias: numeroConfig(r.FormValue("lembrete_dias"), 3, 0, 30),
		AtencaoDias:  numeroConfig(r.FormValue("atencao_dias"), 7, 1, 90),
	}
	if c.Ativo && !conexaoSegura(r) && !c.PermitirHTTP {
		redirecionar(w, r, "/admin/termos", "", errors.New("o painel está em HTTP: senha e código do aceite trafegariam sem criptografia. Use HTTPS ou, só para testes, marque \"permitir HTTP\""))
		return
	}
	if err := s.b.SalvarConfigTermos(c); err != nil {
		redirecionar(w, r, "/admin/termos", "", err)
		return
	}
	if antes.Ativo != c.Ativo {
		estado := "desligado"
		if c.Ativo {
			estado = "ligado"
		}
		s.b.Auditar(sess.Admin.Usuario, "módulo termos", estado)
	}
	if antes.PermitirHTTP != c.PermitirHTTP {
		s.b.Auditar(sess.Admin.Usuario, "termos: permitir HTTP (testes)", fmt.Sprint(c.PermitirHTTP))
	}
	s.b.Auditar(sess.Admin.Usuario, "configurar termos", fmt.Sprintf("domínios=%s; RH=%s; convite=%dd; lembrete=%dd",
		strings.Join(c.Dominios, ","), strings.Join(c.EmailsRH, ","), c.ConviteDias, c.LembreteDias))
	redirecionar(w, r, "/admin/termos", "Configuração dos termos salva.", nil)
}

func (s *Servidor) acaoSalvarEmpresa(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := parseID(r.FormValue("id"))
	novoID, err := s.b.SalvarEmpresa(id, r.FormValue("razao_social"), r.FormValue("cnpj"), r.FormValue("unidades"), r.FormValue("ativa") == "1" || id == 0)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "termos: salvar empresa", fmt.Sprintf("#%d %s", novoID, strings.TrimSpace(r.FormValue("razao_social"))))
	}
	redirecionar(w, r, "/admin/termos#empresas", "Empresa salva.", err)
}

func (s *Servidor) acaoExcluirEmpresa(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	desativada, err := s.b.ExcluirEmpresa(id)
	msg := "Empresa excluída."
	if desativada {
		msg = "A empresa tem pessoas vinculadas: ela foi desativada (some das listas, mas as fichas continuam)."
	}
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "termos: excluir empresa", fmt.Sprintf("#%d (desativada=%v)", id, desativada))
	}
	redirecionar(w, r, "/admin/termos#empresas", msg, err)
}

func (s *Servidor) acaoReenviarEnvio(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	id := idDaURL(r)
	err := s.b.ReenviarEnvio(id)
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "termos: reenviar e-mail", fmt.Sprint("#", id))
		select {
		case s.fila.cutucar <- struct{}{}:
		default:
		}
	}
	voltar := r.FormValue("voltar")
	if !strings.HasPrefix(voltar, "/") || strings.HasPrefix(voltar, "//") {
		voltar = "/admin/termos#envios"
	}
	redirecionar(w, r, voltar, "E-mail colocado de novo na fila.", err)
}

// ---------------- colaboradores ----------------

// podeGerirConta: técnico mexe só em contas de perfil Usuário; admin em todas.
func podeGerirConta(quem Admin, alvo *Ficha) bool {
	return quem.EhAdmin() || alvo.Papel == "usuario"
}

func (s *Servidor) paginaColaboradores(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	filtro := r.URL.Query().Get("filtro")
	busca := r.URL.Query().Get("q")
	s.render(w, r, "colaboradores", "Colaboradores", "termos_colab", sess, map[string]any{
		"Lista": s.b.Colaboradores(busca, filtro), "Filtro": filtro, "Busca": busca,
		"Contagem": s.b.ContagemColaboradores(), "Dominios": strings.Join(s.b.ConfigTermos().Dominios, ", "),
	})
}

func (s *Servidor) acaoConvidar(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	c := s.b.ConfigTermos()
	id, token, renovado, err := s.b.CriarConvite(r.FormValue("email"), r.FormValue("nome"), sess.Admin.Usuario, c.ConviteDias, c.Dominios)
	if err != nil {
		if id > 0 {
			redirecionar(w, r, fmt.Sprintf("/admin/colaboradores/%d", id), "", err)
			return
		}
		redirecionar(w, r, "/admin/colaboradores", "", err)
		return
	}
	acao := "convidar colaborador"
	if renovado {
		acao = "renovar convite"
	}
	s.b.Auditar(sess.Admin.Usuario, "termos: "+acao, strings.ToLower(strings.TrimSpace(r.FormValue("email"))))
	link := s.baseURL(r) + "/convite/" + token
	errEmail := s.enviarConvite(id, link, sess.Admin, c.ConviteDias)
	// o link aparece na tela uma única vez (o banco guarda só o hash): a T.I pode mandar por
	// outro canal (Teams) se o e-mail não chegar.
	s.renderColaborador(w, r, sess, id, map[string]any{"LinkConvite": link, "ConviteRenovado": renovado, "ErroEmailConvite": errEmail})
}

func (s *Servidor) acaoRenovarConvite(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	f, err := s.b.Ficha(idDaURL(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !podeGerirConta(sess.Admin, f) {
		redirecionar(w, r, "/admin/colaboradores", "", errors.New("só um administrador mexe nessa conta"))
		return
	}
	if r.FormValue("cancelar") == "1" {
		err := s.b.CancelarConvite(f.AdminID)
		if err == nil {
			s.b.Auditar(sess.Admin.Usuario, "termos: cancelar convite", f.Email)
		}
		redirecionar(w, r, fmt.Sprintf("/admin/colaboradores/%d", f.AdminID), "Convite cancelado: o link deixou de valer.", err)
		return
	}
	c := s.b.ConfigTermos()
	email := f.Email
	if email == "" {
		email = f.Usuario
	}
	id, token, _, err := s.b.CriarConvite(email, "", sess.Admin.Usuario, c.ConviteDias, c.Dominios)
	if err != nil {
		redirecionar(w, r, fmt.Sprintf("/admin/colaboradores/%d", f.AdminID), "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "termos: renovar convite", email)
	link := s.baseURL(r) + "/convite/" + token
	errEmail := s.enviarConvite(id, link, sess.Admin, c.ConviteDias)
	s.renderColaborador(w, r, sess, id, map[string]any{"LinkConvite": link, "ConviteRenovado": true, "ErroEmailConvite": errEmail})
}

func (s *Servidor) paginaColaborador(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	s.renderColaborador(w, r, sess, idDaURL(r), nil)
}

func (s *Servidor) renderColaborador(w http.ResponseWriter, r *http.Request, sess *Sessao, id int64, extra map[string]any) {
	f, err := s.b.Ficha(id)
	if err != nil {
		redirecionar(w, r, "/admin/colaboradores", "", errors.New("conta não encontrada"))
		return
	}
	var unidades []string
	if f.EmpresaID > 0 {
		if e, err := s.b.Empresa(f.EmpresaID); err == nil {
			unidades = e.Unidades
		}
	}
	email := f.Email
	if email == "" {
		email = f.Usuario
	}
	d := map[string]any{
		"F": f, "Faltando": f.Faltando(unidades), "Empresas": s.b.Empresas(true),
		"Vinculos": opcoesVinculo(), "Regimes": opcoesRegime(),
		"Sugestoes": s.b.SugerirLoginRede(email), "Envios": s.b.EnviosFila(20, f.AdminID),
		"PodeGerir": podeGerirConta(sess.Admin, f), "EhEu": f.AdminID == sess.Admin.ID,
		// 1.24: perfil personalizado e setores de gestor (só o admin altera)
		"Perfis": s.b.Perfis(), "PerfilAtual": s.perfilIDDe(f.AdminID), "SetoresGestor": s.b.SetoresGestor(f.AdminID),
		"SetoresFixos": s.b.SetoresFixos(), "EhAdmin": sess.Admin.EhAdmin(),
	}
	for k, v := range extra {
		d[k] = v
	}
	s.render(w, r, "colaborador", f.NomeExibicao(), "termos_colab", sess, d)
}

func dadosFichaDoForm(r *http.Request) DadosFicha {
	return DadosFicha{
		Nome: r.FormValue("nome"), CPF: r.FormValue("cpf"), Matricula: r.FormValue("matricula"), Cargo: r.FormValue("cargo"),
		Setor: r.FormValue("setor"), Unidade: r.FormValue("unidade"), Gestor: r.FormValue("gestor"), Vinculo: r.FormValue("vinculo"),
		Regime: r.FormValue("regime"), Telefone: r.FormValue("telefone"), EmpresaID: parseID(r.FormValue("empresa_id")),
	}
}

// acaoFichaTI: a T.I corrige a ficha e, com o botão "Salvar e validar", valida (D10) e confirma o login de rede (D15).
func (s *Servidor) acaoFichaTI(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	f, err := s.b.Ficha(idDaURL(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	voltar := fmt.Sprintf("/admin/colaboradores/%d", f.AdminID)
	if !podeGerirConta(sess.Admin, f) {
		redirecionar(w, r, voltar, "", errors.New("só um administrador mexe nessa conta"))
		return
	}
	switch r.FormValue("acao") {
	case "revogar":
		err := s.b.RevogarValidacao(f.AdminID)
		if err == nil {
			s.b.Auditar(sess.Admin.Usuario, "termos: revogar validação da ficha", f.Usuario)
		}
		redirecionar(w, r, voltar, "Validação revogada: a ficha volta para \"a validar\".", err)
		return
	}
	if _, err := s.b.SalvarFicha(f.AdminID, dadosFichaDoForm(r), true); err != nil {
		redirecionar(w, r, voltar, "", err)
		return
	}
	loginRede := strings.TrimSpace(r.FormValue("login_rede"))
	if r.FormValue("acao") == "validar" {
		if err := s.b.ValidarFicha(f.AdminID, loginRede, sess.Admin.Usuario); err != nil {
			redirecionar(w, r, voltar, "", err)
			return
		}
		s.b.Auditar(sess.Admin.Usuario, "termos: validar ficha", f.Usuario+" (login de rede: "+vazioTraco(loginRede)+")")
		redirecionar(w, r, voltar, "Ficha validada.", nil)
		return
	}
	if err := s.b.DefinirLoginRede(f.AdminID, loginRede); err != nil {
		redirecionar(w, r, voltar, "Os dados da ficha foram salvos, mas o login de rede não.", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "termos: editar ficha", f.Usuario)
	redirecionar(w, r, voltar, "Ficha salva.", nil)
}

func (s *Servidor) acaoAtivoColaborador(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	f, err := s.b.Ficha(idDaURL(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	voltar := fmt.Sprintf("/admin/colaboradores/%d", f.AdminID)
	if !podeGerirConta(sess.Admin, f) || f.AdminID == sess.Admin.ID {
		redirecionar(w, r, voltar, "", errors.New("você não pode desativar essa conta"))
		return
	}
	ativo := r.FormValue("ativo") == "1"
	err = s.b.DesativarConta(f.AdminID, ativo)
	msg := "Conta desativada: a pessoa não entra mais, e nada foi apagado."
	acao := "desativar conta"
	if ativo {
		msg, acao = "Conta reativada.", "reativar conta"
	}
	if err == nil {
		s.b.Auditar(sess.Admin.Usuario, "termos: "+acao, f.Usuario)
	}
	redirecionar(w, r, voltar, msg, err)
}

type opcao struct{ Valor, Rotulo string }

func opcoesVinculo() []opcao {
	var l []opcao
	for _, v := range vinculosFicha {
		l = append(l, opcao{v, rotuloVinculo(v)})
	}
	return l
}

func opcoesRegime() []opcao {
	var l []opcao
	for _, v := range regimesFicha {
		l = append(l, opcao{v, rotuloRegime(v)})
	}
	return l
}

// ---------------- convite (público) ----------------

func (s *Servidor) paginaConvite(w http.ResponseWriter, r *http.Request) {
	if err := s.termosLiberado(r); err != nil {
		s.render(w, r, "convite", "Convite", "", nil, map[string]any{"Invalido": err.Error()})
		return
	}
	f, err := s.b.ContaDoConvite(r.PathValue("token"))
	if err != nil {
		s.render(w, r, "convite", "Convite", "", nil, map[string]any{"Invalido": err.Error()})
		return
	}
	s.render(w, r, "convite", "Criar sua senha", "", nil, map[string]any{"F": f, "Token": r.PathValue("token")})
}

func (s *Servidor) acaoConvite(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	voltar := "/convite/" + token
	if err := s.termosLiberado(r); err != nil {
		redirecionar(w, r, voltar, "", err)
		return
	}
	ip := ipCliente(r)
	if s.lim.bloqueado(ip) {
		redirecionar(w, r, voltar, "", errors.New("muitas tentativas; aguarde 10 minutos"))
		return
	}
	if r.FormValue("senha") != r.FormValue("confirma") {
		redirecionar(w, r, voltar, "", errors.New("a confirmação não confere com a senha"))
		return
	}
	id, err := s.b.AceitarConvite(token, r.FormValue("senha"))
	if err != nil {
		if errors.Is(err, errConviteInvalido) {
			s.lim.registrarFalha(ip)
		}
		redirecionar(w, r, voltar, "", err)
		return
	}
	sess, err := s.b.SessaoPara(id)
	if err != nil {
		redirecionar(w, r, "/login", "Senha criada. Entre com o seu e-mail e a senha.", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieSessao, Value: sess.Token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: conexaoSegura(r), MaxAge: 12 * 3600})
	s.b.Auditar(sess.Admin.Usuario, "termos: senha criada pelo convite", ip)
	redirecionar(w, r, "/minha-ficha", "Senha criada. Agora complete a sua ficha.", nil)
}

// ---------------- Minha ficha ----------------

func (s *Servidor) paginaMinhaFicha(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	f, err := s.b.Ficha(sess.Admin.ID)
	if err != nil {
		http.Error(w, "conta não encontrada", http.StatusNotFound)
		return
	}
	var unidades []string
	if f.EmpresaID > 0 {
		if e, err := s.b.Empresa(f.EmpresaID); err == nil {
			unidades = e.Unidades
		}
	}
	s.render(w, r, "minha_ficha", "Minha ficha", "minha_ficha", sess, map[string]any{
		"F": f, "Faltando": f.Faltando(unidades), "Empresas": s.b.Empresas(true),
		"Vinculos": opcoesVinculo(), "Regimes": opcoesRegime(),
	})
}

func (s *Servidor) acaoMinhaFicha(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	antes, _ := s.b.Ficha(sess.Admin.ID)
	completa, err := s.b.SalvarFicha(sess.Admin.ID, dadosFichaDoForm(r), false)
	if err != nil {
		redirecionar(w, r, "/minha-ficha", "", err)
		return
	}
	s.b.Auditar(sess.Admin.Usuario, "termos: ficha preenchida pelo colaborador", fmt.Sprintf("completa=%v", completa))
	msg := "Ficha salva. Ainda faltam alguns campos."
	if completa {
		msg = "Ficha completa. A T.I vai conferir os dados antes de emitir os seus termos."
		if antes != nil && antes.Validada() {
			if depois, _ := s.b.Ficha(sess.Admin.ID); depois != nil && !depois.Validada() {
				msg = "Ficha atualizada. Como os dados mudaram, a T.I vai conferir de novo."
			}
		}
	}
	redirecionar(w, r, "/minha-ficha", msg, nil)
}

// ---------------- e-mails ----------------

// emailSimples monta um e-mail no visual da marca (cabeçalho colorido, parágrafos e um botão).
func (s *Servidor) emailSimples(titulo string, paragrafos []string, botao, link, rodape string) (texto, corpo string) {
	mc := s.marca()
	var t, h strings.Builder
	t.WriteString(titulo + "\n\n")
	for _, p := range paragrafos {
		t.WriteString(p + "\n\n")
		fmt.Fprintf(&h, `<p style="margin:0 0 14px;font-size:15px;line-height:1.5">%s</p>`, html.EscapeString(p))
	}
	if link != "" {
		fmt.Fprintf(&t, "%s: %s\n", botao, link)
	}
	if rodape != "" {
		t.WriteString("\n" + rodape + "\n")
	}
	botaoHTML := ""
	if link != "" {
		botaoHTML = fmt.Sprintf(`<p style="margin:22px 0 8px"><a href="%s" style="background:%s;color:%s;text-decoration:none;font-weight:700;padding:12px 22px;border-radius:999px;display:inline-block">%s</a></p>
<p style="margin:0;font-size:12px;color:#5A6F73;word-break:break-all">Se o botão não abrir, copie o endereço: %s</p>`,
			html.EscapeString(link), mc.CorPri, textoSobre(hexParaRGB(mc.CorPri)), html.EscapeString(botao), html.EscapeString(link))
	}
	corpo = fmt.Sprintf(`<!doctype html><html><body style="margin:0;background:#EEF3F2;font-family:Segoe UI,Arial,sans-serif;color:#1d2b2f">
<div style="max-width:600px;margin:0 auto;padding:24px">
<div style="background:%s;color:%s;border-radius:14px 14px 0 0;padding:16px 22px;font-weight:700">%s · Termos</div>
<div style="background:#fff;border-radius:0 0 14px 14px;padding:22px">
<h2 style="margin:0 0 16px;font-size:20px">%s</h2>%s%s
</div><p style="color:#8A9A9D;font-size:12px;text-align:center">%s</p></div></body></html>`,
		mc.CorSec, textoSobre(hexParaRGB(mc.CorSec)), html.EscapeString(mc.Nome), html.EscapeString(titulo), h.String(), botaoHTML,
		html.EscapeString(vazioPadrao(rodape, "Aviso automático do sistema interno da T.I.")))
	return t.String(), corpo
}

func vazioPadrao(s, padrao string) string {
	if strings.TrimSpace(s) == "" {
		return padrao
	}
	return s
}

// enviarConvite coloca o e-mail de convite na fila. Devolve erro só se nem deu para enfileirar
// (ex.: SMTP não configurado) — a tela então destaca o link para mandar por outro canal.
func (s *Servidor) enviarConvite(adminID int64, link string, por Admin, dias int) error {
	f, err := s.b.Ficha(adminID)
	if err != nil {
		return err
	}
	if !s.b.ConfigSMTP().Configurado() {
		return errors.New("o e-mail (SMTP) não está configurado em Administração → E-mail")
	}
	quem := por.Nome
	if quem == "" {
		quem = por.Usuario
	}
	saudacao := "Olá,"
	if f.Nome != "" {
		saudacao = "Olá, " + strings.Fields(f.Nome)[0] + ","
	}
	texto, corpo := s.emailSimples("Sua conta no "+s.marca().Nome,
		[]string{
			saudacao,
			quem + " (T.I) criou a sua conta. Nela você vai consultar e assinar os seus termos de responsabilidade (equipamentos e uso dos sistemas).",
			"Clique no botão para criar a sua senha. Depois, complete a sua ficha com os dados que o RH precisa.",
			"O link é pessoal, vale por " + strconv.Itoa(dias) + " dias e só pode ser usado uma vez. O seu usuário é o seu e-mail: " + vazioPadrao(f.Email, f.Usuario) + ".",
		}, "Criar minha senha", link, "Se você não esperava este e-mail, avise a T.I.")
	_, err = s.enfileirar(EnvioFila{AdminID: adminID, Destino: "pessoa", Tipo: "convite", Para: vazioPadrao(f.Email, f.Usuario),
		Assunto: "Crie sua senha — " + s.marca().Nome, Texto: texto, HTML: corpo})
	return err
}

func (s *Servidor) perfilIDDe(adminID int64) int64 {
	var id int64
	_ = s.b.db.QueryRow(`SELECT COALESCE(perfil_id,0) FROM admins WHERE id=?`, adminID).Scan(&id)
	return id
}
