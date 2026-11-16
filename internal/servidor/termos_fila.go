package servidor

// Fila de e-mails do módulo Termos (1.23). Convite, termo para assinar, lembrete, cópia do termo
// e aviso ao RH passam por aqui: cada envio fica gravado em termo_envios e, se o SMTP falhar, é
// tentado de novo a cada 15 minutos, até 5 vezes; depois fica com o erro e o botão Reenviar.
// O código de verificação do aceite NÃO passa pela fila (sai na hora; se falhar, a tela avisa).
//
// A fila entrega por uma interface DestinoTermo. Hoje há destinoEmail (pessoa e RH); uma futura
// integração com o LuzIA RH (destinoLuziaAPI) entra registrando outro destino, sem mexer no resto.

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	maxTentativasFila = 5
	intervaloFila     = 15 * time.Minute
)

type EnvioFila struct {
	ID         int64
	TermoID    int64
	AdminID    int64
	Destino    string // pessoa | rh | luzia_api
	Tipo       string // convite | termo | lembrete | copia | rh | ...
	Para       string // e-mails separados por vírgula
	Assunto    string
	Texto      string
	HTML       string
	AnexoNome  string
	AnexoTipo  string
	Anexo      []byte
	Situacao   string // pendente | enviado | erro
	Tentativas int
	Erro       string
	CriadoEm   string
	TentadoEm  string
	ProximaEm  string
}

func (e EnvioFila) RotuloSituacao() string {
	switch e.Situacao {
	case "pendente":
		if e.Tentativas > 0 {
			return fmt.Sprintf("Nova tentativa (%d/%d)", e.Tentativas, maxTentativasFila)
		}
		return "Na fila"
	case "enviado":
		return "Enviado"
	case "erro":
		return "Falhou"
	case "cancelado":
		return "Cancelado"
	}
	return e.Situacao
}

// DestinoTermo entrega um envio da fila.
type DestinoTermo interface {
	Nome() string
	Enviar(e *EnvioFila) error
}

type destinoEmail struct {
	b    *Banco
	nome string
}

func (d destinoEmail) Nome() string { return d.nome }

func (d destinoEmail) Enviar(e *EnvioFila) error {
	var anexos []AnexoEmail
	if len(e.Anexo) > 0 {
		anexos = []AnexoEmail{{Nome: e.AnexoNome, Tipo: e.AnexoTipo, Dados: e.Anexo}}
	}
	return d.b.enviarEmailComAnexos(strings.Split(e.Para, ","), e.Assunto, e.Texto, e.HTML, anexos)
}

// filaEmails guarda os destinos e o "cutucão" para processar na hora.
type filaEmails struct {
	mu       sync.Mutex // um processamento por vez
	destinos map[string]DestinoTermo
	cutucar  chan struct{}
}

func novaFila(b *Banco) *filaEmails {
	return &filaEmails{
		destinos: map[string]DestinoTermo{
			"pessoa": destinoEmail{b, "pessoa"},
			"rh":     destinoEmail{b, "rh"},
		},
		cutucar: make(chan struct{}, 1),
	}
}

// Enfileirar grava o envio como pendente (tentado logo em seguida pelo processador).
func (b *Banco) Enfileirar(e EnvioFila) (int64, error) {
	if e.Destino == "" {
		e.Destino = "pessoa"
	}
	if len(destinatariosValidos(strings.Split(e.Para, ","))) == 0 && e.Destino != "luzia_api" {
		return 0, errors.New("nenhum destinatário com e-mail válido")
	}
	x, err := b.db.Exec(`INSERT INTO termo_envios(termo_id,admin_id,destino,tipo,para,assunto,texto,html,anexo_nome,anexo_tipo,anexo,situacao,criado_em,proxima_em)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,'pendente',?,?)`,
		nullID(e.TermoID), nullID(e.AdminID), e.Destino, e.Tipo, e.Para, e.Assunto, e.Texto, e.HTML, e.AnexoNome, e.AnexoTipo, e.Anexo, agora(), agora())
	if err != nil {
		return 0, err
	}
	return x.LastInsertId()
}

// enfileirar grava e já cutuca o processador.
func (s *Servidor) enfileirar(e EnvioFila) (int64, error) {
	id, err := s.b.Enfileirar(e)
	if err == nil {
		select {
		case s.fila.cutucar <- struct{}{}:
		default:
		}
	}
	return id, err
}

// envioPendentes devolve os envios prontos para tentar agora (lê tudo antes de enviar: o banco
// tem uma conexão só, não dá para enviar dentro do rows.Next()).
func (b *Banco) enviosProntos(limite int) []EnvioFila {
	rows, err := b.db.Query(`SELECT id, COALESCE(termo_id,0), COALESCE(admin_id,0), destino, tipo, para, assunto, texto, html, anexo_nome, anexo_tipo, anexo, tentativas
		FROM termo_envios WHERE situacao='pendente' AND proxima_em <= ? ORDER BY id LIMIT ?`, agora(), limite)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []EnvioFila
	for rows.Next() {
		var e EnvioFila
		if rows.Scan(&e.ID, &e.TermoID, &e.AdminID, &e.Destino, &e.Tipo, &e.Para, &e.Assunto, &e.Texto, &e.HTML, &e.AnexoNome, &e.AnexoTipo, &e.Anexo, &e.Tentativas) == nil {
			l = append(l, e)
		}
	}
	return l
}

