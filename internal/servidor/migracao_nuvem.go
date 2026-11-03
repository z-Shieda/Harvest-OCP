package servidor

// Ida para a nuvem (1.17):
//  - Importar banco: o servidor novo recebe o .db baixado do antigo (Administração → Backup) e passa
//    a usar esse banco depois de reiniciar sozinho — mesmos usuários, token dos agentes, chamados.
//  - Durante a migração (autorizada no servidor antigo), o antigo: avisa os agentes conectados em
//    tempo real ("migrar"), manda a página de abrir/acompanhar chamado para o endereço novo e mostra
//    uma faixa no painel. Ele continua no ar até a última máquina migrar.

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"inventario/internal/modelo"
)

const (
	arquivoImportacao = "inventario-importar.db"
	arquivoPosImport  = "inventario-importar.endereco"
)

// AplicarImportacaoPendente roda ANTES de abrir o banco: se houver um banco importado esperando,
// guarda o atual em backups/ e coloca o importado no lugar.
func AplicarImportacaoPendente(banco string) {
	dir := filepath.Dir(banco)
	imp := filepath.Join(dir, arquivoImportacao)
	if _, err := os.Stat(imp); err != nil {
		return
	}
	bk := filepath.Join(dir, "backups", "antes-de-importar-"+time.Now().Format("20060102-150405"))
	_ = os.MkdirAll(bk, 0o755)
	for _, suf := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(banco + suf); err == nil {
			if err := os.Rename(banco+suf, filepath.Join(bk, filepath.Base(banco)+suf)); err != nil {
				log.Printf("importação do banco ADIADA: não consegui guardar %s: %v", banco+suf, err)
				return
			}
		}
	}
	if err := os.Rename(imp, banco); err != nil {
		log.Printf("ERRO na importação do banco: %v — voltando o anterior", err)
		for _, suf := range []string{"", "-wal", "-shm"} {
			_ = os.Rename(filepath.Join(bk, filepath.Base(banco)+suf), banco+suf)
		}
		return
	}
	log.Printf("banco importado aplicado; o anterior foi guardado em %s", bk)
}

// AjustarAposImportacao roda DEPOIS de abrir o banco importado: mantém o endereço deste servidor
// (o banco veio do antigo, com o endereço dele) e limpa a migração que estava configurada lá.
func (b *Banco) AjustarAposImportacao(banco string) {
	arq := filepath.Join(filepath.Dir(banco), arquivoPosImport)
	conteudo, err := os.ReadFile(arq)
	if err != nil {
		return
	}
	_ = os.Remove(arq)
	_ = b.DefinirConfig("endereco_servidor", strings.TrimSpace(string(conteudo)))
	for _, k := range []string{"migrar_ativa", "migrar_servidor", "migrar_token", "migrar_canario"} {
		_ = b.DefinirConfig(k, "")
	}
	_, _ = b.db.Exec(`DELETE FROM sessoes`) // sessões do servidor antigo não valem aqui
	b.Auditar("sistema", "banco importado de outro servidor", "")
}

// validarBancoImportado confere se o arquivo é um banco deste sistema.
func validarBancoImportado(caminho string) error {
	f, err := os.Open(caminho)
	if err != nil {
		return err
	}
	cab := make([]byte, 16)
	_, _ = io.ReadFull(f, cab)
	f.Close()
	if !bytes.Equal(cab, []byte("SQLite format 3\x00")) {
		return fmt.Errorf("o arquivo não é um banco SQLite (use o .db baixado em Administração → Backup)")
	}
	usarInterpretador()
	db, err := sql.Open("sqlite3", "file:"+caminho+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var nMaq, nAdm int
	var token string
	if err := db.QueryRow(`SELECT COUNT(*) FROM maquinas`).Scan(&nMaq); err != nil {
		return fmt.Errorf("o banco não parece ser do Inventário (sem tabela de máquinas)")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&nAdm); err != nil || nAdm == 0 {
		return fmt.Errorf("o banco não tem usuários do painel")
	}
	_ = db.QueryRow(`SELECT valor FROM config WHERE chave='token_agente'`).Scan(&token)
	if token == "" {
		return fmt.Errorf("o banco não tem token dos agentes")
	}
	var ok string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
		return fmt.Errorf("o banco está corrompido (integrity_check: %s)", ok)
	}
	return nil
}

