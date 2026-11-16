package servidor

import (
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// Anexos de chamados: guardados dentro do banco (BLOB), para entrarem automaticamente no
// backup diário e na migração — sem uma pasta extra para lembrar de copiar.

const (
	maxAnexo          = 10 << 20 // 10 MB por arquivo
	maxAnexosPorEnvio = 5
	maxCorpoUpload    = maxAnexosPorEnvio*maxAnexo + (2 << 20)
)

// Extensões que nunca são aceitas (executáveis e scripts).
var extensoesBloqueadas = map[string]bool{
	".exe": true, ".msi": true, ".bat": true, ".cmd": true, ".com": true, ".scr": true, ".pif": true,
	".ps1": true, ".psm1": true, ".vbs": true, ".vbe": true, ".js": true, ".jse": true, ".wsf": true,
	".wsh": true, ".hta": true, ".lnk": true, ".dll": true, ".jar": true, ".reg": true, ".cpl": true,
	".msp": true, ".appx": true, ".msix": true, ".iso": true, ".img": true, ".vhd": true, ".vhdx": true,
	".html": true, ".htm": true, ".svg": true,
}

// Tipos exibidos direto no navegador; o resto é sempre baixado.
var tiposInline = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

type Anexo struct {
	ID, ChamadoID, ComentarioID int64
	Nome, Tipo, Autor, CriadoEm string
	Tamanho                     int64
	Interno                     bool // pertence a uma nota interna
}

func (a Anexo) EhImagem() bool { return tiposInline[a.Tipo] }
func (a Anexo) TamanhoTexto() string {
	switch {
	case a.Tamanho < 1024:
		return fmt.Sprintf("%d B", a.Tamanho)
	case a.Tamanho < 1<<20:
		return fmt.Sprintf("%d KB", (a.Tamanho+512)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(a.Tamanho)/(1<<20))
	}
}

type arquivoEnviado struct {
	nome, tipo string
	dados      []byte
}

// limitarUpload envolve as rotas que recebem anexos, antes do parse do formulário.
// Se o envio passar do limite, volta para a página de origem com uma mensagem clara
// (em vez do erro genérico de CSRF que apareceria com o formulário cortado).
func limitarUpload(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxCorpoUpload)
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			if err := r.ParseMultipartForm(16 << 20); err != nil {
				volta := "/"
				if u, e := url.Parse(r.Referer()); e == nil && strings.HasPrefix(u.Path, "/") {
					volta = u.Path
				}
				redirecionar(w, r, volta, "", fmt.Errorf("os anexos passam do limite (%d arquivos de até %d MB cada)", maxAnexosPorEnvio, maxAnexo>>20))
				return
			}
		}
		h(w, r)
	}
}

