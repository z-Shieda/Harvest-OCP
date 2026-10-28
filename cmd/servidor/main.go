// Servidor de inventário: recebe os relatórios dos agentes, guarda em SQLite e oferece o painel web.
//
//	inventario-servidor.exe                    inicia na porta 8080 (banco ao lado do executável)
//	inventario-servidor.exe -porta 9000 -banco D:\inventario\inventario.db
//	inventario-servidor.exe -cert cert.pem -chave chave.pem   (HTTPS)
//	inventario-servidor.exe -instalar          inicia junto com o Windows e libera a porta no firewall (admin)
//	inventario-servidor.exe -desinstalar
//	inventario-servidor.exe -redefinir-senha admin
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"inventario/internal/servidor"
)

var versao = "1.2.0"

func main() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)

	porta := flag.Int("porta", 8080, "porta HTTP do painel e da API")
	banco := flag.String("banco", filepath.Join(dir, "inventario.db"), "arquivo do banco de dados SQLite")
	cert := flag.String("cert", "", "certificado TLS (PEM) para usar HTTPS")
	chave := flag.String("chave", "", "chave privada TLS (PEM)")
	token := flag.String("token", "", "define o token dos agentes (opcional; por padrão é gerado automaticamente)")
	fInstalar := flag.Bool("instalar", false, "registra o servidor para iniciar com o Windows e libera a porta no firewall")
	fDesinstalar := flag.Bool("desinstalar", false, "remove a inicialização automática e a regra de firewall")
	fRedefinir := flag.String("redefinir-senha", "", "gera uma nova senha para o administrador informado")
	flag.Parse()

	if *fInstalar {
		if err := instalar(exe, *porta, *banco, *cert, *chave); err != nil {
			log.Fatalf("erro ao instalar: %v", err)
		}
		fmt.Printf("Servidor instalado e iniciado na porta %d. Ele também inicia sozinho com o Windows.\n", *porta)
		fmt.Printf("Painel: http://localhost:%d  (dados em %s)\n", *porta, *banco)
		return
	}
	if *fDesinstalar {
		if err := desinstalar(*porta); err != nil {
			log.Fatalf("erro ao desinstalar: %v", err)
		}
		fmt.Println("Inicialização automática removida.")
		return
	}

	// log em arquivo ao lado do banco (com rotação), além do console
	log.SetOutput(io.MultiWriter(os.Stderr, &logRotativo{caminho: filepath.Join(filepath.Dir(*banco), "servidor.log"), max: 5 << 20, manter: 5}))

	b, err := servidor.AbrirBanco(*banco)
	if err != nil {
		log.Fatalf("erro ao abrir o banco %s: %v", *banco, err)
	}
	defer b.Fechar()

	if *fRedefinir != "" {
		nova, err := b.RedefinirSenha(*fRedefinir)
		if err != nil {
			log.Fatalf("erro: %v", err)
		}
		fmt.Printf("Nova senha de %s: %s\n", *fRedefinir, nova)
		return
	}

	if *token != "" {
		_ = b.DefinirConfig("token_agente", *token)
	}
	if b.Config("token_agente") == "" {
		_ = b.DefinirConfig("token_agente", servidor.NovoToken())
	}
	if b.QtdAdmins() == 0 {
		senha := servidor.NovaSenha()
		if err := b.CriarAdmin("admin", senha); err != nil {
			log.Fatalf("erro ao criar administrador: %v", err)
		}
		arq := filepath.Join(filepath.Dir(*banco), "senha-admin-inicial.txt")
		_ = os.WriteFile(arq, []byte("Usuário: admin\r\nSenha: "+senha+"\r\n\r\nTroque a senha em Administração e apague este arquivo.\r\n"), 0o600)
		log.Printf("=====================================================")
		log.Printf(" Primeiro acesso -> usuário: admin   senha: %s", senha)
		log.Printf(" (também salvo em %s)", arq)
		log.Printf("=====================================================")
	}

	srv, err := servidor.Novo(b, versao)
	if err != nil {
		log.Fatalf("erro ao preparar o servidor: %v", err)
	}
	// backup automático diário com retenção (14 dias) na pasta "backups" ao lado do banco
	srv.BackupAutomatico(filepath.Join(filepath.Dir(*banco), "backups"), 14)
	hs := &http.Server{
		Addr:              fmt.Sprintf(":%d", *porta),
		Handler:           srv.Rotas(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
	}
	esquema := "http"
	if *cert != "" {
		esquema = "https"
	}
	log.Printf("Inventário v%s — painel em %s://localhost:%d  (banco: %s)", versao, esquema, *porta, *banco)
	log.Printf("Token dos agentes: %s", b.Config("token_agente"))
	if *cert != "" {
		err = hs.ListenAndServeTLS(*cert, *chave)
	} else {
		err = hs.ListenAndServe()
	}
	log.Fatal(err)
}

// logRotativo grava no arquivo de log e, quando ele passa de "max" bytes, renomeia para
// .1, .2, ... mantendo no máximo "manter" arquivos antigos. Evita o log crescer sem limite.
type logRotativo struct {
	caminho string
	max     int64
	manter  int
	mu      sync.Mutex
	f       *os.File
	tam     int64
}

func (l *logRotativo) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		f, err := os.OpenFile(l.caminho, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return len(p), nil // não derruba o servidor por causa de log
		}
		l.f = f
		if st, err := f.Stat(); err == nil {
			l.tam = st.Size()
		}
	}
	if l.tam+int64(len(p)) > l.max {
		l.f.Close()
		for i := l.manter; i >= 1; i-- {
			antigo := fmt.Sprintf("%s.%d", l.caminho, i)
			if i == l.manter {
				_ = os.Remove(antigo)
			} else {
				_ = os.Rename(antigo, fmt.Sprintf("%s.%d", l.caminho, i+1))
			}
		}
		_ = os.Rename(l.caminho, l.caminho+".1")
		f, err := os.OpenFile(l.caminho, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			l.f = nil
			return len(p), nil
		}
		l.f, l.tam = f, 0
	}
	n, err := l.f.Write(p)
	l.tam += int64(n)
	return n, err
}