func (s *Servidor) acaoImportarBanco(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	if s.CaminhoBanco == "" {
		redirecionar(w, r, "/admin#importar", "", fmt.Errorf("caminho do banco desconhecido"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		redirecionar(w, r, "/admin#importar", "", fmt.Errorf("envio inválido: %v", err))
		return
	}
	if !s.b.VerificarSenha(sess.Admin.ID, r.FormValue("senha")) {
		s.b.Auditar(sess.Admin.Usuario, "importar banco RECUSADO (senha incorreta)", "")
		redirecionar(w, r, "/admin#importar", "", fmt.Errorf("senha incorreta: nada foi alterado"))
		return
	}
	arq, _, err := r.FormFile("arquivo")
	if err != nil {
		redirecionar(w, r, "/admin#importar", "", fmt.Errorf("selecione o arquivo .db"))
		return
	}
	defer arq.Close()
	dir := filepath.Dir(s.CaminhoBanco)
	tmp := filepath.Join(dir, arquivoImportacao+".parcial")
	f, err := os.Create(tmp)
	if err != nil {
		redirecionar(w, r, "/admin#importar", "", err)
		return
	}
	_, err = io.Copy(f, arq)
	f.Close()
	if err == nil {
		err = validarBancoImportado(tmp)
	}
	if err != nil {
		_ = os.Remove(tmp)
		redirecionar(w, r, "/admin#importar", "", err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(dir, arquivoImportacao)); err != nil {
		redirecionar(w, r, "/admin#importar", "", err)
		return
	}
	_ = os.WriteFile(filepath.Join(dir, arquivoPosImport), []byte(s.b.Config("endereco_servidor")), 0o644)
	s.b.Auditar(sess.Admin.Usuario, "importar banco de outro servidor", "reiniciando")
	log.Printf("banco recebido para importação; reiniciando o servidor")
	s.render(w, r, "atualizando", "Importando o banco", "admin", sess, map[string]any{
		"Mensagem": "Banco recebido e conferido. O servidor está reiniciando para passar a usar o banco importado — em alguns segundos entre de novo com um usuário do banco importado.",
	})
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	go func() {
		time.Sleep(time.Second)
		if err := reiniciarServidor(); err != nil {
			log.Printf("não consegui reiniciar sozinho (%v): reinicie o servidor para aplicar a importação", err)
		}
	}()
}

// ---------- servidor antigo durante a migração ----------

func (s *Servidor) migracaoAtiva() (destino string, ok bool) {
	d := strings.TrimRight(strings.TrimSpace(s.b.Config("migrar_servidor")), "/")
	return d, d != "" && s.b.Config("migrar_ativa") == "1"
}

// eventoMigrar: a ordem de migração para um agente conectado (nil se não se aplica a ele).
func (s *Servidor) eventoMigrar(hostname string) *modelo.Migracao {
	d, ok := s.migracaoAtiva()
	if !ok || !noCanario(s.b.Config("migrar_canario"), hostname) {
		return nil
	}
	return &modelo.Migracao{Servidor: d, Token: s.b.Config("migrar_token")}
}

// avisarMigracaoConectados manda a ordem para os agentes conectados em tempo real agora.
func (s *Servidor) avisarMigracaoConectados() int {
	n := 0
	for _, c := range s.eventos.Conectados() {
		if ev := s.eventoMigrar(c.Hostname); ev != nil {
			n += s.eventos.enviarPara(c.id, "migrar", ev)
		}
	}
	return n
}

// paraNovoServidor: com a migração autorizada, as páginas públicas de chamado vão para o novo.
func (s *Servidor) paraNovoServidor(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d, ok := s.migracaoAtiva(); ok && s.b.Config("migrar_publico") != "0" {
			http.Redirect(w, r, d+r.URL.RequestURI(), http.StatusTemporaryRedirect)
			return
		}
		h(w, r)
	}
}
