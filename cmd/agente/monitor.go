package main

// Programa monitorado (1.15, ex.: Work Monitor). O servidor diz o que procurar (na resposta de
// cada envio) e o agente guarda em monitorar.json. A cada coleta, confere em cada perfil do
// Windows se o arquivo existe (instalado) e se o processo está rodando.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"inventario/internal/modelo"
)

func caminhoAlvoMonitor() string { return filepath.Join(dirDados(), "monitorar.json") }

func carregarAlvoMonitor() *modelo.AlvoMonitor {
	b, err := os.ReadFile(caminhoAlvoMonitor())
	if err != nil {
		return nil
	}
	var a modelo.AlvoMonitor
	if json.Unmarshal(b, &a) != nil || a.Nome == "" {
		return nil
	}
	return &a
}

// salvarAlvoMonitor grava o que o servidor pediu; devolve true se mudou.
func salvarAlvoMonitor(a *modelo.AlvoMonitor) bool {
	atual := carregarAlvoMonitor()
	if a == nil || a.Nome == "" {
		if atual == nil {
			return false
		}
		_ = os.Remove(caminhoAlvoMonitor())
		return true
	}
	if atual != nil && reflect.DeepEqual(*atual, *a) {
		return false
	}
	b, _ := json.MarshalIndent(a, "", "  ")
	return os.WriteFile(caminhoAlvoMonitor(), b, 0o644) == nil
}

func perfisDoWindows() []string {
	var r []string
	raiz := filepath.Join(os.Getenv("SystemDrive")+`\`, "Users")
	ents, err := os.ReadDir(raiz)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		switch strings.ToLower(e.Name()) {
		case "public", "default", "default user", "all users":
			continue
		}
		r = append(r, filepath.Join(raiz, e.Name()))
	}
	return r
}

func coletarMonitor(rel *modelo.Relatorio) {
	alvo := carregarAlvoMonitor()
	if alvo == nil {
		return
	}
	sit := &modelo.SituacaoMonitor{Nome: alvo.Nome}
	instalado := map[string]bool{}
	add := func(perfil, caminho string) {
		fi, err := os.Stat(caminho)
		if err != nil || fi.IsDir() || instalado[strings.ToLower(caminho)] {
			return
		}
		instalado[strings.ToLower(caminho)] = true
		sit.Instalacoes = append(sit.Instalacoes, modelo.InstalacaoMonitor{
			Perfil: perfil, Caminho: caminho, Tamanho: fi.Size(), Modificado: fi.ModTime().UTC().Format(time.RFC3339)})
	}
	for _, p := range perfisDoWindows() {
		for _, rel := range alvo.CaminhosPerfil {
			add(filepath.Base(p), filepath.Join(p, filepath.FromSlash(strings.ReplaceAll(rel, `\`, "/"))))
		}
	}
	for _, c := range alvo.CaminhosFixos {
		add("", os.ExpandEnv(c))
	}
	for _, pr := range listarProcessos() {
		casa := false
		for _, n := range alvo.Processos {
			if strings.EqualFold(pr.Nome, n) || strings.EqualFold(pr.Nome, n+".exe") {
				casa = true
			}
		}
		if pr.Caminho != "" && instalado[strings.ToLower(pr.Caminho)] {
			casa = true
		}
		if casa {
			sit.Rodando = append(sit.Rodando, pr)
		}
	}
	rel.Monitor = sit
}
