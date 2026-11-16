// Package assinatura: a "assinatura da T.I" do Harvest (1.21).
//
// Duas chaves Ed25519:
//
//   - CHAVE RAIZ — sai das 24 palavras (palavras.go). Nunca fica gravada. Só serve para
//     emitir/trocar a chave de assinatura (certificado) e para recuperar o acesso ao painel.
//     A parte pública vai gravada dentro do agente e do servidor (marcador, ver Marcar).
//   - CHAVE DE ASSINATURA — o "carimbo" do dia a dia. Fica num arquivo .chave protegido por
//     senha (guardado no 1Password). Assina cada versão do agente e as ordens de migração.
//     Vale enquanto houver um CERTIFICADO dela assinado pela raiz; um certificado com número
//     (Seq) maior substitui o anterior — é assim que uma chave perdida/vazada é revogada.
//
// O agente só troca o próprio executável (e só muda de servidor) se a cadeia
// raiz → certificado → assinatura conferir. Quem invadir o servidor não tem nenhuma das chaves.
package assinatura

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---------------- marcador da chave raiz dentro dos executáveis ----------------

// PrefixoMarcador antecede os 64 dígitos hex da chave raiz pública gravada no .exe.
// A ferramenta harvest-chave troca os zeros pela chave da T.I (Marcar) — sem recompilar.
const PrefixoMarcador = "HARVEST-RAIZ-PUB:"

// marcador: NÃO mude o formato (prefixo + 64 hex). Zeros = assinatura ainda não configurada.
var marcador = "HARVEST-RAIZ-PUB:0000000000000000000000000000000000000000000000000000000000000000"

// RaizEmbutida devolve a chave raiz pública gravada neste executável (ok=false se não configurada).
func RaizEmbutida() (ed25519.PublicKey, bool) {
	return raizDeHex(marcador[len(PrefixoMarcador):])
}

func raizDeHex(h string) (ed25519.PublicKey, bool) {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, false
	}
	if bytes.Equal(b, make([]byte, ed25519.PublicKeySize)) {
		return nil, false
	}
	return ed25519.PublicKey(b), true
}

// Marcar grava a chave raiz em todos os marcadores do executável (o servidor tem dois: o dele
// e o do agente embutido). Devolve quantos trocou.
func Marcar(exe []byte, raiz ed25519.PublicKey) int {
	novo := []byte(hex.EncodeToString(raiz))
	n, i := 0, 0
	for {
		j := bytes.Index(exe[i:], []byte(PrefixoMarcador))
		if j < 0 {
			return n
		}
		ini := i + j + len(PrefixoMarcador)
		if ini+64 <= len(exe) && ehHex(exe[ini:ini+64]) {
			copy(exe[ini:ini+64], novo)
			n++
		}
		i = ini
	}
}

// RaizesNoExecutavel lista as chaves raiz gravadas num executável ("" = não configurada).
func RaizesNoExecutavel(exe []byte) []string {
	var out []string
	i := 0
	for {
		j := bytes.Index(exe[i:], []byte(PrefixoMarcador))
		if j < 0 {
			return out
		}
		ini := i + j + len(PrefixoMarcador)
		if ini+64 <= len(exe) && ehHex(exe[ini:ini+64]) {
			h := string(exe[ini : ini+64])
			if _, ok := raizDeHex(h); !ok {
				h = ""
			}
			out = append(out, h)
		}
		i = ini
	}
}

