package servidor

// Identificação da máquina de onde a pessoa assina (1.23, D15) e segurança informada pelo
// agente 1.22 (D11).
//
// Na hora de "Enviar código", o servidor pergunta pelo SSE (evento "quem") a todas as máquinas
// conectadas quem está logado, e espera até 2 s. Agentes antigos ou sem SSE entram pelo último
// relatório, se tiver menos de 2 h. Com o servidor na rede local, o IP do navegador também
// aponta a máquina (MaquinaPorIP); com IP público (nuvem) o IP não é usado.

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"inventario/internal/modelo"
)

// ---------------- evento "quem" ----------------

type perguntasQuem struct {
	mu       sync.Mutex
	abertas  map[string]chan modelo.RespostaQuem
}

var quem = &perguntasQuem{abertas: map[string]chan modelo.RespostaQuem{}}

// perguntarQuem manda "quem" para todas as máquinas conectadas e junta as respostas até o prazo.
func (s *Servidor) perguntarQuem(prazo time.Duration) []modelo.RespostaQuem {
	id := aleatorio(8)
	ch := make(chan modelo.RespostaQuem, 256)
	quem.mu.Lock()
	quem.abertas[id] = ch
	quem.mu.Unlock()
	defer func() {
		quem.mu.Lock()
		delete(quem.abertas, id)
		quem.mu.Unlock()
	}()
	enviados := s.eventos.enviar("quem", modelo.EventoQuem{ID: id})
	var l []modelo.RespostaQuem
	if enviados == 0 {
		return l
	}
	limite := time.After(prazo)
	for len(l) < enviados {
		select {
		case r := <-ch:
			l = append(l, r)
		case <-limite:
			return l
		}
	}
	return l
}

