package servidor

// Programa monitorado (1.15, ex.: Work Monitor). Configurado em Configurações; o servidor manda o
// "alvo" para os agentes na resposta de cada envio e guarda o resultado na máquina:
// rodando · parado (usuário logado e o programa não está aberto) · instalado (ninguém logado)
// · sem (não instalado) · "" (ainda não verificado).

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"inventario/internal/modelo"
)

type ConfigMonitor struct {
	Ativo                                     bool
	Nome, CaminhosPerfil, CaminhosFixos, Proc string
}

func linhas(s string) []string {
	var r []string
	for _, l := range strings.FieldsFunc(s, func(c rune) bool { return c == '\n' || c == ',' || c == ';' }) {
		if l = strings.TrimSpace(l); l != "" {
			r = append(r, l)
		}
	}
	return r
}

// ConfigMonitor: na primeira vez já vem com o Work Monitor (identificado na máquina do Daniel:
// %AppData%\spa\work.exe, assinado pela SMART Computadores e Serviços).
func (b *Banco) ConfigMonitor() ConfigMonitor {
	if b.Config("monitor_configurado") != "1" {
		return ConfigMonitor{Ativo: true, Nome: "Work Monitor", CaminhosPerfil: `AppData\Roaming\spa\work.exe`, Proc: "work.exe"}
	}
	return ConfigMonitor{
		Ativo: b.Config("monitor_ativo") == "1", Nome: b.Config("monitor_nome"),
		CaminhosPerfil: b.Config("monitor_caminhos"), CaminhosFixos: b.Config("monitor_fixos"), Proc: b.Config("monitor_processos"),
	}
}

// Alvo: o que vai para os agentes (vazio = desligado, o agente apaga o que tinha).
func (c ConfigMonitor) Alvo() *modelo.AlvoMonitor {
	if !c.Ativo || strings.TrimSpace(c.Nome) == "" {
		return &modelo.AlvoMonitor{}
	}
	return &modelo.AlvoMonitor{Nome: strings.TrimSpace(c.Nome), CaminhosPerfil: linhas(c.CaminhosPerfil),
		CaminhosFixos: linhas(c.CaminhosFixos), Processos: linhas(c.Proc)}
}

func rotuloMonitor(sit string) string {
	switch sit {
	case "rodando":
		return "instalado e rodando"
	case "parado":
		return "instalado, mas NÃO está rodando"
	case "instalado":
		return "instalado (ninguém logado agora)"
	case "sem":
		return "não instalado"
	}
	return "ainda não verificado"
}

// monitorAusente: situações que pedem atenção.
func monitorAusente(sit string) bool { return sit == "sem" || sit == "parado" }

// GravarMonitor guarda o resultado da verificação enviada pelo agente.
func (b *Banco) GravarMonitor(maquinaID int64, rel *modelo.Relatorio) {
	m := rel.Monitor
	if m == nil {
		return
	}
	logado := strings.TrimSpace(rel.UsuarioAtual) != "" || len(rel.UsuariosSessao) > 0
	sit := "sem"
	switch {
	case len(m.Rodando) > 0:
		sit = "rodando"
	case len(m.Instalacoes) > 0 && logado:
		sit = "parado"
	case len(m.Instalacoes) > 0:
		sit = "instalado"
	}
	var det []string
	for _, i := range m.Instalacoes {
		d := "instalado"
		if i.Perfil != "" {
			d += " para " + i.Perfil
		}
		if t, err := time.Parse(time.RFC3339, i.Modificado); err == nil {
			d += fmt.Sprintf(" (arquivo de %s, %s)", t.Local().Format("02/01/2006"), formatarTamanho(uint64(i.Tamanho)))
		}
		det = append(det, d)
	}
	for _, p := range m.Rodando {
		d := "rodando: " + p.Nome
		if p.Caminho != "" {
			d = "rodando: " + p.Caminho
		}
		det = append(det, d)
	}
	_, _ = b.db.Exec(`UPDATE maquinas SET monitor_situacao=?, monitor_nome=?, monitor_detalhe=? WHERE id=?`,
		sit, m.Nome, strings.Join(det, " · "), maquinaID)
}

func (s *Servidor) acaoConfigMonitor(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	antes := s.b.ConfigMonitor()
	ativo := "0"
	if r.FormValue("ativo") == "1" {
		ativo = "1"
	}
	nome := strings.TrimSpace(r.FormValue("nome"))
	if ativo == "1" && nome == "" {
		redirecionar(w, r, "/configuracoes#monitor", "", fmt.Errorf("informe o nome do programa"))
		return
	}
	for k, v := range map[string]string{
		"monitor_configurado": "1", "monitor_ativo": ativo, "monitor_nome": nome,
		"monitor_caminhos": strings.TrimSpace(r.FormValue("caminhos")), "monitor_fixos": strings.TrimSpace(r.FormValue("fixos")),
		"monitor_processos": strings.TrimSpace(r.FormValue("processos")),
	} {
		_ = s.b.DefinirConfig(k, v)
	}
	depois := s.b.ConfigMonitor()
	if fmt.Sprint(antes.Alvo()) != fmt.Sprint(depois.Alvo()) {
		// o que estava gravado nas máquinas era de outra regra: volta para "não verificado"
		_, _ = s.b.db.Exec(`UPDATE maquinas SET monitor_situacao='', monitor_detalhe=''`)
	}
	s.b.Auditar(sess.Admin.Usuario, "programa monitorado", fmt.Sprintf("%s (ativo=%s)", nome, ativo))
	redirecionar(w, r, "/configuracoes#monitor", "Programa monitorado salvo. Cada máquina confere no próximo envio (até 1 h, no logon ou ao ligar).", nil)
}
