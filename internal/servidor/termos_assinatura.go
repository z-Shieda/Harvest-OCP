package servidor

// Aceite do termo pelo colaborador (1.23, fase 3): dois fatores (senha da conta + código de
// 6 dígitos no e-mail corporativo), identificação da máquina, evidências, hash SHA-256 e PDF.
// A assinatura é gravada uma vez em termo_assinaturas e nunca é alterada.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// conferirDono: só o colaborador do termo assina.
func conferirDono(t *Termo, a Admin) error {
	if t.AdminID != a.ID {
		return errors.New("este termo é de outra pessoa")
	}
	if t.Situacao != "aguardando" {
		return errors.New("este termo não está aguardando assinatura (" + strings.ToLower(t.RotuloSituacao()) + ")")
	}
	return nil
}

// conferirPropriaMaquina: com "exigir assinatura na própria máquina", só confirmada/provável na máquina do termo.
func conferirPropriaMaquina(tp *TipoTermo, t *Termo, m MaquinaAssinatura) error {
	if !tp.ExigePropriaMaquina || t.MaquinaID == 0 || t.LiberarMaquinaMotivo != "" {
		return nil
	}
	if m.ID == t.MaquinaID && (m.Grau == "confirmada" || m.Grau == "provavel") {
		return nil
	}
	alvo := t.Equipamento
	if alvo == "" {
		alvo = "a máquina do termo"
	}
	return fmt.Errorf("este termo precisa ser assinado no próprio equipamento (%s), com o seu usuário do Windows logado nele. Identificado agora: %s. Se não tiver acesso ao equipamento, peça à T.I para liberar", alvo, m.Texto())
}

// rascunhoColaborador lê e guarda o que a pessoa já preencheu (campos dela e aceites).
func (s *Servidor) rascunhoColaborador(t *Termo, v *VersaoTermo, get func(string) string) ([]bool, error) {
	if err := LerValoresForm(v.Campos(), "colaborador", &t.Valores, get); err != nil {
		return nil, err
	}
	_ = s.b.gravarValores(t.ID, t.Valores)
	var marcados []bool
	for i := range v.Aceites() {
		marcados = append(marcados, get("aceite_"+strconv.Itoa(i)) == "1")
	}
	return marcados, nil
}

func faltaAceite(marcados []bool) bool {
	for _, m := range marcados {
		if !m {
			return true
		}
	}
	return false
}

// PedirCodigo: confere campos e aceites, identifica a máquina e manda o código por e-mail (fora da fila).
func (s *Servidor) PedirCodigo(t *Termo, sess *Sessao, ip string, get func(string) string) (MaquinaAssinatura, error) {
	var m MaquinaAssinatura
	if err := conferirDono(t, sess.Admin); err != nil {
		return m, err
	}
	v, err := s.b.Versao(t.VersaoID)
	if err != nil {
		return m, err
	}
	tp, err := s.b.TipoTermo(t.TipoID)
	if err != nil {
		return m, err
	}
	marcados, err := s.rascunhoColaborador(t, v, get)
	if err != nil {
		return m, err
	}
	if p := Pendencias(v.Campos(), t.Valores, "colaborador", 0); len(p) > 0 {
		return m, errors.New("preencha: " + strings.Join(p, "; "))
	}
	if faltaAceite(marcados) {
		return m, errors.New("marque todas as caixas de aceite antes de pedir o código")
	}
	m = s.identificarMaquina(ip, s.loginRedeDe(t.AdminID))
	if err := conferirPropriaMaquina(tp, t, m); err != nil {
		return m, err
	}
	mj, _ := json.Marshal(m)
	codigo, err := s.b.NovoCodigo(sess.Admin.ID, t.ID, ip, string(mj))
	if err != nil {
		return m, err
	}
	para := t.Email
	texto, corpo := s.emailSimples("Seu código para assinar o termo: "+codigo, []string{
		"Use este código para confirmar a assinatura do " + t.TipoNome + " nº " + t.NumeroTexto() + ":",
		codigo,
		"Ele vale por 10 minutos. Se não foi você que pediu, não use o código e avise a T.I.",
	}, "", "", "Nunca informe este código a ninguém — nem à T.I.")
	err = s.emailDireto([]string{para}, "Código de assinatura: "+codigo+" — "+s.marca().Nome, texto, corpo)
	s.b.RegistrarEnvio(0, []string{para}, "Código de assinatura do termo nº "+t.NumeroTexto(), err)
	if err != nil {
		_, _ = s.b.db.Exec(`UPDATE termo_codigos SET usado=1 WHERE admin_id=? AND termo_id=? AND usado=0`, sess.Admin.ID, t.ID)
		return m, fmt.Errorf("não foi possível enviar o código para %s: %s", para, explicarErroSMTP(err))
	}
	s.b.Auditar(sess.Admin.Usuario, "termos: código de assinatura enviado", fmt.Sprintf("termo %s · máquina %s", t.NumeroTexto(), m.Texto()))
	return m, nil
}

