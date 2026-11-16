package servidor

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// acaoAtualizar recebe um novo executável do servidor (enviado pelo painel), valida,
// guarda uma cópia do atual e troca os arquivos, reiniciando o serviço em seguida.
func (s *Servidor) acaoAtualizar(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		redirecionar(w, r, "/admin", "", fmt.Errorf("envio inválido: %v", err))
		return
	}
	arq, cab, err := r.FormFile("arquivo")
	if err != nil {
		redirecionar(w, r, "/admin", "", fmt.Errorf("selecione o arquivo do novo servidor (.exe)"))
		return
	}
	defer arq.Close()

	dados, err := io.ReadAll(io.LimitReader(arq, 200<<20))
	if err != nil {
		redirecionar(w, r, "/admin", "", err)
		return
	}
	if err := validarExecutavel(dados); err != nil {
		redirecionar(w, r, "/admin", "", err)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		redirecionar(w, r, "/admin", "", err)
		return
	}
	novo := exe + ".novo"
	if err := os.WriteFile(novo, dados, 0o755); err != nil {
		redirecionar(w, r, "/admin", "", fmt.Errorf("não foi possível gravar o arquivo (permissão de administrador?): %v", err))
		return
	}

	s.b.Auditar(sess.Admin.Usuario, "atualizar servidor", fmt.Sprintf("%s (%d KB)", cab.Filename, len(dados)/1024))
	log.Printf("atualização enviada por %s: %s (%d bytes) — aplicando e reiniciando", sess.Admin.Usuario, cab.Filename, len(dados))

	// Responde antes de reiniciar, para o navegador não ficar esperando uma conexão que cai.
	s.render(w, r, "atualizando", "Atualizando", "admin", sess, map[string]any{"URL": s.baseURL(r)})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {
		time.Sleep(2 * time.Second)
		if err := reiniciarComNovo(novo); err != nil {
			log.Printf("ERRO ao aplicar atualização: %v", err)
			_ = os.Remove(novo)
		}
	}()
}

// validarExecutavel faz uma checagem básica de que o arquivo é mesmo um executável do Windows (PE).
func validarExecutavel(b []byte) error {
	if len(b) < 64 || b[0] != 'M' || b[1] != 'Z' {
		return fmt.Errorf("o arquivo não parece um executável do Windows (.exe)")
	}
	if len(b) < 1<<20 {
		return fmt.Errorf("o arquivo é pequeno demais para ser o servidor; confira se escolheu o inventario-servidor.exe certo")
	}
	return nil
}

// caminhoBackupServidor devolve onde fica a cópia da versão anterior.
func caminhoBackupServidor() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "inventario-servidor.antigo.exe")
}
