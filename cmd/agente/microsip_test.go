package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLerMicroSIP(t *testing.T) {
	d := os.Getenv("MSIP_DIR")
	if d == "" {
		t.Skip()
	}
	m := lerMicroSIP("daniel.costa", d)
	b, _ := json.MarshalIndent(m, "", " ")
	t.Log(string(b))
	if strings.Contains(string(b), "NAOPODEVAZAR") {
		t.Fatal("senha vazou")
	}
	if len(m.Contas) != 1 || m.Contas[0].Usuario != "2010" || len(m.Chamadas) != 0 || m.Erro != "" {
		t.Fatal("inesperado")
	}
}
