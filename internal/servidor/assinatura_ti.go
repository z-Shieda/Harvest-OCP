package servidor

// Assinatura da T.I (1.21) no servidor:
//   - guarda e distribui a assinatura (manifesto) do agente embutido — /api/v1/agente.assinatura;
//   - envia o certificado atual aos agentes (revogação de chave antiga);
//   - exige ordem de migração assinada;
//   - "Esqueci minha senha" do painel com as 24 palavras (/recuperar), sem as palavras
//     nunca chegarem ao servidor: o harvest-chave responde a um desafio.
//
// O servidor NÃO tem nenhuma chave privada. Mesmo invadido, não consegue assinar nada.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"inventario/internal/assinatura"
)

const arquivoAssinatura = "agente.assinatura"

type StatusAssinatura struct {
	RaizConfigurada bool
	Raiz            string // impressão curta da chave raiz
	Presente        bool
	Valida          bool
	Motivo          string
	Versao          string
	Certificado     int64
	AssinadoEm      string
}

// CarregarAssinatura: na inicialização, importa o agente.assinatura deixado ao lado do banco
// pelo INSTALAR-SERVIDOR (se for válido para o agente embutido).
func (s *Servidor) CarregarAssinatura() {
	if s.CaminhoBanco == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(s.CaminhoBanco), arquivoAssinatura))
	if err != nil {
		return
	}
	if err := s.guardarAssinatura(b); err != nil {
		log.Printf("assinatura: %s ignorado: %v", arquivoAssinatura, err)
		return
	}
	log.Printf("assinatura da T.I carregada (%s)", arquivoAssinatura)
}

// guardarAssinatura valida o manifesto contra a raiz e o agente embutido e grava no banco.
func (s *Servidor) guardarAssinatura(b []byte) error {
	var m assinatura.Manifesto
	if json.Unmarshal(b, &m) != nil || m.Formato != assinatura.FormatoManifesto {
		return errors.New("arquivo de assinatura inválido (use o agente.assinatura gerado pelo harvest-chave)")
	}
	if err := s.conferirManifesto(&m); err != nil {
		return err
	}
	j, _ := json.Marshal(m)
	return s.b.DefinirConfig("assinatura_agente", string(j))
}

func (s *Servidor) conferirManifesto(m *assinatura.Manifesto) error {
	raiz, ok := assinatura.RaizEmbutida()
	if !ok {
		return errors.New("este servidor não tem a chave raiz da T.I gravada (assine o kit com o harvest-chave antes de instalar)")
	}
	if err := m.Verificar(raiz, 0); err != nil {
		return err
	}
	if m.Versao != s.VersaoAgente {
		return fmt.Errorf("a assinatura é da v%s, mas o agente embutido é a v%s", m.Versao, s.VersaoAgente)
	}
	if err := m.Confere(agenteExe); err != nil {
		return fmt.Errorf("não confere com o agente embutido: %v", err)
	}
	return nil
}

// manifestoAtual: o manifesto guardado, só se continuar válido para o agente embutido.
func (s *Servidor) manifestoAtual() (*assinatura.Manifesto, error) {
	j := s.b.Config("assinatura_agente")
	if j == "" {
		return nil, errors.New("nenhuma assinatura carregada")
	}
	var m assinatura.Manifesto
	if json.Unmarshal([]byte(j), &m) != nil {
		return nil, errors.New("assinatura guardada ilegível")
	}
	if err := s.conferirManifesto(&m); err != nil {
		return &m, err
	}
	return &m, nil
}

func (s *Servidor) statusAssinatura() StatusAssinatura {
	var st StatusAssinatura
	raiz, ok := assinatura.RaizEmbutida()
	st.RaizConfigurada = ok
	if ok {
		st.Raiz = assinatura.Impressao(raiz)
	}
	m, err := s.manifestoAtual()
	if m != nil {
		st.Presente = true
		st.Versao, st.Certificado = m.Versao, m.Certificado.Seq
		if t, e := time.Parse(time.RFC3339, m.AssinadoEm); e == nil {
			st.AssinadoEm = t.Local().Format("02/01/2006 15:04")
		}
	}
	if err != nil {
		st.Motivo = err.Error()
	} else {
		st.Valida = true
	}
	return st
}

// certificadoParaAgentes: o certificado atual, enviado em cada resposta (revoga os antigos).
func (s *Servidor) certificadoParaAgentes() *assinatura.Certificado {
	if m, err := s.manifestoAtual(); err == nil {
		c := m.Certificado
		return &c
	}
	return nil
}

