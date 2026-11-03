package servidor

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"log"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// ----------------------------------------------------------------------------
// E-mail de chamados (1.10), por SMTP do provedor da empresa (ex.: Microsoft 365:
// smtp.office365.com, porta 587, STARTTLS; Gmail: smtp.gmail.com, 587).
//
//   - "e-mail ao responsável": quando um chamado é atribuído a alguém (por outra pessoa ou pelo
//     responsável automático), essa pessoa recebe um e-mail — se tiver e-mail no cadastro.
//   - "e-mail para a equipe": todo chamado novo vai para admins e técnicos com e-mail.
//   - "responsável automático": chamados novos sem responsável já nascem atribuídos a ele.
//
// A senha do SMTP fica no banco (pasta com acesso só de SYSTEM/Administradores).
// ----------------------------------------------------------------------------

type ConfigSMTP struct {
	Host, Usuario, Senha, De, Seguranca string // Seguranca: starttls | tls | nenhuma
	Porta                               int
}

func (b *Banco) ConfigSMTP() ConfigSMTP {
	c := ConfigSMTP{
		Host: strings.TrimSpace(b.Config("smtp_host")), Usuario: strings.TrimSpace(b.Config("smtp_usuario")),
		Senha: b.Config("smtp_senha"), De: strings.TrimSpace(b.Config("smtp_de")), Seguranca: b.Config("smtp_seguranca"),
	}
	c.Porta, _ = strconv.Atoi(b.Config("smtp_porta"))
	if c.Porta == 0 {
		c.Porta = 587
	}
	if c.Seguranca == "" {
		c.Seguranca = "starttls"
	}
	if c.De == "" {
		c.De = c.Usuario
	}
	return c
}

func (c ConfigSMTP) Configurado() bool { return c.Host != "" && c.De != "" }

// loginAuth: mecanismo AUTH LOGIN (o Microsoft 365 aceita LOGIN, não PLAIN).
type loginAuth struct{ usuario, senha string }

