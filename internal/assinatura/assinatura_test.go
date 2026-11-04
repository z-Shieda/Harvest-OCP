package assinatura

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestPalavras(t *testing.T) {
	ps, err := GerarPalavras()
	if err != nil || len(ps) != 24 {
		t.Fatal(err, len(ps))
	}
	// lidas de volta, com numeração, maiúsculas e só 4 letras
	var b strings.Builder
	for i, p := range ps {
		b.WriteString(strings.ToUpper(p[:min(len(p), 4)]))
		if i%2 == 0 {
			b.WriteString(p[min(len(p), 4):])
		}
		b.WriteString("\n" + string(rune('0'+i%10)) + ". ")
	}
	lidas, err := LerPalavras(b.String())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lidas, " ") != strings.Join(ps, " ") {
		t.Fatal("não leu igual")
	}
	// trocar duas palavras de lugar quebra o verificador (quase sempre)
	tr := append([]string{}, ps...)
	tr[3], tr[17] = tr[17], tr[3]
	if tr[3] != tr[17] {
		if _, err := LerPalavras(strings.Join(tr, " ")); err == nil {
			t.Log("aviso: troca não detectada (1/256 de chance)")
		}
	}
	r1, _ := RaizDasPalavras(ps)
	r2, _ := RaizDasPalavras(lidas)
	if !bytes.Equal(r1, r2) {
		t.Fatal("raiz não é determinística")
	}
}

// vetor oficial do BIP39 (entropia 0x7f…7f, 24 palavras em inglês não serve aqui; confere a
// codificação de bits com entropia toda zero: 23× primeira palavra + verificador)
func TestEntropiaZero(t *testing.T) {
	ps := palavrasDeEntropia(make([]byte, 32))
	for i := 0; i < 23; i++ {
		if ps[i] != "abacate" {
			t.Fatal(ps)
		}
	}
	if _, err := LerPalavras(strings.Join(ps, " ")); err != nil {
		t.Fatal(err)
	}
}

func TestCadeia(t *testing.T) {
	ps, _ := GerarPalavras()
	raiz, _ := RaizDasPalavras(ps)
	rpub := raiz.Public().(ed25519.PublicKey)
	pub, priv, _ := ed25519.GenerateKey(nil)
	cert := EmitirCertificado(raiz, pub)
	exe := bytes.Repeat([]byte("MZ agente "), 1000)
	m := AssinarAgente(priv, cert, "1.21.0", exe)
	if err := m.Verificar(rpub, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Confere(exe); err != nil {
		t.Fatal(err)
	}
	exe[10] ^= 1
	if m.Confere(exe) == nil {
		t.Fatal("aceitou arquivo alterado")
	}
	m2 := m
	m2.Versao = "9.9.9"
	if m2.Verificar(rpub, 0) == nil {
		t.Fatal("aceitou versão trocada")
	}
	// raiz de outra pessoa
	outra, _, _ := ed25519.GenerateKey(nil)
	if m.Verificar(outra, 0) == nil {
		t.Fatal("aceitou outra raiz")
	}
	// certificado revogado
	if m.Verificar(rpub, cert.Seq+1) == nil {
		t.Fatal("aceitou certificado antigo")
	}
	// migração
	o := AssinarMigracao(priv, cert, "http://novo:8081/", "tok", time.Hour)
	lida, err := LerOrdem(" " + o.Texto()[:20] + "\n" + o.Texto()[20:])
	if err != nil {
		t.Fatal(err)
	}
	if err := lida.Verificar(rpub, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	if lida.Verificar(rpub, 0, time.Now().Add(2*time.Hour)) == nil {
		t.Fatal("aceitou ordem vencida")
	}
	lida.Token = "outro"
	if lida.Verificar(rpub, 0, time.Now()) == nil {
		t.Fatal("aceitou ordem alterada")
	}
	// recuperação
	r := ResponderDesafio(raiz, "abc123")
	if !ConferirResposta(rpub, "abc123", r) || ConferirResposta(rpub, "abc124", r) {
		t.Fatal("recuperação")
	}
	// arquivo
	a, err := NovoArquivo(rpub, cert, priv, "senha-forte-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Abrir("errada-errada"); err == nil {
		t.Fatal("abriu com senha errada")
	}
	p2, r2, err := a.Abrir("senha-forte-123")
	if err != nil || !bytes.Equal(p2, priv) || !bytes.Equal(r2, rpub) {
		t.Fatal(err)
	}
}

func TestMarcar(t *testing.T) {
	if _, ok := RaizEmbutida(); ok {
		t.Fatal("marcador deveria vir zerado")
	}
	exe := []byte("xx" + marcador + "yy HARVEST-RAIZ-PUB: texto" + marcador + "zz")
	pub, _, _ := ed25519.GenerateKey(nil)
	if n := Marcar(exe, pub); n != 2 {
		t.Fatal(n)
	}
	rs := RaizesNoExecutavel(exe)
	if len(rs) != 2 || rs[0] != hex.EncodeToString(pub) {
		t.Fatal(rs)
	}
}