func (s *Servidor) apiAgenteAssinatura(w http.ResponseWriter, r *http.Request) {
	if ok, _ := s.b.validarTokenAgente(r.Header.Get("X-Agente-Token")); !ok {
		http.Error(w, "token inválido", http.StatusUnauthorized)
		return
	}
	m, err := s.manifestoAtual()
	if err != nil {
		http.Error(w, "sem assinatura válida: "+err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

func (s *Servidor) acaoEnviarAssinatura(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	f, _, err := r.FormFile("arquivo")
	if err != nil {
		redirecionar(w, r, "/admin#assinatura", "", errors.New("escolha o arquivo agente.assinatura"))
		return
	}
	b, _ := io.ReadAll(io.LimitReader(f, 64<<10))
	f.Close()
	if err := s.guardarAssinatura(b); err != nil {
		redirecionar(w, r, "/admin#assinatura", "", err)
		return
	}
	st := s.statusAssinatura()
	s.b.Auditar(sess.Admin.Usuario, "enviar assinatura do agente", fmt.Sprintf("v%s cert %d", st.Versao, st.Certificado))
	s.eventos.enviar("versao", s.eventoVersao()) // conectados em tempo real se atualizam já
	redirecionar(w, r, "/admin#assinatura", "Assinatura da v"+st.Versao+" carregada: os agentes já podem se atualizar.", nil)
}

// ---------------- migração assinada ----------------

// ordemMigracaoValida confere a ordem colada no painel (com a assinatura configurada).
func ordemMigracaoValida(texto string) (assinatura.OrdemMigracao, error) {
	o, err := assinatura.LerOrdem(texto)
	if err != nil {
		return o, err
	}
	raiz, _ := assinatura.RaizEmbutida()
	if err := o.Verificar(raiz, 0, time.Now()); err != nil {
		return o, err
	}
	return o, nil
}

// ---------------- recuperar o acesso ao painel (24 palavras) ----------------

type desafioRecuperacao struct {
	expira     time.Time
	tentativas int
}

var (
	muDesafios sync.Mutex
	desafios   = map[string]*desafioRecuperacao{}
)

func novoDesafio() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	d := strings.ToUpper(hex.EncodeToString(b))
	d = d[0:6] + "-" + d[6:12] + "-" + d[12:18] + "-" + d[18:24]
	muDesafios.Lock()
	defer muDesafios.Unlock()
	for k, v := range desafios {
		if time.Now().After(v.expira) {
			delete(desafios, k)
		}
	}
	if len(desafios) > 50 {
		desafios = map[string]*desafioRecuperacao{}
	}
	desafios[d] = &desafioRecuperacao{expira: time.Now().Add(15 * time.Minute)}
	return d
}

func (s *Servidor) paginaRecuperar(w http.ResponseWriter, r *http.Request) {
	_, ok := assinatura.RaizEmbutida()
	d := ""
	if ok {
		d = novoDesafio()
	}
	s.render(w, r, "recuperar", "Recuperar acesso", "", nil, map[string]any{"Disponivel": ok, "Desafio": d})
}

func (s *Servidor) acaoRecuperar(w http.ResponseWriter, r *http.Request) {
	falha := func(msg string) { redirecionar(w, r, "/recuperar", "", errors.New(msg)) }
	raiz, ok := assinatura.RaizEmbutida()
	if !ok {
		falha("recuperação indisponível neste servidor")
		return
	}
	chaveLim := "recuperar:" + ipCliente(r)
	if s.lim.bloqueado(chaveLim) {
		falha("muitas tentativas; aguarde 10 minutos")
		return
	}
	d := strings.ToUpper(strings.TrimSpace(r.FormValue("desafio")))
	muDesafios.Lock()
	reg := desafios[d]
	if reg != nil {
		reg.tentativas++
	}
	muDesafios.Unlock()
	if reg == nil || time.Now().After(reg.expira) || reg.tentativas > 5 {
		s.lim.registrarFalha(chaveLim)
		falha("o código de desafio venceu; use o novo código desta página")
		return
	}
	if !assinatura.ConferirResposta(raiz, d, r.FormValue("resposta")) {
		s.lim.registrarFalha(chaveLim)
		s.b.Auditar("recuperação", "recuperar acesso RECUSADO (resposta inválida)", ipCliente(r))
		falha("a resposta não confere — gere de novo no harvest-chave com as 24 palavras e este código")
		return
	}
	muDesafios.Lock()
	delete(desafios, d) // uso único
	muDesafios.Unlock()
	usuario := strings.TrimSpace(r.FormValue("usuario"))
	senha := r.FormValue("senha")
	if senha != r.FormValue("senha2") {
		falha("as duas senhas não são iguais")
		return
	}
	if err := s.b.DefinirSenhaAdministrador(usuario, senha); err != nil {
		falha(err.Error())
		return
	}
	s.b.Auditar("recuperação", "senha redefinida com as 24 palavras da T.I", usuario+" — IP "+ipCliente(r))
	log.Printf("RECUPERAÇÃO: senha de %s redefinida com a chave raiz (IP %s)", usuario, ipCliente(r))
	redirecionar(w, r, "/login", "Senha redefinida com as 24 palavras. Entre com a nova senha.", nil)
}

// RecusaAssinatura: máquina que recusou a atualização por causa da assinatura.
type RecusaAssinatura struct {
	ID       int64
	Hostname string
	Erro     string
}

func (b *Banco) RecusasAssinatura() []RecusaAssinatura {
	var l []RecusaAssinatura
	rows, err := b.db.Query(`SELECT id, hostname, au_erro FROM maquinas WHERE au_erro LIKE '%RECUSADA (assinatura)%' AND ignorada_em='' ORDER BY hostname`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var x RecusaAssinatura
		if rows.Scan(&x.ID, &x.Hostname, &x.Erro) == nil {
			l = append(l, x)
		}
	}
	return l
}
