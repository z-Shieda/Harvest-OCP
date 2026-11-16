package servidor

// AnyDesk (1.14): o agente lê o ID do AnyDesk (system.conf) e o servidor guarda na máquina.
// Situação: "ok" (instalado como serviço), "portatil" (só a versão portátil, ID pode mudar),
// "sem_id" (instalado, mas o ID não foi lido), "sem" (não instalado), "" (agente antigo).
// Ao abrir um chamado para a máquina, o ID vai junto.

import (
	"strings"

	"inventario/internal/modelo"
)

// formatarAnyDesk: "123456789" → "123 456 789".
func formatarAnyDesk(id string) string {
	id = strings.TrimSpace(id)
	if len(id) < 4 {
		return id
	}
	var b strings.Builder
	for i, c := range id {
		if i > 0 && i%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func rotuloAnyDesk(situacao string) string {
	switch situacao {
	case "ok":
		return "instalado"
	case "portatil":
		return "só portátil (não instalado)"
	case "sem_id":
		return "instalado, ID não encontrado"
	case "sem":
		return "não instalado"
	}
	return "ainda não verificado (agente antigo)"
}

// AnyDeskAusente: situações que pedem atenção.
func anyDeskAusente(situacao string) bool {
	return situacao == "sem" || situacao == "portatil" || situacao == "sem_id"
}

// GravarAnyDesk atualiza o AnyDesk da máquina a partir do relatório do agente.
func (b *Banco) GravarAnyDesk(maquinaID int64, rel *modelo.Relatorio) {
	if !rel.AnyDeskVerificado {
		return // agente sem a coleta (antes da 1.14): não mexe
	}
	var principal *modelo.AnyDesk
	for i := range rel.AnyDesk {
		if rel.AnyDesk[i].Tipo == "servico" {
			principal = &rel.AnyDesk[i]
			break
		}
	}
	if principal == nil && len(rel.AnyDesk) > 0 {
		principal = &rel.AnyDesk[0]
	}
	situacao, id, alias := "sem", "", ""
	var det []string
	switch {
	case principal != nil && principal.Tipo == "servico":
		situacao = "ok"
	case principal != nil:
		situacao = "portatil"
		if rel.AnyDeskInstalado {
			situacao = "ok"
		}
	case rel.AnyDeskInstalado:
		situacao = "sem_id"
	}
	if principal != nil {
		id, alias = principal.ID, principal.Alias
	}
	for _, a := range rel.AnyDesk {
		d := formatarAnyDesk(a.ID)
		if a.Tipo == "portatil" {
			d += " (portátil, usuário " + a.Perfil + ")"
		}
		det = append(det, d)
	}
	_, _ = b.db.Exec(`UPDATE maquinas SET anydesk_id=?, anydesk_situacao=?, anydesk_alias=?, anydesk_detalhe=? WHERE id=?`,
		id, situacao, alias, strings.Join(det, " · "), maquinaID)
	if id != "" {
		// chamados em aberto desta máquina que ainda não tinham o AnyDesk
		_, _ = b.db.Exec(`UPDATE chamados SET anydesk_id=? WHERE maquina_id=? AND anydesk_id='' AND status NOT IN ('resolvido','fechado')`, id, maquinaID)
	}
}

// anexarAnyDesk grava no chamado o AnyDesk atual da máquina (ou limpa, se a máquina não tem).
func (b *Banco) anexarAnyDesk(chamadoID, maquinaID int64, autor string) {
	var id string
	if maquinaID > 0 {
		_ = b.db.QueryRow(`SELECT anydesk_id FROM maquinas WHERE id=?`, maquinaID).Scan(&id)
	}
	var antes string
	_ = b.db.QueryRow(`SELECT anydesk_id FROM chamados WHERE id=?`, chamadoID).Scan(&antes)
	if id == antes {
		return
	}
	_, _ = b.db.Exec(`UPDATE chamados SET anydesk_id=? WHERE id=?`, id, chamadoID)
	if id != "" {
		b.RegistrarEvento(chamadoID, autor, "anydesk", "AnyDesk da máquina: "+formatarAnyDesk(id))
	}
}