func ehHex(b []byte) bool {
	for _, c := range b {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Impressao: forma curta de mostrar uma chave pública (16 hex, em blocos).
func Impressao(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	s := strings.ToUpper(hex.EncodeToString(h[:8]))
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}

// ---------------- certificado (raiz → chave de assinatura) ----------------

type Certificado struct {
	Chave      string `json:"chave"`      // chave de assinatura pública (hex)
	Seq        int64  `json:"seq"`        // maior = mais novo; agentes não aceitam Seq menor que o último visto
	Emitido    string `json:"emitido"`    // RFC3339
	Assinatura string `json:"assinatura"` // da raiz (hex)
}

func (c Certificado) mensagem() []byte {
	return []byte("harvest-certificado-v1\n" + c.Chave + "\n" + strconv.FormatInt(c.Seq, 10) + "\n" + c.Emitido)
}

// EmitirCertificado: a raiz autoriza uma chave de assinatura. Seq = hora atual (sempre cresce).
func EmitirCertificado(raiz ed25519.PrivateKey, chave ed25519.PublicKey) Certificado {
	agora := time.Now().UTC()
	c := Certificado{Chave: hex.EncodeToString(chave), Seq: agora.Unix(), Emitido: agora.Format(time.RFC3339)}
	c.Assinatura = hex.EncodeToString(ed25519.Sign(raiz, c.mensagem()))
	return c
}

// Verificar confere o certificado contra a raiz e o menor Seq aceito; devolve a chave autorizada.
func (c Certificado) Verificar(raiz ed25519.PublicKey, seqMinimo int64) (ed25519.PublicKey, error) {
	ch, err := hex.DecodeString(c.Chave)
	if err != nil || len(ch) != ed25519.PublicKeySize {
		return nil, errors.New("certificado com chave inválida")
	}
	sig, err := hex.DecodeString(c.Assinatura)
	if err != nil || !ed25519.Verify(raiz, c.mensagem(), sig) {
		return nil, errors.New("certificado não foi emitido pela chave raiz da T.I")
	}
	if c.Seq < seqMinimo {
		return nil, fmt.Errorf("certificado antigo (revogado): nº %d, já foi visto o nº %d", c.Seq, seqMinimo)
	}
	return ed25519.PublicKey(ch), nil
}

// ---------------- manifesto de uma versão do agente ----------------

const FormatoManifesto = "harvest-agente-v1"

type Manifesto struct {
	Formato     string      `json:"formato"`
	Versao      string      `json:"versao"`
	SHA256      string      `json:"sha256"`
	Tamanho     int64       `json:"tamanho"`
	AssinadoEm  string      `json:"assinado_em"`
	Certificado Certificado `json:"certificado"`
	Assinatura  string      `json:"assinatura"` // da chave de assinatura (hex)
}

func (m Manifesto) mensagem() []byte {
	return []byte(FormatoManifesto + "\n" + m.Versao + "\n" + m.SHA256 + "\n" + strconv.FormatInt(m.Tamanho, 10) + "\n" + m.AssinadoEm)
}

func AssinarAgente(chave ed25519.PrivateKey, cert Certificado, versao string, exe []byte) Manifesto {
	h := sha256.Sum256(exe)
	m := Manifesto{Formato: FormatoManifesto, Versao: versao, SHA256: hex.EncodeToString(h[:]), Tamanho: int64(len(exe)),
		AssinadoEm: time.Now().UTC().Format(time.RFC3339), Certificado: cert}
	m.Assinatura = hex.EncodeToString(ed25519.Sign(chave, m.mensagem()))
	return m
}

// Verificar confere a cadeia raiz → certificado → manifesto (sem olhar o arquivo).
func (m Manifesto) Verificar(raiz ed25519.PublicKey, seqMinimo int64) error {
	if m.Formato != FormatoManifesto {
		return errors.New("assinatura em formato desconhecido")
	}
	chave, err := m.Certificado.Verificar(raiz, seqMinimo)
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(m.Assinatura)
	if err != nil || !ed25519.Verify(chave, m.mensagem(), sig) {
		return errors.New("assinatura da versão inválida")
	}
	return nil
}

// Confere diz se o arquivo é exatamente o que foi assinado.
func (m Manifesto) Confere(exe []byte) error {
	if int64(len(exe)) != m.Tamanho {
		return fmt.Errorf("tamanho diferente do assinado (%d ≠ %d bytes)", len(exe), m.Tamanho)
	}
	h := sha256.Sum256(exe)
	if hex.EncodeToString(h[:]) != m.SHA256 {
		return errors.New("o arquivo não é o que foi assinado (SHA-256 diferente)")
	}
	return nil
}

// ---------------- ordem de migração ----------------

const prefixoOrdem = "HARVEST-MIGRAR:"

type OrdemMigracao struct {
	Servidor    string      `json:"servidor"`
	Token       string      `json:"token"`
	Validade    string      `json:"validade"` // RFC3339; depois disso os agentes recusam
	Certificado Certificado `json:"certificado"`
	Assinatura  string      `json:"assinatura"`
}

func (o OrdemMigracao) mensagem() []byte {
	return []byte("harvest-migrar-v1\n" + o.Servidor + "\n" + o.Token + "\n" + o.Validade)
}

func AssinarMigracao(chave ed25519.PrivateKey, cert Certificado, servidor, token string, validade time.Duration) OrdemMigracao {
	o := OrdemMigracao{Servidor: strings.TrimRight(strings.TrimSpace(servidor), "/"), Token: strings.TrimSpace(token),
		Validade: time.Now().UTC().Add(validade).Format(time.RFC3339), Certificado: cert}
	o.Assinatura = hex.EncodeToString(ed25519.Sign(chave, o.mensagem()))
	return o
}

func (o OrdemMigracao) Verificar(raiz ed25519.PublicKey, seqMinimo int64, agora time.Time) error {
	chave, err := o.Certificado.Verificar(raiz, seqMinimo)
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(o.Assinatura)
	if err != nil || !ed25519.Verify(chave, o.mensagem(), sig) {
		return errors.New("ordem de migração com assinatura inválida")
	}
	v, err := time.Parse(time.RFC3339, o.Validade)
	if err != nil || agora.After(v) {
		return errors.New("ordem de migração vencida — gere outra no harvest-chave")
	}
	return nil
}

// Texto: a ordem como uma linha para copiar e colar no painel.
func (o OrdemMigracao) Texto() string {
	b, _ := json.Marshal(o)
	return prefixoOrdem + base64.RawURLEncoding.EncodeToString(b)
}

func LerOrdem(texto string) (OrdemMigracao, error) {
	var o OrdemMigracao
	t := strings.Join(strings.Fields(texto), "")
	if !strings.HasPrefix(t, prefixoOrdem) {
		return o, errors.New("isso não é uma ordem de migração do harvest-chave (deve começar com HARVEST-MIGRAR:)")
	}
	b, err := base64.RawURLEncoding.DecodeString(t[len(prefixoOrdem):])
	if err != nil || json.Unmarshal(b, &o) != nil || o.Servidor == "" {
		return o, errors.New("ordem de migração incompleta (copie a linha inteira)")
	}
	return o, nil
}

// ---------------- recuperação do acesso ao painel (só com as 24 palavras) ----------------

const prefixoResposta = "HARVEST-RECUPERAR:"

func mensagemRecuperar(desafio string) []byte {
	return []byte("harvest-recuperar-acesso-v1\n" + strings.TrimSpace(desafio))
}

// ResponderDesafio: feito no PC da T.I com a raiz (palavras); o servidor confere com a raiz pública.
func ResponderDesafio(raiz ed25519.PrivateKey, desafio string) string {
	return prefixoResposta + hex.EncodeToString(ed25519.Sign(raiz, mensagemRecuperar(desafio)))
}

func ConferirResposta(raiz ed25519.PublicKey, desafio, resposta string) bool {
	r := strings.Join(strings.Fields(resposta), "")
	r = strings.TrimPrefix(r, prefixoResposta)
	sig, err := hex.DecodeString(r)
	return err == nil && ed25519.Verify(raiz, mensagemRecuperar(desafio), sig)
}

// ---------------- arquivo da chave de assinatura (.chave) ----------------

const FormatoArquivo = "harvest-chave-v1"

type ArquivoChave struct {
	Formato     string      `json:"formato"`
	Aviso       string      `json:"aviso"`
	Criada      string      `json:"criada"`
	Raiz        string      `json:"raiz"` // chave raiz PÚBLICA (hex) — as palavras não ficam aqui
	Certificado Certificado `json:"certificado"`
	KDF         string      `json:"kdf"`
	Voltas      int         `json:"voltas"`
	Sal         string      `json:"sal"`
	Nonce       string      `json:"nonce"`
	Cifrada     string      `json:"cifrada"` // chave de assinatura privada, AES-256-GCM
}

const voltasPBKDF2 = 600_000

func chaveDaSenha(senha string, sal []byte, voltas int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, senha, sal, voltas, 32)
}

// NovoArquivo cifra a chave de assinatura com a senha.
func NovoArquivo(raiz ed25519.PublicKey, cert Certificado, priv ed25519.PrivateKey, senha string) (*ArquivoChave, error) {
	if len([]rune(senha)) < 10 {
		return nil, errors.New("use uma senha com pelo menos 10 caracteres")
	}
	sal := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := rand.Read(sal); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	k, err := chaveDaSenha(senha, sal, voltasPBKDF2)
	if err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(k)
	gcm, _ := cipher.NewGCM(blk)
	a := &ArquivoChave{
		Formato: FormatoArquivo, Criada: time.Now().Format(time.RFC3339),
		Aviso: "Chave de assinatura da T.I (Harvest). Guarde no 1Password. Sem a senha ela não serve para nada.",
		Raiz:  hex.EncodeToString(raiz), Certificado: cert, KDF: "pbkdf2-sha256", Voltas: voltasPBKDF2,
		Sal: base64.StdEncoding.EncodeToString(sal), Nonce: base64.StdEncoding.EncodeToString(nonce),
	}
	a.Cifrada = base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, priv.Seed(), []byte(a.Raiz+a.Certificado.Chave)))
	return a, nil
}