// Assinar grava o aceite, gera o PDF e avisa a pessoa e o RH.
func (s *Servidor) Assinar(t *Termo, sess *Sessao, codigo, ip, ua, baseURL string, get func(string) string) error {
	if err := conferirDono(t, sess.Admin); err != nil {
		return err
	}
	v, err := s.b.Versao(t.VersaoID)
	if err != nil {
		return err
	}
	tp, err := s.b.TipoTermo(t.TipoID)
	if err != nil {
		return err
	}
	marcados, err := s.rascunhoColaborador(t, v, get)
	if err != nil {
		return err
	}
	if p := Pendencias(v.Campos(), t.Valores, "colaborador", 0); len(p) > 0 {
		return errors.New("preencha: " + strings.Join(p, "; "))
	}
	if faltaAceite(marcados) {
		return errors.New("marque todas as caixas de aceite")
	}
	var maq MaquinaAssinatura
	metodo := "senha da conta"
	if tp.ExigeCodigo {
		mj, err := s.b.ConferirCodigo(sess.Admin.ID, t.ID, codigo)
		if err != nil {
			s.b.Auditar(sess.Admin.Usuario, "termos: código de assinatura recusado", t.NumeroTexto()+": "+err.Error())
			return err
		}
		_ = json.Unmarshal([]byte(mj), &maq)
		metodo = "senha + código por e-mail para " + t.Email
	} else {
		maq = s.identificarMaquina(ip, s.loginRedeDe(t.AdminID))
	}
	if err := conferirPropriaMaquina(tp, t, maq); err != nil {
		return err
	}
	f, _ := s.b.Ficha(t.AdminID)
	assinadoEm := agora()
	ev := &Evidencias{
		AceitoPor:    vazioPadrao(f.Nome, f.Usuario) + " / " + f.Usuario,
		Autenticacao: "Senha da conta" + map[bool]string{true: " + código de 6 dígitos enviado a " + t.Email, false: ""}[tp.ExigeCodigo],
		DataHora:     dataHoraBrasilia(assinadoEm),
		IP:           ip,
		Dispositivo:  limitar(ua, 180),
		Maquina:      maq.Texto(),
		EmailCopia:   t.Email,
		EntreguePor:  vazioPadrao(t.Valores.Texto["entregue_por"], t.EnviadoPor),
		ValidacaoTI:  dataHoraBrasilia(t.EnviadoEm),
		Hash:         "-",
		Codigo:       "-",
	}
	if t.MaquinaID > 0 && maq.ID != 0 && maq.ID != t.MaquinaID {
		ev.AlertaMaquina = fmt.Sprintf("assinado na máquina %s, diferente do equipamento do termo (%s).", maq.Nome, t.Equipamento)
	}
	m := novaMontagem(v.CamposDef, v.AceitesDef, t.Valores)
	m.marcados, m.evid = marcados, ev
	base := textoPuro(m.resolver(v.Corpo))
	vj, _ := json.Marshal(t.Valores)
	soma := sha256.Sum256([]byte(base + "\n" + string(vj)))
	hash := hex.EncodeToString(soma[:])
	cod := codigoVerificacao(hash)
	ev.Hash, ev.Codigo = hash, cod
	pdfBytes := s.pdfDoTermo(t, v, m, false)
	aj, _ := json.Marshal(marcados)
	_, err = s.b.db.Exec(`INSERT INTO termo_assinaturas(termo_id,etapa,aceites_json,assinado_em,ip,user_agent,maquina_assinatura_id,maquina_assinatura,maquina_grau,maquinas_candidatas,metodo,email_codigo,texto_final,hash_sha256,codigo_verificacao,pdf)
		VALUES(?,'entrega',?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, t.ID, string(aj), assinadoEm, ip, limitar(ua, 400), nullID(maq.ID), maq.Nome, maq.Grau,
		strings.Join(maq.Candidatas, ", "), metodo, map[bool]string{true: t.Email, false: ""}[tp.ExigeCodigo], base+"\n"+string(vj), hash, cod, pdfBytes)
	if err != nil {
		return fmt.Errorf("não foi possível gravar a assinatura: %v", err)
	}
	if _, err := s.b.db.Exec(`UPDATE termos SET situacao='assinado', assinado_em=? WHERE id=? AND situacao='aguardando'`, assinadoEm, t.ID); err != nil {
		return err
	}
	s.b.Auditar(sess.Admin.Usuario, "termos: termo assinado", fmt.Sprintf("nº %s %s · máquina %s · código %s", t.NumeroTexto(), v.Sigla, maq.Texto(), cod))
	if t.ChamadoID > 0 {
		prazo := time.Now().Add(48 * time.Hour).In(fusoBrasilia).Format("02/01/2006 15:04")
		det := fmt.Sprintf("Termo nº %s assinado por %s (máquina %s). O colaborador pode registrar divergência sobre o estado do equipamento neste chamado até %s (48 h).",
			t.NumeroTexto(), t.Pessoa, maq.Texto(), prazo)
		if ev.AlertaMaquina != "" {
			det += " ATENÇÃO: " + ev.AlertaMaquina
		}
		s.b.RegistrarEvento(t.ChamadoID, t.Pessoa, "termo", det)
	}
	s.enviarCopias(t, tp, v, pdfBytes, cod, baseURL)
	return nil
}

// pdfDoTermo desenha o PDF de um termo (com as evidências da montagem).
func (s *Servidor) pdfDoTermo(t *Termo, v *VersaoTermo, m *montagem, previa bool) []byte {
	mc := s.marca()
	rodape := t.TipoNome
	if m.evid != nil && m.evid.Codigo != "" && m.evid.Codigo != "-" {
		rodape = "Assinado eletronicamente no Harvest · código de verificação " + m.evid.Codigo
	}
	return paraPDF(m.resolver(v.Corpo), CabecalhoPDF{Logo: s.logoPDF(), NomeMarca: mc.Nome, CorSec: hexPDF(mc.CorSec), CorPri: hexPDF(mc.CorPri),
		Direita: "Termo nº " + t.NumeroTexto() + " · " + v.Sigla, Rodape: rodape, Titulo: t.TipoNome + " nº " + t.NumeroTexto(), Previa: previa})
}

func nomeArquivoTermo(t *Termo, sigla string) string {
	n := fmt.Sprintf("Termo-%s-%s-%s.pdf", t.NumeroTexto(), sigla, primeiroNomeTexto(t.Pessoa))
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`, r) || r < 32 {
			return '_'
		}
		return r
	}, n)
}

