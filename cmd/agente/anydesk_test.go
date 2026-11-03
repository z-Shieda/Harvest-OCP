package main

import (
	"os"
	"path/filepath"
	"testing"

	"inventario/internal/modelo"
)

func TestAnyDesk(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "AnyDesk"), 0o755)
	os.MkdirAll(filepath.Join(d, "AnyDesk-abc123", "ad_9f"), 0o755)
	os.WriteFile(filepath.Join(d, "AnyDesk", "system.conf"), []byte("\ufeffad.anynet.alias=ti-pc@ad\r\nad.anynet.id=123456789\r\nad.anynet.pwd_hash=SEGREDO\r\n"), 0o644)
	os.WriteFile(filepath.Join(d, "AnyDesk-abc123", "ad_9f", "system.conf"), []byte("ad.anynet.id=987654321\n"), 0o644)
	os.WriteFile(filepath.Join(d, "AnyDesk-abc123", "lixo.conf"), []byte("x"), 0o644)
	cs := confsAnyDesk(d)
	if len(cs) != 2 {
		t.Fatalf("confs: %v", cs)
	}
	id, al := lerConfAnyDesk(cs[0])
	if id != "123456789" || al != "ti-pc@ad" {
		t.Fatalf("%q %q", id, al)
	}
	t.Setenv("ProgramData", d)
	var rel modelo.Relatorio
	coletarAnyDesk(&rel)
	if len(rel.AnyDesk) != 2 || !rel.AnyDeskVerificado || rel.AnyDesk[0].Tipo != "servico" {
		t.Fatalf("%+v", rel)
	}
}
