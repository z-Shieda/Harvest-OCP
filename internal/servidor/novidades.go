package servidor

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// Avisos de chamado novo no navegador (1.10): a página consulta este endereço a cada ~20 s.
// Devolve o maior número de chamado e os chamados novos desde o último que o navegador viu.

type novidadeChamado struct {
	ID          int64  `json:"id"`
	Titulo      string `json:"titulo"`
	Solicitante string `json:"solicitante"`
	Prioridade  string `json:"prioridade"`
	ParaMim     bool   `json:"para_mim"`
}

func (s *Servidor) apiNovidadesChamados(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if !sess.Admin.PodeGerenciarChamados() {
		_ = json.NewEncoder(w).Encode(map[string]any{"ultimo": 0, "novos": []any{}})
		return
	}
	desde, _ := strconv.ParseInt(r.URL.Query().Get("desde"), 10, 64)
	var ultimo int64
	_ = s.b.db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM chamados`).Scan(&ultimo)
	novos := []novidadeChamado{}
	if desde > 0 && ultimo > desde {
		rows, err := s.b.db.Query(`SELECT c.id, c.titulo, c.prioridade, COALESCE(c.atribuido_a,0),
			CASE WHEN c.solicitante_nome<>'' THEN c.solicitante_nome ELSE COALESCE(CASE WHEN a.nome<>'' THEN a.nome ELSE a.usuario END,'') END
			FROM chamados c LEFT JOIN admins a ON a.id=c.criado_por WHERE c.id>? ORDER BY c.id LIMIT 10`, desde)
		if err == nil {
			for rows.Next() {
				var n novidadeChamado
				var atrib int64
				if rows.Scan(&n.ID, &n.Titulo, &n.Prioridade, &atrib, &n.Solicitante) == nil {
					n.ParaMim = atrib == sess.Admin.ID
					n.Prioridade = rotuloPrioridade(n.Prioridade)
					novos = append(novos, n)
				}
			}
			rows.Close()
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ultimo": ultimo, "novos": novos, "abertos": s.b.ResumoChamados()})
}