// enviarCopias: PDF para a pessoa e para o RH (fila com nova tentativa).
func (s *Servidor) enviarCopias(t *Termo, tp *TipoTermo, v *VersaoTermo, pdfBytes []byte, cod, baseURL string) {
	arq := nomeArquivoTermo(t, v.Sigla)
	verif := s.linkAbsoluto(baseURL + "/verificar/" + cod)
	texto, corpo := s.emailSimples("Cópia do seu termo assinado", []string{
		"Olá, " + primeiroNomeTexto(t.Pessoa) + ",",
		"Segue em anexo a cópia em PDF do " + t.TipoNome + " nº " + t.NumeroTexto() + " (" + v.Sigla + "), que você acabou de assinar.",
		"Código de verificação: " + cod + ". Você também encontra o termo em Meus termos, no Harvest.",
	}, "Conferir autenticidade", verif, "")
	_, _ = s.enfileirar(EnvioFila{TermoID: t.ID, AdminID: t.AdminID, Destino: "pessoa", Tipo: "copia", Para: t.Email,
		Assunto: "Cópia do termo assinado — " + t.TipoNome + " nº " + t.NumeroTexto(), Texto: texto, HTML: corpo,
		AnexoNome: arq, AnexoTipo: "application/pdf", Anexo: pdfBytes})
	rh := s.b.ConfigTermos().EmailsRH
	if !tp.EnviaRH || len(rh) == 0 {
		return
	}
	f, _ := s.b.Ficha(t.AdminID)
	pat := t.Valores.Texto["equipamento_patrimonio"]
	assunto := fmt.Sprintf("[Termo assinado] %s · Nº %s · %s", v.Sigla, t.NumeroTexto(), t.Pessoa)
	if pat != "" {
		assunto += " · " + pat
	}
	linhas := []string{
		"Termo assinado eletronicamente no Harvest, para arquivar no LuzIA RH.",
		"Colaborador: " + t.Pessoa + func() string {
			if f != nil && f.CPF != "" {
				return " · CPF " + f.CPFFormatado()
			}
			return ""
		}(),
		"Termo: " + t.TipoNome + " · nº " + t.NumeroTexto() + " · versão " + v.Sigla,
		"Código de verificação: " + cod,
	}
	if t.Equipamento != "" {
		linhas = append(linhas, "Equipamento: "+t.Equipamento)
	}
	texto, corpo = s.emailSimples("Termo assinado", linhas, "Conferir autenticidade", verif, "")
	_, _ = s.enfileirar(EnvioFila{TermoID: t.ID, AdminID: t.AdminID, Destino: "rh", Tipo: "rh", Para: strings.Join(rh, ","),
		Assunto: assunto, Texto: texto, HTML: corpo, AnexoNome: arq, AnexoTipo: "application/pdf", Anexo: pdfBytes})
}