// processarFila tenta cada envio pronto pelo destino dele e grava o resultado.
func (b *Banco) processarFila(destinos map[string]DestinoTermo, quando time.Time) (enviados, falhas int) {
	for _, e := range b.enviosProntos(50) {
		d, ok := destinos[e.Destino]
		var err error
		if !ok {
			err = fmt.Errorf("destino %q sem integração configurada", e.Destino)
		} else {
			err = d.Enviar(&e)
		}
		e.Tentativas++
		tentado := quando.UTC().Format(time.RFC3339)
		if err == nil {
			enviados++
			_, _ = b.db.Exec(`UPDATE termo_envios SET situacao='enviado', tentativas=?, erro='', tentado_em=?, proxima_em='' WHERE id=?`, e.Tentativas, tentado, e.ID)
			if e.Tipo == "convite" {
				b.apagarCorpoEnvio(e.ID) // o corpo tem o link do convite: não fica guardado
			}
		} else {
			falhas++
			situacao, proxima := "pendente", quando.Add(intervaloFila).UTC().Format(time.RFC3339)
			if e.Tentativas >= maxTentativasFila {
				situacao, proxima = "erro", ""
			}
			_, _ = b.db.Exec(`UPDATE termo_envios SET situacao=?, tentativas=?, erro=?, tentado_em=?, proxima_em=? WHERE id=?`,
				situacao, e.Tentativas, explicarErroSMTP(err), tentado, proxima, e.ID)
		}
		if e.Destino != "luzia_api" {
			b.RegistrarEnvio(0, strings.Split(e.Para, ","), e.Assunto, err) // aparece em "Últimos envios"
		}
	}
	return
}

// IniciarFilaEmails roda o processador em segundo plano: a cada minuto e quando algo entra na fila.
func (s *Servidor) IniciarFilaEmails() {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			s.rodarFila()
			s.lembretesSolicitacoes() // 1.24: lembrete ao gestor (no máximo uma verificação por hora)
			select {
			case <-t.C:
			case <-s.fila.cutucar:
			}
		}
	}()
}

func (s *Servidor) rodarFila() {
	s.fila.mu.Lock()
	defer s.fila.mu.Unlock()
	if ok, f := s.b.processarFila(s.fila.destinos, time.Now()); ok+f > 0 {
		log.Printf("fila de e-mails: %d enviado(s), %d falha(s)", ok, f)
	}
}

const corpoRemovido = "[conteúdo removido: continha o link de convite, que só vale uma vez]"

// apagarCorpoEnvio tira o texto de um envio (o link do convite não fica guardado em claro).
func (b *Banco) apagarCorpoEnvio(id int64) {
	_, _ = b.db.Exec(`UPDATE termo_envios SET texto=?, html=? WHERE id=?`, corpoRemovido, corpoRemovido, id)
}

// cancelarConvitesNaFila: ao renovar, cancelar ou usar um convite, os e-mails de convite antigos
// dessa pessoa saem da fila e perdem o corpo (o link antigo não vale mais).
func (b *Banco) cancelarConvitesNaFila(adminID int64) {
	_, _ = b.db.Exec(`UPDATE termo_envios SET situacao=CASE WHEN situacao='enviado' THEN 'enviado' ELSE 'cancelado' END, texto=?, html=?, proxima_em=''
		WHERE admin_id=? AND tipo='convite'`, corpoRemovido, corpoRemovido, adminID)
}

// ReenviarEnvio volta um envio com erro (ou já enviado) para a fila, zerando as tentativas.
func (b *Banco) ReenviarEnvio(id int64) error {
	var tipo, texto string
	if err := b.db.QueryRow(`SELECT tipo, texto FROM termo_envios WHERE id=?`, id).Scan(&tipo, &texto); err != nil {
		return errors.New("envio não encontrado")
	}
	if texto == corpoRemovido {
		return errors.New("este convite não pode ser reenviado (o link antigo não vale mais): use \"Renovar convite\" na ficha da pessoa")
	}
	x, err := b.db.Exec(`UPDATE termo_envios SET situacao='pendente', tentativas=0, erro='', proxima_em=? WHERE id=?`, agora(), id)
	if err != nil {
		return err
	}
	if n, _ := x.RowsAffected(); n == 0 {
		return errors.New("envio não encontrado")
	}
	return nil
}

// EnviosFila lista os últimos envios (sem o conteúdo do anexo). adminID > 0 filtra por pessoa.
func (b *Banco) EnviosFila(limite int, adminID int64) []EnvioFila {
	q := `SELECT id, COALESCE(termo_id,0), COALESCE(admin_id,0), destino, tipo, para, assunto, anexo_nome, situacao, tentativas, erro, criado_em, tentado_em, proxima_em FROM termo_envios`
	args := []any{}
	if adminID > 0 {
		q += ` WHERE admin_id=?`
		args = append(args, adminID)
	}
	args = append(args, limite)
	rows, err := b.db.Query(q+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []EnvioFila
	for rows.Next() {
		var e EnvioFila
		if rows.Scan(&e.ID, &e.TermoID, &e.AdminID, &e.Destino, &e.Tipo, &e.Para, &e.Assunto, &e.AnexoNome, &e.Situacao, &e.Tentativas, &e.Erro, &e.CriadoEm, &e.TentadoEm, &e.ProximaEm) == nil {
			l = append(l, e)
		}
	}
	return l
}

// QtdEnviosComErro: envios que desistiram (para destacar no painel).
func (b *Banco) QtdEnviosComErro() int {
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM termo_envios WHERE situacao='erro'`).Scan(&n)
	return n
}