// apiRespostaQuem recebe a resposta de um agente ao "quem".
func (s *Servidor) apiRespostaQuem(w http.ResponseWriter, r *http.Request) {
	if ok, _ := s.b.validarTokenAgente(r.Header.Get("X-Agente-Token")); !ok {
		http.Error(w, "token inválido", http.StatusUnauthorized)
		return
	}
	var resp modelo.RespostaQuem
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<10)).Decode(&resp); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}
	resp.Hostname = cortar(resp.Hostname, 100)
	if len(resp.Usuarios) > 50 {
		resp.Usuarios = resp.Usuarios[:50]
	}
	quem.mu.Lock()
	ch := quem.abertas[resp.ID]
	quem.mu.Unlock()
	if ch == nil {
		http.Error(w, "pergunta expirada", http.StatusGone)
		return
	}
	select {
	case ch <- resp:
	default:
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------- identificação ----------------

// MaquinaAssinatura: resultado gravado na assinatura.
type MaquinaAssinatura struct {
	ID         int64    `json:"id"`
	Nome       string   `json:"nome"`
	Grau       string   `json:"grau"` // confirmada | provavel | nao_identificada
	Candidatas []string `json:"candidatas,omitempty"`
	PeloIP     string   `json:"pelo_ip,omitempty"`
	Motivo     string   `json:"motivo,omitempty"`
}

func (m MaquinaAssinatura) Texto() string {
	if m.Nome == "" {
		t := "não identificada"
		if len(m.Candidatas) > 0 {
			t += " (candidatas: " + strings.Join(m.Candidatas, ", ") + ")"
		} else if m.Motivo != "" {
			t += " (" + m.Motivo + ")"
		}
		return t
	}
	return m.Nome + " (" + rotuloGrau(m.Grau) + ")"
}

func ipPrivado(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && (p.IsPrivate() || p.IsLoopback())
}

func mesmoLogin(a, b string) bool {
	a, b = chaveCurta(a), chaveCurta(b)
	return a != "" && strings.EqualFold(a, b)
}

// identificarMaquina cruza o login de rede da conta com quem está logado agora e com o IP.
func (s *Servidor) identificarMaquina(ipNavegador, loginRede string) MaquinaAssinatura {
	var porIP *maquinaIP
	if ipPrivado(ipNavegador) {
		porIP = s.b.MaquinaPorIP(ipNavegador)
	}
	res := MaquinaAssinatura{Grau: "nao_identificada"}
	if porIP != nil {
		res.PeloIP = porIP.Hostname
	}
	if strings.TrimSpace(loginRede) == "" {
		res.Motivo = "conta sem login de rede"
		if porIP != nil {
			res.Candidatas = []string{porIP.Hostname + " (pelo IP)"}
		}
		return res
	}
	logadas := map[string]int64{} // hostname -> id
	ids := s.b.idsPorHostname()
	respondeu := map[string]bool{}
	for _, r := range s.perguntarQuem(2 * time.Second) {
		h := strings.ToUpper(r.Hostname)
		respondeu[h] = true
		for _, u := range r.Usuarios {
			if mesmoLogin(u, loginRede) {
				logadas[h] = ids[h]
			}
		}
	}
	// quem não respondeu: último relatório com menos de 2 h
	limite := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	if rows, err := s.b.db.Query(`SELECT id, hostname, usuario_atual, dados_json FROM maquinas WHERE ultimo_contato>=? AND ignorada_em=''`, limite); err == nil {
		type cand struct {
			id       int64
			h, u, dj string
		}
		var cs []cand
		for rows.Next() {
			var c cand
			if rows.Scan(&c.id, &c.h, &c.u, &c.dj) == nil {
				cs = append(cs, c)
			}
		}
		rows.Close()
		for _, c := range cs {
			h := strings.ToUpper(c.h)
			if respondeu[h] {
				continue
			}
			achou := mesmoLogin(c.u, loginRede)
			if !achou {
				var rel struct {
					Sessao []string `json:"usuarios_sessao"`
				}
				_ = json.Unmarshal([]byte(c.dj), &rel)
				for _, u := range rel.Sessao {
					if mesmoLogin(u, loginRede) {
						achou = true
					}
				}
			}
			if achou {
				logadas[h] = c.id
			}
		}
	}
	nomes := make([]string, 0, len(logadas))
	for h := range logadas {
		nomes = append(nomes, h)
	}
	sort.Strings(nomes)
	nomeReal := func(h string) string {
		if m, err := s.b.Maquina(logadas[h]); err == nil {
			return m.Hostname
		}
		return h
	}
	switch {
	case porIP != nil && logadas[strings.ToUpper(porIP.Hostname)] != 0:
		res.ID, res.Nome, res.Grau = porIP.ID, porIP.Hostname, "confirmada"
	case len(logadas) == 1:
		res.ID, res.Nome, res.Grau = logadas[nomes[0]], nomeReal(nomes[0]), "provavel"
	default:
		for _, h := range nomes {
			res.Candidatas = append(res.Candidatas, nomeReal(h))
		}
		if porIP != nil {
			res.Candidatas = append(res.Candidatas, porIP.Hostname+" (pelo IP)")
		}
		if len(nomes) == 0 && porIP == nil {
			res.Motivo = loginRede + " não está logado em nenhuma máquina com o agente"
		}
	}
	return res
}

// ---------------- segurança (agente 1.22) ----------------

// GravarSeguranca guarda o que o agente detectou (só agentes 1.22+ mandam).
func (b *Banco) GravarSeguranca(maquinaID int64, rel *modelo.Relatorio) {
	sg := rel.Seguranca
	if sg == nil || maquinaID == 0 {
		return
	}
	av, avOK := "", ""
	if sg.AntivirusLido {
		var nomes []string
		ok := false
		for _, a := range sg.Antivirus {
			n := a.Nome
			switch {
			case a.Ativo && a.Atualizado:
				ok = true
				n += " (ativo, atualizado)"
			case a.Ativo:
				n += " (ativo, DESATUALIZADO)"
			default:
				n += " (desligado)"
			}
			nomes = append(nomes, n)
		}
		av = strings.Join(nomes, "; ")
		if av == "" {
			av = "nenhum antivírus registrado"
		}
		avOK = "nao"
		if ok {
			avOK = "sim"
		}
	}
	admins := ""
	if sg.AdminsLidos {
		admins = strings.Join(sg.AdminsLocais, ", ")
		if admins == "" {
			admins = "(nenhum)"
		}
	}
	_, _ = b.db.Exec(`UPDATE maquinas SET seg_bitlocker=?, seg_antivirus=?, seg_antivirus_ok=?, seg_admins_locais=?, seg_ultima_atualizacao=?, seg_coletado_em=? WHERE id=?`,
		sg.BitLocker, cortar(av, 400), avOK, cortar(admins, 600), sg.UltimaAtualizacao, agora(), maquinaID)
}
