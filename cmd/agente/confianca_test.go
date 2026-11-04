package main

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"inventario/internal/assinatura"
	"inventario/internal/modelo"
)

func TestAtualizacaoAssinada(t *testing.T) {
	dir := t.TempDir()
	velho, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer os.Chdir(velho)
	_ = os.MkdirAll(dirDados(), 0o755)

	ps, _ := assinatura.GerarPalavras()
	raiz, _ := assinatura.RaizDasPalavras(ps)
	raizTeste = raiz.Public().(ed25519.PublicKey)
	confiancaTeste = true
	defer func() { raizTeste = nil; confiancaTeste = false }()

	pub, priv, _ := ed25519.GenerateKey(nil)
	cert := assinatura.EmitirCertificado(raiz, pub)
	exe := make([]byte, 2_000_000)
	exe[0], exe[1] = 'M', 'Z'
	manif := assinatura.AssinarAgente(priv, cert, "9.0.0", exe)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Agente-Token") != "TOK" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(manif)
	}))
	defer srv.Close()
	cfg := Config{Servidor: srv.URL, Token: "TOK"}
	arq := filepath.Join(dir, "novo.exe")

	// 1) arquivo certo
	_ = os.WriteFile(arq, exe, 0o644)
	if err := verificarAtualizacao(cfg, arq, "9.0.0"); err != nil {
		t.Fatal("deveria aceitar:", err)
	}
	if lerConfianca().Seq != cert.Seq {
		t.Fatal("não guardou o certificado")
	}
	// 2) arquivo adulterado (servidor invadido trocou o .exe)
	ruim := append([]byte(nil), exe...)
	ruim[500] = 1
	_ = os.WriteFile(arq, ruim, 0o644)
	if err := verificarAtualizacao(cfg, arq, "9.0.0"); err == nil {
		t.Fatal("aceitou .exe adulterado")
	} else {
		t.Log("adulterado:", err)
	}
	// 3) versão anunciada diferente da assinada
	_ = os.WriteFile(arq, exe, 0o644)
	if err := verificarAtualizacao(cfg, arq, "9.9.9"); err == nil {
		t.Fatal("aceitou versão anunciada diferente")
	}
	// 4) versão antiga assinada (downgrade)
	manif = assinatura.AssinarAgente(priv, cert, "1.0.0", exe)
	if err := verificarAtualizacao(cfg, arq, ""); err == nil {
		t.Fatal("aceitou downgrade")
	} else {
		t.Log("downgrade:", err)
	}
	// 5) assinado por um atacante com outra raiz
	_, falsa, _ := ed25519.GenerateKey(nil)
	pub2, priv2, _ := ed25519.GenerateKey(nil)
	manif = assinatura.AssinarAgente(priv2, assinatura.EmitirCertificado(falsa, pub2), "9.0.0", exe)
	if err := verificarAtualizacao(cfg, arq, "9.0.0"); err == nil {
		t.Fatal("aceitou outra raiz")
	} else {
		t.Log("outra raiz:", err)
	}
	// 6) chave trocada com as palavras: certificado novo revoga o antigo
	time.Sleep(1100 * time.Millisecond)
	pub3, priv3, _ := ed25519.GenerateKey(nil)
	cert3 := assinatura.EmitirCertificado(raiz, pub3)
	aceitarCertificado(&cert3)
	if lerConfianca().Seq != cert3.Seq {
		t.Fatal("não aceitou certificado novo")
	}
	manif = assinatura.AssinarAgente(priv, cert, "9.0.0", exe) // chave ANTIGA
	if err := verificarAtualizacao(cfg, arq, "9.0.0"); err == nil {
		t.Fatal("aceitou chave revogada")
	} else {
		t.Log("revogada:", err)
	}
	manif = assinatura.AssinarAgente(priv3, cert3, "9.0.0", exe)
	if err := verificarAtualizacao(cfg, arq, "9.0.0"); err != nil {
		t.Fatal("chave nova deveria valer:", err)
	}
	// 7) migração
	o := assinatura.AssinarMigracao(priv3, cert3, "http://novo:8081", "TOK2", time.Hour)
	if err := conferirMigracao(modelo.Migracao{Servidor: "http://novo:8081", Token: "TOK2", Ordem: o.Texto()}); err != nil {
		t.Fatal(err)
	}
	if conferirMigracao(modelo.Migracao{Servidor: "http://mal:8081", Token: "TOK2", Ordem: o.Texto()}) == nil {
		t.Fatal("aceitou ordem para outro servidor")
	}
	if conferirMigracao(modelo.Migracao{Servidor: "http://novo:8081", Token: "TOK2"}) == nil {
		t.Fatal("aceitou migração sem ordem")
	}
}
