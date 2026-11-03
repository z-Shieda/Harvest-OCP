package main

import (
	"os"
	"path/filepath"
	"testing"

	"inventario/internal/modelo"
)

func TestMonitor(t *testing.T) {
	d := t.TempDir()
	old, _ := os.Getwd()
	os.Chdir(d)
	defer os.Chdir(old)
	os.MkdirAll(dirDados(), 0o755)
	exe := filepath.Join(d, "spa", "work.exe")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("x"), 0o644)
	a := &modelo.AlvoMonitor{Nome: "Work Monitor", CaminhosFixos: []string{exe}, Processos: []string{"work.exe"}}
	if !salvarAlvoMonitor(a) || salvarAlvoMonitor(a) {
		t.Fatal("salvar deveria mudar só na 1ª vez")
	}
	var rel modelo.Relatorio
	coletarMonitor(&rel)
	if rel.Monitor == nil || len(rel.Monitor.Instalacoes) != 1 || rel.Monitor.Nome != "Work Monitor" {
		t.Fatalf("%+v", rel.Monitor)
	}
	if !salvarAlvoMonitor(&modelo.AlvoMonitor{}) || carregarAlvoMonitor() != nil {
		t.Fatal("desligar deveria apagar")
	}
}
