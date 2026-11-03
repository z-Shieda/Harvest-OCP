package main

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"inventario/internal/modelo"
)

func tamanho(b uint64) string {
	const gb = 1024 * 1024 * 1024
	if b >= 1024*gb {
		return fmt.Sprintf("%.2f TB", float64(b)/(1024*gb))
	}
	return fmt.Sprintf("%.1f GB", float64(b)/gb)
}

func dataLocal(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return t.Local().Format("02/01/2006 15:04")
}

var tplRelatorio = template.Must(template.New("r").Funcs(template.FuncMap{"tam": tamanho, "data": dataLocal}).Parse(`<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><title>Inventário - {{.Hostname}}</title>
<style>
body{font-family:Segoe UI,Arial,sans-serif;margin:24px;color:#1f2937;background:#f8fafc}
h1{margin:0 0 4px} h2{margin-top:28px;font-size:18px;border-bottom:2px solid #2563eb;padding-bottom:4px}
table{border-collapse:collapse;width:100%;background:#fff} td,th{border:1px solid #e5e7eb;padding:6px 10px;text-align:left;font-size:14px}
th{background:#f1f5f9} .k{width:240px;color:#475569} .sub{color:#64748b}
</style></head><body>
<h1>{{.Hostname}}</h1><div class="sub">Coletado em {{data .ColetadoEm}} · agente {{.AgentVersion}} · ID {{.AgentID}}</div>
<h2>Sistema</h2><table>
<tr><td class="k">Fabricante / Modelo</td><td>{{.Fabricante}} {{.Modelo}}</td></tr>
<tr><td class="k">Número de série</td><td>{{.Serial}}</td></tr>
<tr><td class="k">UUID</td><td>{{.HWUUID}}</td></tr>
<tr><td class="k">BIOS</td><td>{{.BIOS}}</td></tr>
<tr><td class="k">Sistema operacional</td><td>{{.SOName}} {{.SOVersao}} (build {{.SOBuild}}) {{.SOArquitetura}}</td></tr>
<tr><td class="k">Instalado em / Último boot</td><td>{{data .SOInstalacao}} / {{data .UltimoBoot}}</td></tr>
<tr><td class="k">Domínio</td><td>{{.Dominio}}</td></tr>
<tr><td class="k">Processador</td><td>{{.CPU}} ({{.CPUNucleos}} núcleos / {{.CPUThreads}} threads)</td></tr>
<tr><td class="k">Memória RAM</td><td>{{tam .RAMBytes}}</td></tr>
<tr><td class="k">Vídeo</td><td>{{range .GPUs}}{{.}}<br>{{end}}</td></tr>
</table>
<h2>Usuários</h2><table>
<tr><td class="k">Usuário conectado agora</td><td>{{.UsuarioAtual}}</td></tr>
<tr><td class="k">Sessões ativas</td><td>{{range .UsuariosSessao}}{{.}}<br>{{end}}</td></tr>
<tr><td class="k">Último logon</td><td>{{.UltimoLogon}} {{if .UltimoLogonNome}}({{.UltimoLogonNome}}){{end}}</td></tr>
</table>
<h2>Perfis de usuário</h2><table><tr><th>Usuário</th><th>Pasta</th><th>Último uso</th></tr>
{{range .Perfis}}<tr><td>{{.Usuario}}</td><td>{{.Caminho}}</td><td>{{data .UltimoUso}}</td></tr>{{end}}</table>
<h2>Rede</h2><table><tr><th>Adaptador</th><th>MAC</th><th>IPv4</th><th>Conectada</th><th>Física</th></tr>
{{range .Redes}}<tr><td>{{.Nome}}</td><td>{{.MAC}}</td><td>{{range .IPv4}}{{.}} {{end}}</td><td>{{if .Conectada}}Sim{{else}}Não{{end}}</td><td>{{if .Fisica}}Sim{{else}}Não{{end}}</td></tr>{{end}}</table>
<h2>Discos</h2><table><tr><th>Modelo</th><th>Serial</th><th>Tipo</th><th>Tamanho</th></tr>
{{range .Discos}}<tr><td>{{.Modelo}}</td><td>{{.Serial}}</td><td>{{.Tipo}} {{.Barramento}}</td><td>{{tam .Tamanho}}</td></tr>{{end}}</table>
<h2>Volumes</h2><table><tr><th>Unidade</th><th>Rótulo</th><th>Sistema</th><th>Tamanho</th><th>Livre</th></tr>
{{range .Volumes}}<tr><td>{{.Letra}}</td><td>{{.Rotulo}}</td><td>{{.Sistema}}</td><td>{{tam .Tamanho}}</td><td>{{tam .Livre}}</td></tr>{{end}}</table>
<h2>Memória</h2><table><tr><th>Slot</th><th>Capacidade</th><th>Velocidade</th><th>Fabricante</th><th>Part number</th></tr>
{{range .Memorias}}<tr><td>{{.Slot}}</td><td>{{tam .Capacidade}}</td><td>{{.Velocidade}} MHz</td><td>{{.Fabricante}}</td><td>{{.PartNumber}}</td></tr>{{end}}</table>
</body></html>`))

// gerarRelatorioHTML grava o relatório na pasta temporária do usuário e devolve o caminho.
func gerarRelatorioHTML(rel *modelo.Relatorio) (string, error) {
	caminho := filepath.Join(os.TempDir(), "inventario_"+rel.Hostname+".html")
	f, err := os.Create(caminho)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return caminho, tplRelatorio.Execute(f, rel)
}
