package main

// AnyDesk (1.14): lê o ID do AnyDesk da máquina.
//   C:\ProgramData\AnyDesk\system.conf      → instalado (serviço)
//   C:\Users\<u>\AppData\Roaming\AnyDesk\system.conf → portátil / executado sem instalar
// Clientes personalizados do AnyDesk usam pastas "AnyDesk-xxxx" ou subpastas "ad_xxxx": também
// são procuradas. Só a linha do ID (ad.anynet.id) e o apelido (ad.anynet.alias) são lidos —
// nada de senha ou permissões.

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"inventario/internal/modelo"
)

// lerConfAnyDesk extrai ID e apelido de um system.conf.
func lerConfAnyDesk(caminho string) (id, alias string) {
	f, err := os.Open(caminho)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		l := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "ad.anynet.id":
			v = strings.TrimSpace(v)
			if soDigitosAD(v) {
				id = v
			}
		case "ad.anynet.alias":
			alias = strings.TrimSpace(v)
		}
	}
	return id, alias
}

func soDigitosAD(s string) bool {
	if len(s) < 6 || len(s) > 12 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// confsAnyDesk lista os system.conf numa pasta base (AnyDesk*, e um nível de subpastas).
func confsAnyDesk(base string) []string {
	var r []string
	ents, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(strings.ToLower(e.Name()), "anydesk") {
			continue
		}
		dir := filepath.Join(base, e.Name())
		if c := filepath.Join(dir, "system.conf"); existe(c) {
			r = append(r, c)
		}
		if subs, err := os.ReadDir(dir); err == nil {
			for _, s := range subs {
				if c := filepath.Join(dir, s.Name(), "system.conf"); s.IsDir() && existe(c) {
					r = append(r, c)
				}
			}
		}
	}
	return r
}

func existe(p string) bool { _, err := os.Stat(p); return err == nil }

func coletarAnyDesk(rel *modelo.Relatorio) {
	rel.AnyDeskVerificado = true
	rel.AnyDeskInstalado = anyDeskInstalado()
	vistos := map[string]bool{}
	add := func(conf, tipo, perfil string) {
		id, alias := lerConfAnyDesk(conf)
		if id == "" || vistos[tipo+id] {
			return
		}
		vistos[tipo+id] = true
		rel.AnyDesk = append(rel.AnyDesk, modelo.AnyDesk{ID: id, Alias: alias, Tipo: tipo, Perfil: perfil, Caminho: conf})
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	for _, c := range confsAnyDesk(pd) {
		add(c, "servico", "")
	}
	// portátil: AppData\Roaming de cada perfil (o agente roda como SYSTEM)
	raiz := filepath.Join(os.Getenv("SystemDrive")+`\`, "Users")
	if ents, err := os.ReadDir(raiz); err == nil {
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			switch strings.ToLower(e.Name()) {
			case "public", "default", "default user", "all users":
				continue
			}
			for _, c := range confsAnyDesk(filepath.Join(raiz, e.Name(), "AppData", "Roaming")) {
				add(c, "portatil", e.Name())
			}
		}
	}
}
