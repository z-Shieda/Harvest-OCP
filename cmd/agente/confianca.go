package main

// Assinatura da T.I (1.21): o agente só troca o próprio executável — e só muda de servidor —
// se a versão/ordem estiver assinada pela chave da T.I (cadeia raiz → certificado → assinatura).
// A chave raiz pública fica gravada neste executável (harvest-chave "Assinar kit"). Sem ela
// (marcador zerado) o agente funciona como antes, sem verificar.

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"inventario/internal/assinatura"
	"inventario/internal/modelo"
)

// raizTeste/confiancaTeste: só para os testes automatizados (nunca preenchidos no agente real).
var (
	raizTeste      ed25519.PublicKey
	confiancaTeste bool
)

func raizDoAgente() (ed25519.PublicKey, bool) {
	if raizTeste != nil {
		return raizTeste, true
	}
	return assinatura.RaizEmbutida()
}

func caminhoConfianca() string { return filepath.Join(dirDados(), "confianca.json") }

func lerConfianca() assinatura.Confianca {
	var c assinatura.Confianca
	if b, err := os.ReadFile(caminhoConfianca()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

// aceitarCertificado guarda o certificado se for válido e mais novo que o último aceito.
// Daí em diante, certificados com número menor (chave antiga) são recusados.
func aceitarCertificado(c *assinatura.Certificado) {
	raiz, ok := raizDoAgente()
	if !ok || c == nil || !(executandoInstalado() || confiancaTeste) {
		return
	}
	atual := lerConfianca()
	if c.Seq <= atual.Seq {
		return
	}
	if _, err := c.Verificar(raiz, atual.Seq); err != nil {
		registrar("certificado recebido do servidor recusado: " + err.Error())
		return
	}
	b, _ := json.Marshal(assinatura.Confianca{Seq: c.Seq, Chave: c.Chave})
	if os.WriteFile(caminhoConfianca(), b, 0o644) == nil {
		registrar(fmt.Sprintf("chave de assinatura da T.I atualizada (certificado nº %d)", c.Seq))
	}
}

// baixarManifesto busca a assinatura da versão que o servidor distribui.
func baixarManifesto(cfg Config) (*assinatura.Manifesto, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(cfg.Servidor, "/")+"/api/v1/agente.assinatura", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Agente-Token", cfg.Token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("o servidor não tem a assinatura desta versão (assine o kit com o harvest-chave)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("servidor respondeu %d ao pedir a assinatura", resp.StatusCode)
	}
	var m assinatura.Manifesto
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&m); err != nil {
		return nil, errors.New("assinatura ilegível")
	}
	return &m, nil
}

// conferirManifesto: cadeia válida, certificado não revogado e versão realmente mais nova.
func conferirManifesto(m *assinatura.Manifesto, versaoAnunciada string) error {
	raiz, _ := raizDoAgente()
	if err := m.Verificar(raiz, lerConfianca().Seq); err != nil {
		return err
	}
	if versaoAnunciada != "" && m.Versao != versaoAnunciada {
		return fmt.Errorf("o servidor anunciou a v%s mas a assinatura é da v%s", versaoAnunciada, m.Versao)
	}
	if !versaoMenor(versao, m.Versao) {
		return fmt.Errorf("a versão assinada (v%s) não é mais nova que a instalada (v%s)", m.Versao, versao)
	}
	return nil
}

// verificarAtualizacao confere o executável baixado ANTES de trocá-lo. nil = pode instalar.
func verificarAtualizacao(cfg Config, arquivo, versaoAnunciada string) error {
	if _, ok := raizDoAgente(); !ok {
		return nil // assinatura não configurada neste agente (modo antigo)
	}
	m, err := baixarManifesto(cfg)
	if err != nil {
		return err
	}
	if err := conferirManifesto(m, versaoAnunciada); err != nil {
		return err
	}
	exe, err := os.ReadFile(arquivo)
	if err != nil {
		return err
	}
	if err := m.Confere(exe); err != nil {
		return err
	}
	aceitarCertificado(&m.Certificado)
	return nil
}

// conferirMigracao: com a assinatura configurada, só muda de servidor com ordem assinada
// pela T.I para exatamente este destino/token, dentro da validade.
func conferirMigracao(m modelo.Migracao) error {
	raiz, ok := raizDoAgente()
	if !ok {
		return nil
	}
	if strings.TrimSpace(m.Ordem) == "" {
		return errors.New("migração sem ordem assinada pela T.I")
	}
	o, err := assinatura.LerOrdem(m.Ordem)
	if err != nil {
		return err
	}
	if err := o.Verificar(raiz, lerConfianca().Seq, time.Now()); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimRight(o.Servidor, "/"), strings.TrimRight(strings.TrimSpace(m.Servidor), "/")) ||
		o.Token != strings.TrimSpace(m.Token) {
		return errors.New("a ordem assinada é para outro servidor/token")
	}
	return nil
}
