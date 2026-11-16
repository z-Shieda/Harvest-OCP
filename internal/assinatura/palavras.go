package assinatura

// 24 palavras (padrão BIP39, lista oficial em português — a mesma das carteiras de bitcoin):
// 256 bits aleatórios + 8 bits de verificação. Das palavras sai, sempre igual, a CHAVE RAIZ
// da T.I (Ed25519). Ela nunca é gravada em disco: só existe enquanto alguém digita as palavras.

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

//go:embed palavras-pt.txt
var listaTexto string

var (
	lista   []string
	posicao = map[string]int{}
	prefixo = map[string]int{} // as 4 primeiras letras já identificam a palavra (regra do BIP39)
)

func init() {
	lista = strings.Fields(listaTexto)
	if len(lista) != 2048 {
		panic("assinatura: lista de palavras incompleta")
	}
	for i, p := range lista {
		posicao[p] = i
		if len(p) >= 4 {
			prefixo[p[:4]] = i
		}
	}
}

// Palavra devolve a i-ésima palavra da lista (0..2047).
func Palavra(i int) string { return lista[i] }

// GerarPalavras sorteia 24 palavras novas (com o dígito verificador do BIP39).
func GerarPalavras() ([]string, error) {
	ent := make([]byte, 32)
	if _, err := rand.Read(ent); err != nil {
		return nil, err
	}
	return palavrasDeEntropia(ent), nil
}

func palavrasDeEntropia(ent []byte) []string {
	h := sha256.Sum256(ent)
	bits := append(append([]byte{}, ent...), h[0]) // 256 + 8 = 264 bits = 24 × 11
	out := make([]string, 24)
	for i := 0; i < 24; i++ {
		v := 0
		for b := 0; b < 11; b++ {
			pos := i*11 + b
			bit := (bits[pos/8] >> (7 - uint(pos%8))) & 1
			v = v<<1 | int(bit)
		}
		out[i] = lista[v]
	}
	return out
}

var tiraAcento = strings.NewReplacer("á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "é", "e", "ê", "e", "è", "e",
	"í", "i", "î", "i", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ú", "u", "ü", "u", "ç", "c")

// semAcento: a lista em português do BIP39 não tem acentos; quem digitar "lâmpada" acerta igual.
func semAcento(s string) string { return tiraAcento.Replace(strings.ToLower(s)) }

// LerPalavras interpreta o texto digitado (separado por espaço, vírgula ou linha; aceita
// números "1." na frente, maiúsculas, acentos e só as 4 primeiras letras) e confere o
// dígito verificador — uma palavra trocada ou fora de ordem é detectada.
func LerPalavras(texto string) ([]string, error) {
	campos := strings.FieldsFunc(texto, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';' || r == '-'
	})
	var ps []string
	for _, c := range campos {
		c = strings.Trim(semAcento(c), ".)(:")
		if c == "" || strings.Trim(c, "0123456789") == "" {
			continue // numeração "1." "2)" …
		}
		c = strings.TrimLeft(c, "0123456789.)")
		if i, ok := posicao[c]; ok {
			ps = append(ps, lista[i])
			continue
		}
		if len(c) >= 4 {
			if i, ok := prefixo[c[:4]]; ok {
				ps = append(ps, lista[i])
				continue
			}
		}
		return nil, fmt.Errorf("a palavra %q não está na lista (confira a grafia)", c)
	}
	if len(ps) != 24 {
		return nil, fmt.Errorf("são 24 palavras; foram reconhecidas %d", len(ps))
	}
	// confere o verificador
	bits := make([]byte, 33)
	for i, p := range ps {
		v := posicao[p]
		for b := 0; b < 11; b++ {
			if v>>(10-uint(b))&1 == 1 {
				pos := i*11 + b
				bits[pos/8] |= 1 << (7 - uint(pos%8))
			}
		}
	}
	h := sha256.Sum256(bits[:32])
	if h[0] != bits[32] {
		return nil, errors.New("as palavras não conferem (alguma está errada ou fora de ordem)")
	}
	return ps, nil
}

// RaizDasPalavras deriva a chave raiz: semente BIP39 (PBKDF2-HMAC-SHA512, 2048 voltas, sal
// "mnemonic") e, dela, uma chave Ed25519 exclusiva do Harvest.
func RaizDasPalavras(ps []string) (ed25519.PrivateKey, error) {
	frase := strings.Join(ps, " ") // a lista é só ASCII: a normalização NFKD do BIP39 não muda nada
	semente, err := pbkdf2.Key(sha512.New, frase, []byte("mnemonic"), 2048, 64)
	if err != nil {
		return nil, err
	}
	m := hmac.New(sha512.New, []byte("Harvest chave raiz v1"))
	m.Write(semente)
	return ed25519.NewKeyFromSeed(m.Sum(nil)[:32]), nil
}