// lerAnexos lê os arquivos do campo "anexos" e valida quantidade, tamanho e tipo.
func lerAnexos(r *http.Request) ([]arquivoEnviado, error) {
	if r.MultipartForm == nil {
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			if strings.Contains(err.Error(), "too large") {
				return nil, fmt.Errorf("os anexos passam do limite (%d arquivos de até %d MB)", maxAnexosPorEnvio, maxAnexo>>20)
			}
			if err == http.ErrNotMultipart {
				return nil, nil
			}
			return nil, fmt.Errorf("não foi possível ler o formulário: %v", err)
		}
	}
	var cabecalhos []*multipart.FileHeader
	for _, fh := range r.MultipartForm.File["anexos"] {
		if fh.Size > 0 && fh.Filename != "" {
			cabecalhos = append(cabecalhos, fh)
		}
	}
	if len(cabecalhos) > maxAnexosPorEnvio {
		return nil, fmt.Errorf("envie no máximo %d arquivos por vez", maxAnexosPorEnvio)
	}
	var l []arquivoEnviado
	for _, fh := range cabecalhos {
		nome := nomeArquivoSeguro(fh.Filename)
		if extensoesBloqueadas[strings.ToLower(filepath.Ext(nome))] {
			return nil, fmt.Errorf("o arquivo %q não é permitido (executáveis, scripts e páginas são bloqueados; compacte em .zip se precisar)", nome)
		}
		if fh.Size > maxAnexo {
			return nil, fmt.Errorf("o arquivo %q passa de %d MB", nome, maxAnexo>>20)
		}
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		dados, err := io.ReadAll(io.LimitReader(f, maxAnexo+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(dados) > maxAnexo {
			return nil, fmt.Errorf("o arquivo %q passa de %d MB", nome, maxAnexo>>20)
		}
		// o tipo vem do conteúdo, não do que o navegador informou
		tipo := http.DetectContentType(dados)
		if i := strings.Index(tipo, ";"); i >= 0 {
			tipo = strings.TrimSpace(tipo[:i])
		}
		l = append(l, arquivoEnviado{nome: nome, tipo: tipo, dados: dados})
	}
	return l, nil
}

func nomeArquivoSeguro(n string) string {
	n = filepath.Base(strings.ReplaceAll(n, `\`, "/"))
	n = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, n)
	n = strings.TrimSpace(strings.Trim(n, "."))
	if n == "" {
		n = "arquivo"
	}
	if r := []rune(n); len(r) > 120 {
		ext := filepath.Ext(n)
		n = string(r[:120-len([]rune(ext))]) + ext
	}
	return n
}

func (s *Servidor) salvarAnexos(chamadoID, comentarioID int64, arquivos []arquivoEnviado, autor string) {
	for _, a := range arquivos {
		if err := s.b.SalvarAnexo(chamadoID, comentarioID, a.nome, a.tipo, a.dados, autor); err != nil {
			log.Printf("anexo do chamado #%d (%s): %v", chamadoID, a.nome, err)
			continue
		}
		s.b.RegistrarEvento(chamadoID, autor, "anexo", a.nome)
	}
}

// ---------------- Banco ----------------

func (b *Banco) SalvarAnexo(chamadoID, comentarioID int64, nome, tipo string, dados []byte, autor string) error {
	_, err := b.db.Exec(`INSERT INTO chamado_anexos(chamado_id,comentario_id,nome,tipo,tamanho,dados,autor,criado_em) VALUES(?,?,?,?,?,?,?,?)`,
		chamadoID, nullID(comentarioID), nome, tipo, len(dados), dados, autor, agora())
	return err
}

// AnexosChamado devolve os metadados (sem o conteúdo) dos anexos do chamado.
func (b *Banco) AnexosChamado(id int64) ([]Anexo, error) {
	rows, err := b.db.Query(`SELECT a.id, a.chamado_id, COALESCE(a.comentario_id,0), a.nome, a.tipo, a.tamanho, a.autor, a.criado_em,
		COALESCE(cc.interno,0)
		FROM chamado_anexos a LEFT JOIN chamado_comentarios cc ON cc.id=a.comentario_id
		WHERE a.chamado_id=? ORDER BY a.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var l []Anexo
	for rows.Next() {
		var a Anexo
		var interno int
		if rows.Scan(&a.ID, &a.ChamadoID, &a.ComentarioID, &a.Nome, &a.Tipo, &a.Tamanho, &a.Autor, &a.CriadoEm, &interno) == nil {
			a.Interno = interno == 1
			l = append(l, a)
		}
	}
	return l, rows.Err()
}

func (b *Banco) Anexo(chamadoID, anexoID int64) (*Anexo, []byte, error) {
	var a Anexo
	var interno int
	var dados []byte
	err := b.db.QueryRow(`SELECT a.id, a.chamado_id, COALESCE(a.comentario_id,0), a.nome, a.tipo, a.tamanho, a.autor, a.criado_em,
		COALESCE(cc.interno,0), a.dados
		FROM chamado_anexos a LEFT JOIN chamado_comentarios cc ON cc.id=a.comentario_id
		WHERE a.id=? AND a.chamado_id=?`, anexoID, chamadoID).
		Scan(&a.ID, &a.ChamadoID, &a.ComentarioID, &a.Nome, &a.Tipo, &a.Tamanho, &a.Autor, &a.CriadoEm, &interno, &dados)
	if err != nil {
		return nil, nil, err
	}
	a.Interno = interno == 1
	return &a, dados, nil
}

// servirAnexo entrega o arquivo com cabeçalhos seguros: imagens abrem no navegador,
// o resto é sempre baixado; nada é interpretado como página.
func servirAnexo(w http.ResponseWriter, r *http.Request, a *Anexo, dados []byte) {
	disp := "attachment"
	tipo := "application/octet-stream"
	if tiposInline[a.Tipo] {
		disp, tipo = "inline", a.Tipo
	}
	if r.URL.Query().Get("baixar") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", tipo)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`,
		disp, strings.ReplaceAll(asciiSeguro(a.Nome), `"`, ""), url.PathEscape(a.Nome)))
	w.Header().Set("Content-Length", strconv.Itoa(len(dados)))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(dados)
}

func asciiSeguro(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return '_'
		}
		return r
	}, s)
}

// baixarAnexo (área logada): respeita o acesso ao chamado e esconde anexos de notas
// internas de quem não é da TI.
func (s *Servidor) baixarAnexo(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	c := s.chamadoDaURL(w, r, sess)
	if c == nil {
		return
	}
	a, dados, err := s.b.Anexo(c.ID, parseID(r.PathValue("aid")))
	if err != nil || (a.Interno && !sess.Admin.PodeGerenciarChamados()) {
		http.NotFound(w, r)
		return
	}
	servirAnexo(w, r, a, dados)
}
