package servidor

// Diagnóstico do auto-update (1.11): o servidor anota, por máquina, quando ofereceu a versão
// nova, quando o .exe foi baixado (pelo IP) e o erro que o agente informar (agente 1.11+).
// Com isso a tela mostra em que etapa cada máquina parou, sem precisar abrir o agente.log.

import (
	"fmt"
	"strings"
	"time"
)

// RegistrarSituacaoAutoUpdate é chamado a cada relatório recebido.
func (b *Banco) RegistrarSituacaoAutoUpdate(maquinaID int64, ofertou bool, versao, erroAgente string) {
	t := agora()
	if ofertou {
		// conta as ofertas da MESMA versão; versão nova zera a contagem e o download anterior
		_, _ = b.db.Exec(`UPDATE maquinas SET
			au_ofertas = CASE WHEN au_oferta_versao=? THEN au_ofertas+1 ELSE 1 END,
			au_download_em = CASE WHEN au_oferta_versao=? THEN au_download_em ELSE '' END,
			au_oferta_versao=?, au_oferta_em=?, au_erro=? WHERE id=?`, versao, versao, versao, t, erroAgente, maquinaID)
		return
	}
	_, _ = b.db.Exec(`UPDATE maquinas SET au_ofertas=0, au_erro=? WHERE id=?`, erroAgente, maquinaID)
}

// RegistrarDownloadAgente anota o download do agente na máquina que tem esse IP.
func (b *Banco) RegistrarDownloadAgente(ip string) string {
	var id int64
	var host string
	if err := b.db.QueryRow(`SELECT id, hostname FROM maquinas WHERE ip_origem=? ORDER BY ultimo_contato DESC LIMIT 1`, ip).Scan(&id, &host); err != nil {
		return "máquina não identificada"
	}
	_, _ = b.db.Exec(`UPDATE maquinas SET au_download_em=? WHERE id=?`, agora(), id)
	return host
}

type DiagAutoUpdate struct {
	ID            int64
	Hostname      string
	Versao        string
	UltimoContato string
	Ofertas       int
	OfertaEm      string
	DownloadEm    string
	Erro          string
	Etapa         string // resumo curto
	Texto         string // o que fazer
	Grave         bool
}

// DiagnosticoAutoUpdate lista as máquinas com agente anterior ao do servidor e explica em que
// etapa cada uma parou.
func (b *Banco) DiagnosticoAutoUpdate(versaoAtual string, ligado, payloadOK bool) []DiagAutoUpdate {
	rows, err := b.db.Query(`SELECT id, hostname, versao_agente, ultimo_contato, au_ofertas, au_oferta_em, au_oferta_versao, au_download_em, au_erro
		FROM maquinas WHERE versao_agente<>'' ORDER BY ultimo_contato DESC`)
	if err != nil {
		return nil
	}
	var todas []DiagAutoUpdate
	var versoesOferta []string
	for rows.Next() {
		var d DiagAutoUpdate
		var vOferta string
		if rows.Scan(&d.ID, &d.Hostname, &d.Versao, &d.UltimoContato, &d.Ofertas, &d.OfertaEm, &vOferta, &d.DownloadEm, &d.Erro) != nil {
			continue
		}
		todas = append(todas, d)
		versoesOferta = append(versoesOferta, vOferta)
	}
	rows.Close()
	recusadas := map[string]bool{}
	limite := time.Now().AddDate(0, 0, -7).UTC().Format(time.RFC3339)
	if rr, err := b.db.Query(`SELECT hostname FROM tokens_recusados WHERE ultimo>=? AND hostname<>''`, limite); err == nil {
		for rr.Next() {
			var h string
			if rr.Scan(&h) == nil {
				recusadas[strings.ToUpper(h)] = true
			}
		}
		rr.Close()
	}
	var r []DiagAutoUpdate
	for i, d := range todas {
		if !versaoMenor(d.Versao, versaoAtual) {
			continue
		}
		contato, _ := parseData(d.UltimoContato)
		download, temDownload := parseData(d.DownloadEm)
		if versoesOferta[i] != versaoAtual {
			d.Ofertas, temDownload = 0, false
		}
		switch {
		case !ligado:
			d.Etapa, d.Texto = "Auto-update desligado", "Ligue o auto-update acima."
			d.Grave = true
		case !payloadOK:
			d.Etapa, d.Texto = "Servidor sem o agente novo", "Este servidor foi compilado sem o agente da versão anunciada (use compilar.sh/compilar.ps1 ou o .exe entregue)."
			d.Grave = true
		case recusadas[strings.ToUpper(d.Hostname)]:
			d.Etapa = "Token recusado"
			d.Texto = "A máquina está tentando enviar com um token antigo e o servidor recusa (401) — por isso não recebe a atualização. Aceite o token em Administração → Token dos agentes."
			d.Grave = true
		case d.Erro != "":
			d.Etapa, d.Texto = "O agente informou um erro", d.Erro
			d.Grave = true
		case time.Since(contato) > 48*time.Hour:
			d.Etapa = "Sem contato"
			d.Texto = "A máquina não manda inventário desde " + formatarData(d.UltimoContato) + " — está desligada, fora da rede, ou a tarefa agendada \\Inventario\\Agente parou. Sem contato não há como receber a atualização."
			d.Grave = true
		case d.Ofertas == 0:
			d.Etapa = "Aguardando contato"
			d.Texto = "Ainda não mandou inventário desde que este servidor passou a oferecer a v" + versaoAtual + ". Acontece no próximo envio (até 1 h, no logon ou ao ligar)."
		case !temDownload:
			d.Etapa = fmt.Sprintf("Oferecida %d vez(es), nunca baixou", d.Ofertas)
			d.Texto = "O agente recebeu a ordem e não baixou o arquivo. Causa mais comum: agente instalado fora de C:\\Program Files\\Inventario (instalação antiga/manual) — nesse caso ele ignora a atualização. Rode o REINSTALAR nessa máquina uma vez; dali em diante o auto-update funciona."
			d.Grave = d.Ofertas >= 2
		case contato.After(download.Add(5*time.Minute)) && d.Ofertas >= 2:
			d.Etapa = "Baixou, mas não trocou"
			d.Texto = "Baixou a v" + versaoAtual + " em " + formatarData(d.DownloadEm) + " e continua na antiga. A troca do .exe foi bloqueada — quase sempre o antivírus (libere C:\\Program Files\\Inventario e C:\\ProgramData\\Inventario no Bitdefender) — ou a tarefa ainda roda outro executável."
			d.Grave = true
		default:
			d.Etapa = "Baixou — trocando"
			d.Texto = "Baixou em " + formatarData(d.DownloadEm) + ". A versão nova passa a valer na próxima execução da tarefa (até 1 h, no logon ou ao ligar)."
		}
		r = append(r, d)
	}
	return r
}