// montagemDoTermo: o termo como está agora (para ler na tela ou baixar o PDF).
func (s *Servidor) montagemDoTermo(t *Termo, v *VersaoTermo) *montagem {
	m := novaMontagem(v.CamposDef, v.AceitesDef, t.Valores)
	m.devolucao = t.Situacao == "em_devolucao" || t.Situacao == "encerrado"
	if as := s.b.AssinaturasDoTermo(t.ID); len(as) > 0 {
		a := as[0]
		m.marcados = a.Aceites
		f, _ := s.b.Ficha(t.AdminID)
		m.evid = &Evidencias{AceitoPor: vazioPadrao(f.Nome, f.Usuario) + " / " + f.Usuario, DataHora: dataHoraBrasilia(a.AssinadoEm), IP: a.IP,
			Dispositivo: limitar(a.UserAgent, 180), Codigo: a.Codigo, Hash: a.Hash, EmailCopia: t.Email,
			EntreguePor: vazioPadrao(t.Valores.Texto["entregue_por"], t.EnviadoPor), ValidacaoTI: dataHoraBrasilia(t.EnviadoEm)}
		if a.EmailCodigo != "" {
			m.evid.Autenticacao = "Senha da conta + código de 6 dígitos enviado a " + a.EmailCodigo
		} else {
			m.evid.Autenticacao = "Senha da conta"
		}
		ma := MaquinaAssinatura{ID: a.MaquinaID, Nome: a.Maquina, Grau: a.Grau}
		if a.Candidatas != "" {
			ma.Candidatas = strings.Split(a.Candidatas, ", ")
		}
		m.evid.Maquina = ma.Texto()
		if t.MaquinaID > 0 && a.MaquinaID != 0 && a.MaquinaID != t.MaquinaID {
			m.evid.AlertaMaquina = fmt.Sprintf("assinado na máquina %s, diferente do equipamento do termo (%s).", a.Maquina, t.Equipamento)
		}
	}
	return m
}
