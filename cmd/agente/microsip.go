package main

// Coleta do MicroSIP (softphone): lê o microsip.ini de cada perfil do Windows
// (%APPDATA%\MicroSIP) e de instalações portáteis conhecidas, pegando as contas SIP
// (ramal, nome, servidor — NUNCA a senha). O histórico de chamadas NÃO é lido (decisão 1.11:
// ler o call_log.db exigiria embutir o SQLite e aumentaria o agente de ~8 para ~13 MB).

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"inventario/internal/modelo"
)

// lerTextoINI decodifica o .ini (o MicroSIP grava em UTF-16 LE com BOM; versões antigas em ANSI/UTF-8).
func lerTextoINI(b []byte) string {
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return decodificarUTF16(b[2:], false)
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return decodificarUTF16(b[2:], true)
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:])
	}
	return string(b)
}

func decodificarUTF16(b []byte, be bool) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if be {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return string(utf16.Decode(u))
}

// lerINI devolve seção → chave → valor (chaves em minúsculas).
func lerINI(texto string) map[string]map[string]string {
	r := map[string]map[string]string{}
	secao := ""
	sc := bufio.NewScanner(strings.NewReader(texto))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || l[0] == ';' || l[0] == '#' {
			continue
		}
		if l[0] == '[' && strings.HasSuffix(l, "]") {
			secao = strings.TrimSpace(l[1 : len(l)-1])
			if r[secao] == nil {
				r[secao] = map[string]string{}
			}
			continue
		}
		if i := strings.IndexByte(l, '='); i > 0 && secao != "" {
			v := strings.TrimSpace(l[i+1:])
			if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
				v = v[1 : len(v)-1]
			}
			r[secao][strings.ToLower(strings.TrimSpace(l[:i]))] = v
		}
	}
	return r
}

// contasDoINI extrai as contas SIP (ignora a senha de propósito).
func contasDoINI(ini map[string]map[string]string) []modelo.ContaSIP {
	var nomes []string
	for s := range ini {
		if strings.HasPrefix(strings.ToLower(s), "account") {
			nomes = append(nomes, s)
		}
	}
	sort.Strings(nomes)
	var r []modelo.ContaSIP
	for _, s := range nomes {
		c := ini[s]
		conta := modelo.ContaSIP{
			Secao: s, Rotulo: c["label"], Usuario: c["username"], AuthID: c["authid"],
			Nome: c["displayname"], Servidor: c["server"], Dominio: c["domain"],
		}
		if conta.Usuario == "" && conta.AuthID == "" {
			continue
		}
		r = append(r, conta)
	}
	return r
}

// lerMicroSIP lê uma pasta que contém microsip.ini.
func lerMicroSIP(perfil, dir string) *modelo.MicroSIP {
	var ini string
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.EqualFold(e.Name(), "microsip.ini") {
			ini = filepath.Join(dir, e.Name())
		}
	}
	if ini == "" {
		return nil
	}
	b, err := os.ReadFile(ini)
	if err != nil {
		return &modelo.MicroSIP{Perfil: perfil, Caminho: dir, Erro: "não foi possível ler o microsip.ini: " + err.Error()}
	}
	cfg := lerINI(lerTextoINI(bytes.TrimSpace(b)))
	m := &modelo.MicroSIP{Perfil: perfil, Caminho: dir, Contas: contasDoINI(cfg)}
	if g := cfg["Global"]; g != nil {
		m.Versao = g["version"]
	}
	return m
}

// coletarMicroSIP procura o MicroSIP em todos os perfis (C:\Users\*\AppData\Roaming\MicroSIP)
// e nas pastas comuns da versão portátil.
func coletarMicroSIP(rel *modelo.Relatorio) {
	vistos := map[string]bool{}
	add := func(perfil, dir string) {
		k := strings.ToLower(filepath.Clean(dir))
		if vistos[k] {
			return
		}
		vistos[k] = true
		if m := lerMicroSIP(perfil, dir); m != nil {
			rel.MicroSIP = append(rel.MicroSIP, *m)
		}
	}
	// perfis informados pela coleta + todas as pastas em C:\Users
	var perfis []string
	for _, p := range rel.Perfis {
		if p.Caminho != "" {
			perfis = append(perfis, p.Caminho)
		}
	}
	raiz := filepath.Join(os.Getenv("SystemDrive")+`\`, "Users")
	if ents, err := os.ReadDir(raiz); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				perfis = append(perfis, filepath.Join(raiz, e.Name()))
			}
		}
	}
	for _, p := range perfis {
		nome := filepath.Base(p)
		switch strings.ToLower(nome) {
		case "public", "default", "default user", "all users", "defaultapppool":
			continue
		}
		add(nome, filepath.Join(p, "AppData", "Roaming", "MicroSIP"))
		add(nome, filepath.Join(p, "AppData", "Local", "MicroSIP")) // portátil copiado para o perfil
	}
	// versão portátil (microsip.ini ao lado do microsip.exe)
	for _, d := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("SystemDrive") + `\`} {
		if d != "" {
			add("", filepath.Join(d, "MicroSIP"))
		}
	}
}