func (a loginAuth) Start(srv *smtp.ServerInfo) (string, []byte, error) {
	if !srv.TLS {
		return "", nil, errors.New("o servidor de e-mail exige conexão segura (use STARTTLS ou TLS)")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(desafio []byte, mais bool) ([]byte, error) {
	if !mais {
		return nil, nil
	}
	p := strings.ToLower(strings.TrimSpace(string(desafio)))
	switch {
	case strings.Contains(p, "user"):
		return []byte(a.usuario), nil
	case strings.Contains(p, "pass"):
		return []byte(a.senha), nil
	}
	return nil, fmt.Errorf("pergunta inesperada do servidor de e-mail: %q", desafio)
}

// enviarEmail manda um e-mail HTML (com versão em texto) para os destinatários.
func (b *Banco) enviarEmail(para []string, assunto, textoPuro, corpoHTML string) error {
	c := b.ConfigSMTP()
	if !c.Configurado() {
		return errors.New("e-mail não configurado (Administração → E-mail)")
	}
	var dest []string
	for _, p := range para {
		if p = strings.TrimSpace(p); p != "" && strings.Contains(p, "@") && !strings.ContainsAny(p, "\r\n<>,;") {
			dest = append(dest, p)
		}
	}
	if len(dest) == 0 {
		return errors.New("nenhum destinatário com e-mail válido")
	}
	if strings.ContainsAny(c.De, "\r\n") {
		return errors.New("remetente inválido")
	}

	// mensagem multipart/alternative em UTF-8
	limite := "limite-" + aleatorio(8)
	var msg bytes.Buffer
	cab := func(k, v string) { fmt.Fprintf(&msg, "%s: %s\r\n", k, v) }
	cab("From", c.De)
	cab("To", strings.Join(dest, ", "))
	cab("Subject", mime.QEncoding.Encode("utf-8", assunto))
	cab("Date", time.Now().Format(time.RFC1123Z))
	cab("Message-ID", "<"+aleatorio(12)+"@inventario>")
	cab("MIME-Version", "1.0")
	cab("Content-Type", `multipart/alternative; boundary="`+limite+`"`)
	msg.WriteString("\r\n")
	parte := func(tipo, corpo string) {
		fmt.Fprintf(&msg, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", limite, tipo)
		qp := quotedprintable.NewWriter(&msg)
		_, _ = qp.Write([]byte(corpo))
		_ = qp.Close()
		msg.WriteString("\r\n")
	}
	parte("text/plain", textoPuro)
	parte("text/html", corpoHTML)
	fmt.Fprintf(&msg, "--%s--\r\n", limite)

	endereco := net.JoinHostPort(c.Host, strconv.Itoa(c.Porta))
	tlsCfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	var cli *smtp.Client
	var err error
	if c.Seguranca == "tls" {
		con, e := tls.DialWithDialer(&net.Dialer{Timeout: 20 * time.Second}, "tcp", endereco, tlsCfg)
		if e != nil {
			return fmt.Errorf("conectar em %s: %w", endereco, e)
		}
		_ = con.SetDeadline(time.Now().Add(30 * time.Second))
		cli, err = smtp.NewClient(con, c.Host)
	} else {
		con, e := net.DialTimeout("tcp", endereco, 20*time.Second)
		if e != nil {
			return fmt.Errorf("conectar em %s: %w", endereco, e)
		}
		_ = con.SetDeadline(time.Now().Add(30 * time.Second)) // nada fica pendurado
		cli, err = smtp.NewClient(con, c.Host)
	}
	if err != nil {
		return err
	}
	defer cli.Close()
	if err := cli.Hello("inventario"); err != nil {
		return err
	}
	if c.Seguranca == "starttls" {
		if ok, _ := cli.Extension("STARTTLS"); !ok {
			return errors.New("o servidor de e-mail não oferece STARTTLS (tente a opção TLS na porta 465)")
		}
		if err := cli.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if c.Usuario != "" {
		ok, mecanismos := cli.Extension("AUTH")
		if !ok {
			return errors.New("o servidor de e-mail não pede autenticação; deixe usuário e senha em branco")
		}
		var auth smtp.Auth
		if strings.Contains(strings.ToUpper(mecanismos), "LOGIN") {
			auth = loginAuth{c.Usuario, c.Senha}
		} else {
			auth = smtp.PlainAuth("", c.Usuario, c.Senha, c.Host)
		}
		if err := cli.Auth(auth); err != nil {
			return fmt.Errorf("login no servidor de e-mail recusado: %w", err)
		}
	}
	remetente := c.De
	if i := strings.LastIndex(remetente, "<"); i >= 0 {
		remetente = strings.Trim(remetente[i:], "<> ")
	}
	if err := cli.Mail(remetente); err != nil {
		return err
	}
	for _, d := range dest {
		if err := cli.Rcpt(d); err != nil {
			return fmt.Errorf("destinatário %s recusado: %w", d, err)
		}
	}
	wr, err := cli.Data()
	if err != nil {
		return err
	}
	if _, err := wr.Write(msg.Bytes()); err != nil {
		return err
	}
	if err := wr.Close(); err != nil {
		return err
	}
	return cli.Quit()
}

// ---------------- conteúdo e gatilhos ----------------

func (s *Servidor) emailChamado(para []string, titulo string, c *Chamado, link, motivo string) {
	if len(para) == 0 || c == nil {
		return
	}
	assunto := fmt.Sprintf("[Chamado #%d] %s", c.ID, c.Titulo)
	if titulo != "" {
		assunto = titulo + " — " + assunto
	}
	linhas := [][2]string{
		{"Solicitante", c.Solicitante() + prefixo(" · ", c.SolicitanteSetor)},
		{"Prioridade", c.RotuloPrioridade()},
		{"Categoria", c.Categoria},
		{"Máquina", vazioTraco(c.MaquinaHostname)},
		{"AnyDesk", vazioTraco(formatarAnyDesk(c.AnyDesk()))},
		{"Prazo", vazioTraco(formatarData(c.Prazo))},
		{"Responsável", vazioTraco(c.AtribuidoNome)},
	}
	var txt, tab strings.Builder
	fmt.Fprintf(&txt, "%s\n\n#%d — %s\n\n", motivo, c.ID, c.Titulo)
	for _, l := range linhas {
		fmt.Fprintf(&txt, "%s: %s\n", l[0], l[1])
		fmt.Fprintf(&tab, `<tr><td style="padding:4px 14px 4px 0;color:#5A6F73">%s</td><td style="padding:4px 0">%s</td></tr>`, html.EscapeString(l[0]), html.EscapeString(l[1]))
	}
	desc := strings.TrimSpace(c.Descricao)
	if len([]rune(desc)) > 1500 {
		desc = string([]rune(desc)[:1500]) + "…"
	}
	fmt.Fprintf(&txt, "\n%s\n\nAbrir o chamado: %s\n", desc, link)
	corpo := fmt.Sprintf(`<!doctype html><html><body style="margin:0;background:#EEF3F2;font-family:Segoe UI,Arial,sans-serif;color:#1d2b2f">
<div style="max-width:600px;margin:0 auto;padding:24px">
<div style="background:#18424C;color:#fff;border-radius:14px 14px 0 0;padding:16px 22px;font-weight:700">Ocupacional · Chamados da TI</div>
<div style="background:#fff;border-radius:0 0 14px 14px;padding:22px">
<p style="margin:0 0 6px;color:#5A6F73">%s</p>
<h2 style="margin:0 0 16px;font-size:20px">#%d · %s</h2>
<table style="border-collapse:collapse;font-size:14px;margin-bottom:16px">%s</table>
<div style="white-space:pre-wrap;background:#F4F7F6;border-radius:10px;padding:12px 14px;font-size:14px">%s</div>
<p style="margin:22px 0 0"><a href="%s" style="background:#ADDA23;color:#0C1F23;text-decoration:none;font-weight:700;padding:12px 22px;border-radius:999px;display:inline-block">Abrir o chamado</a></p>
</div><p style="color:#8A9A9D;font-size:12px;text-align:center">Aviso automático do sistema interno da TI.</p></div></body></html>`,
		html.EscapeString(motivo), c.ID, html.EscapeString(c.Titulo), tab.String(), html.EscapeString(vazioTraco(desc)), html.EscapeString(link))
	go func() {
		err := s.b.enviarEmail(para, assunto, txt.String(), corpo)
		s.b.RegistrarEnvio(c.ID, para, assunto, err)
		destino := strings.Join(para, ", ")
		if err != nil {
			log.Printf("e-mail do chamado #%d não enviado: %v", c.ID, err)
			s.b.RegistrarEvento(c.ID, "sistema", "email", "e-mail NÃO enviado para "+destino+": "+explicarErroSMTP(err))
		} else {
			log.Printf("e-mail do chamado #%d enviado para %s", c.ID, destino)
			s.b.RegistrarEvento(c.ID, "sistema", "email", "e-mail enviado para "+destino)
		}
	}()
}

// emailsEquipe: e-mails de admins e técnicos (sem repetir), exceto o de quem fez a ação.
func (s *Servidor) emailsEquipe(excetoID int64) []string {
	t, _ := s.b.Tecnicos()
	vistos := map[string]bool{}
	var l []string
	for _, a := range t {
		e := strings.ToLower(strings.TrimSpace(a.Email))
		if e == "" || a.ID == excetoID || vistos[e] {
			continue
		}
		vistos[e] = true
		l = append(l, e)
	}
	return l
}

// avisarNovoChamado: chamado recém-aberto (painel ou página pública). autorID = quem abriu
// pelo painel (0 na página pública), para não mandar e-mail para si mesmo.
func (s *Servidor) avisarNovoChamadoPorEmail(r *http.Request, id, autorID int64) {
	c, err := s.b.Chamado(id)
	if err != nil {
		return
	}
	link := s.baseURL(r) + "/chamados/" + strconv.FormatInt(id, 10)
	enviados := map[string]bool{}
	if c.AtribuidoID > 0 && c.AtribuidoID != autorID && s.b.Config("email_atribuicao") != "0" {
		if a, err := s.b.AdminPorID(c.AtribuidoID); err == nil && strings.TrimSpace(a.Email) != "" {
			e := strings.ToLower(strings.TrimSpace(a.Email))
			enviados[e] = true
			s.emailChamado([]string{e}, "Atribuído a você", c, link, "Um chamado novo foi aberto e direcionado para você.")
		}
	}
	if s.b.Config("email_equipe") == "1" {
		var resto []string
		for _, e := range s.emailsEquipe(autorID) {
			if !enviados[e] {
				resto = append(resto, e)
			}
		}
		s.emailChamado(resto, "Chamado novo", c, link, "Um chamado novo foi aberto.")
	}
}

// avisarAtribuicaoPorEmail: o responsável mudou para alguém diferente de quem fez a mudança.
func (s *Servidor) avisarAtribuicaoPorEmail(r *http.Request, id, novoResp, autorID int64) {
	if novoResp <= 0 || novoResp == autorID || s.b.Config("email_atribuicao") == "0" {
		return
	}
	a, err := s.b.AdminPorID(novoResp)
	if err != nil || strings.TrimSpace(a.Email) == "" {
		return
	}
	c, err := s.b.Chamado(id)
	if err != nil {
		return
	}
	s.emailChamado([]string{a.Email}, "Atribuído a você", c, s.baseURL(r)+"/chamados/"+strconv.FormatInt(id, 10),
		"Este chamado foi direcionado para você.")
}

// responsavelAutomatico: técnico configurado para receber os chamados novos sem responsável.
func (s *Servidor) responsavelAutomatico() int64 {
	id := parseID(s.b.Config("chamados_responsavel_padrao"))
	if id <= 0 {
		return 0
	}
	if a, err := s.b.AdminPorID(id); err != nil || !a.PodeGerenciarChamados() {
		return 0
	}
	return id
}

// ---------------- Administração ----------------

func (s *Servidor) acaoConfigEmail(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	host := strings.TrimSpace(r.FormValue("smtp_host"))
	porta := soNumero(r.FormValue("smtp_porta"))
	seg := r.FormValue("smtp_seguranca")
	if seg != "tls" && seg != "nenhuma" {
		seg = "starttls"
	}
	de := strings.TrimSpace(r.FormValue("smtp_de"))
	if strings.ContainsAny(host+de, "\r\n") {
		redirecionar(w, r, "/admin#email", "", fmt.Errorf("dados inválidos"))
		return
	}
	_ = s.b.DefinirConfig("smtp_host", host)
	_ = s.b.DefinirConfig("smtp_porta", porta)
	_ = s.b.DefinirConfig("smtp_seguranca", seg)
	_ = s.b.DefinirConfig("smtp_usuario", strings.TrimSpace(r.FormValue("smtp_usuario")))
	_ = s.b.DefinirConfig("smtp_de", de)
	if senha := r.FormValue("smtp_senha"); senha != "" {
		_ = s.b.DefinirConfig("smtp_senha", senha) // vazio = mantém a senha salva
	}
	if r.FormValue("smtp_apagar_senha") == "1" {
		_ = s.b.DefinirConfig("smtp_senha", "")
	}
	um := func(c string) string {
		if r.FormValue(c) == "1" {
			return "1"
		}
		return "0"
	}
	_ = s.b.DefinirConfig("email_atribuicao", um("email_atribuicao"))
	_ = s.b.DefinirConfig("email_equipe", um("email_equipe"))
	resp := parseID(r.FormValue("responsavel_padrao"))
	if resp > 0 {
		_ = s.b.DefinirConfig("chamados_responsavel_padrao", strconv.FormatInt(resp, 10))
	} else {
		_ = s.b.DefinirConfig("chamados_responsavel_padrao", "")
	}
	s.b.Auditar(sess.Admin.Usuario, "configurar e-mail", fmt.Sprintf("%s:%s (%s), atribuição=%s, equipe=%s", host, porta, seg, um("email_atribuicao"), um("email_equipe")))
	redirecionar(w, r, "/admin#email", "Configurações de e-mail salvas.", nil)
}

func (s *Servidor) acaoTestarEmail(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	para := strings.TrimSpace(r.FormValue("para"))
	if para == "" {
		para = sess.Admin.Email
	}
	if para == "" {
		redirecionar(w, r, "/admin#email", "", fmt.Errorf("informe um e-mail para o teste (ou cadastre o seu em Minha conta)"))
		return
	}
	err := s.b.enviarEmail([]string{para}, "Teste de e-mail — Chamados da TI",
		"Se você recebeu esta mensagem, o envio de e-mails do sistema está funcionando.",
		`<p style="font-family:Segoe UI,Arial,sans-serif">Se você recebeu esta mensagem, o envio de e-mails do sistema de chamados da TI está <b>funcionando</b>.</p>`)
	s.b.RegistrarEnvio(0, []string{para}, "Teste de e-mail", err)
	s.b.Auditar(sess.Admin.Usuario, "teste de e-mail", para)
	if err != nil {
		err = fmt.Errorf("o e-mail de teste NÃO foi enviado: %s", explicarErroSMTP(err))
	}
	redirecionar(w, r, "/admin#email", "E-mail de teste aceito pelo servidor SMTP para "+para+". Se não chegar em alguns minutos, olhe a caixa de spam.", err)
}

// ---------------- registro de envios (1.10.1) ----------------

type EnvioEmail struct {
	Em, Para, Assunto, Erro string
	ChamadoID               int64
	OK                      bool
}

func (b *Banco) RegistrarEnvio(chamadoID int64, para []string, assunto string, err error) {
	e := ""
	if err != nil {
		e = explicarErroSMTP(err)
	}
	_, _ = b.db.Exec(`INSERT INTO email_envios(em,chamado_id,para,assunto,ok,erro) VALUES(?,?,?,?,?,?)`,
		agora(), nullID(chamadoID), strings.Join(para, ", "), assunto, err == nil, e)
	_, _ = b.db.Exec(`DELETE FROM email_envios WHERE id NOT IN (SELECT id FROM email_envios ORDER BY id DESC LIMIT 500)`)
}

func (b *Banco) UltimosEnvios(n int) []EnvioEmail {
	rows, err := b.db.Query(`SELECT em, COALESCE(chamado_id,0), para, assunto, ok, erro FROM email_envios ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []EnvioEmail
	for rows.Next() {
		var e EnvioEmail
		if rows.Scan(&e.Em, &e.ChamadoID, &e.Para, &e.Assunto, &e.OK, &e.Erro) == nil {
			l = append(l, e)
		}
	}
	return l
}

// explicarErroSMTP traduz os erros mais comuns em uma dica do que fazer.
func explicarErroSMTP(err error) string {
	if err == nil {
		return ""
	}
	m := err.Error()
	l := strings.ToLower(m)
	dica := ""
	switch {
	case strings.Contains(l, "5.7.139") || strings.Contains(l, "smtpclientauthentication") || strings.Contains(l, "smtp auth") && strings.Contains(l, "disabled"):
		dica = "o SMTP autenticado está desligado no Microsoft 365 para essa conta. No Centro de administração: Usuários → a conta → Email → Gerenciar aplicativos de email → marque \"SMTP autenticado\" (pode levar alguns minutos para valer)."
	case strings.Contains(l, "login no servidor de e-mail recusado") && (strings.Contains(l, "i/o timeout") || strings.Contains(l, "deadline")):
		dica = "o servidor de e-mail não respondeu ao login. Normalmente é usuário/senha errados (alguns servidores demoram a recusar) ou a conexão sendo cortada por firewall/antivírus."
	case strings.Contains(l, "5.7.3") || strings.Contains(l, "535") || strings.Contains(l, "authentication unsuccessful") || strings.Contains(l, "login no servidor de e-mail recusado"):
		dica = "usuário ou senha do SMTP recusados. Confira a senha; se a conta tem MFA, use uma senha de app."
	case strings.Contains(l, "5.7.60") || strings.Contains(l, "sendasdenied") || strings.Contains(l, "send as"):
		dica = "o remetente (De) precisa ser o mesmo e-mail da conta do SMTP (ou uma caixa que ela possa usar)."
	case strings.Contains(l, "i/o timeout") || strings.Contains(l, "connection timed out") || strings.Contains(l, "deadline"):
		dica = "o servidor não conseguiu falar com o SMTP. A porta (587/465) pode estar bloqueada no firewall/antivírus da rede."
	case strings.Contains(l, "no such host"):
		dica = "o nome do servidor SMTP não foi encontrado. Confira o endereço (ex.: smtp.office365.com)."
	case strings.Contains(l, "connection refused"):
		dica = "o servidor SMTP recusou a conexão nessa porta. Confira porta e segurança (587 = STARTTLS, 465 = TLS)."
	case strings.Contains(l, "x509") || strings.Contains(l, "certificate"):
		dica = "problema no certificado do servidor SMTP. Confira se o endereço está certo (o nome tem que bater com o certificado)."
	case strings.Contains(l, "não configurado"):
		dica = "preencha o servidor SMTP e o remetente em Administração."
	}
	if dica != "" {
		return m + " — " + dica
	}
	return m
}