func LerArquivo(b []byte) (*ArquivoChave, error) {
	var a ArquivoChave
	if json.Unmarshal(b, &a) != nil || a.Formato != FormatoArquivo {
		return nil, errors.New("isso não é um arquivo de chave do Harvest (.chave)")
	}
	return &a, nil
}

// Abrir decifra a chave de assinatura e confere que ela bate com o certificado e a raiz.
func (a *ArquivoChave) Abrir(senha string) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	sal, _ := base64.StdEncoding.DecodeString(a.Sal)
	nonce, _ := base64.StdEncoding.DecodeString(a.Nonce)
	dados, _ := base64.StdEncoding.DecodeString(a.Cifrada)
	k, err := chaveDaSenha(senha, sal, a.Voltas)
	if err != nil {
		return nil, nil, err
	}
	blk, _ := aes.NewCipher(k)
	gcm, _ := cipher.NewGCM(blk)
	seed, err := gcm.Open(nil, nonce, dados, []byte(a.Raiz+a.Certificado.Chave))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, nil, errors.New("senha incorreta")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	raiz, ok := raizDeHex(a.Raiz)
	if !ok {
		return nil, nil, errors.New("arquivo de chave sem a chave raiz")
	}
	pub, err := a.Certificado.Verificar(raiz, 0)
	if err != nil {
		return nil, nil, err
	}
	if !bytes.Equal(pub, priv.Public().(ed25519.PublicKey)) {
		return nil, nil, errors.New("o certificado do arquivo não corresponde à chave")
	}
	return priv, raiz, nil
}

// ---------------- confiança gravada no agente ----------------

// Confianca: o certificado mais novo que o agente já aceitou (fica em ProgramData).
type Confianca struct {
	Seq   int64  `json:"seq"`
	Chave string `json:"chave"`
}
