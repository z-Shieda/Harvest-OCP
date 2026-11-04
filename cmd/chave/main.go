// harvest-chave: a "assinatura da T.I" do Harvest. Roda SÓ no PC da T.I (nunca no servidor).
//
//	harvest-chave                 menu
//	harvest-chave criar           cria as 24 palavras e a chave de assinatura (primeira vez)
//	harvest-chave assinar <kit>   grava a chave raiz no kit e assina o agente (antes de instalar)
//	harvest-chave migrar          gera uma ordem de migração de servidor assinada
//	harvest-chave recuperar       responde ao desafio da página "Esqueci minha senha"
//	harvest-chave nova-chave      nova chave de assinatura com as 24 palavras (perdeu/vazou)
//	harvest-chave conferir        confere se as 24 palavras são as da sua chave
//
// Para testes/automação: HARVEST_SENHA, HARVEST_PALAVRAS, HARVEST_CHAVE (arquivo .chave).
package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"inventario/internal/assinatura"
)

var entrada = bufio.NewReader(os.Stdin)

func main() {
	cmd := ""
	args := os.Args[1:]
	if len(args) > 0 {
		cmd, args = strings.ToLower(args[0]), args[1:]
	}
	var err error
	switch cmd {
	case "criar":
		err = criar()
	case "assinar", "assinar-kit":
		kit := ""
		if len(args) > 0 {
			kit = args[0]
		}
		err = assinarKit(kit, "")
	case "migrar":
		err = ordemMigracao()
	case "recuperar":
		err = recuperar()
	case "nova-chave":
		err = novaChave()
	case "conferir":
		err = conferir()
	case "":
		menu()
		return
	default:
		// arrastar a pasta do kit para cima do .exe
		if fi, e := os.Stat(cmd); e == nil && fi.IsDir() {
			err = assinarKit(os.Args[1], "")
			break
		}
		fmt.Println("Comando desconhecido. Use: criar | assinar <pasta do kit> | migrar | recuperar | nova-chave | conferir")
		os.Exit(2)
	}
	if err != nil {
		fmt.Println("\nERRO: " + err.Error())
		pausar()
		os.Exit(1)
	}
	pausar()
}

func menu() {
	for {
		titulo("HARVEST — assinatura da T.I")
		fmt.Println("  1) Criar as chaves da T.I (primeira vez)")
		fmt.Println("  2) Assinar o kit de uma versão (antes de instalar o servidor)")
		fmt.Println("  3) Ordem de migração de servidor")
		fmt.Println("  4) Recuperar acesso ao painel (esqueci a senha)")
		fmt.Println("  5) Nova chave de assinatura (perdi o arquivo .chave ou ele vazou)")
		fmt.Println("  6) Conferir as 24 palavras")
		fmt.Println("  0) Sair")
		var err error
		switch perguntar("\nOpção", "") {
		case "1":
			err = criar()
		case "2":
			err = assinarKit("", "")
		case "3":
			err = ordemMigracao()
		case "4":
			err = recuperar()
		case "5":
			err = novaChave()
		case "6":
			err = conferir()
		case "0", "":
			return
		default:
			continue
		}
		if err != nil {
			fmt.Println("\nERRO: " + err.Error())
		}
		pausar()
	}
}

// ---------------- 1) criar ----------------

func criar() error {
	titulo("Criar as chaves da T.I")
	fmt.Println(`Serão criadas:
  • 24 PALAVRAS — a chave mestra. Com elas você recria tudo: troca a chave de
    assinatura se ela vazar/sumir e recupera o acesso ao painel. Anote em PAPEL.
  • o arquivo harvest-ti.chave — o carimbo do dia a dia (assina as versões),
    protegido por uma senha. Guarde no 1Password.

Faça isso uma vez só. Se já criou, use a opção 5 ou 6.`)
	if strings.ToUpper(perguntar("\nContinuar? (S/N)", "S")) != "S" {
		return nil
	}
	ps, err := assinatura.GerarPalavras()
	if err != nil {
		return err
	}
	if os.Getenv("HARVEST_PALAVRAS") == "" {
		mostrarPalavras(ps)
		fmt.Println(`ANOTE AS 24 PALAVRAS, NA ORDEM, EM PAPEL.
  • Não tire foto, não salve em arquivo, não mande por e-mail/Teams.
  • Não guarde junto com o arquivo .chave (se os dois vazarem juntos, não há como revogar).
  • Guarde o papel num lugar seguro (cofre/gaveta trancada). Uma segunda cópia em outro lugar é recomendada.`)
		perguntar("\nQuando terminar de anotar, pressione Enter", "")
		limparTela()
		// confere 3 palavras sorteadas
		pos := rand.Perm(24)[:3]
		for _, p := range pos {
			for {
				r := strings.ToLower(strings.TrimSpace(perguntar(fmt.Sprintf("Confira: qual é a palavra nº %d?", p+1), "")))
				if r == ps[p] || (len(r) >= 4 && len(ps[p]) >= 4 && r[:4] == ps[p][:4]) {
					break
				}
				fmt.Println("  Não confere. Olhe sua anotação (se perdeu, feche e comece de novo).")
			}
		}
	} else {
		ps, err = assinatura.LerPalavras(os.Getenv("HARVEST_PALAVRAS"))
		if err != nil {
			return err
		}
	}
	raiz, err := assinatura.RaizDasPalavras(ps)
	if err != nil {
		return err
	}
	destino, err := gerarChaveAssinatura(raiz, "harvest-ti.chave")
	if err != nil {
		return err
	}
	rpub := raiz.Public().(ed25519.PublicKey)
	fmt.Printf("\n✔ Pronto.\n  Chave raiz da T.I: %s  (aparece no painel em Administração → Assinatura)\n", assinatura.Impressao(rpub))
	fmt.Printf("  Arquivo de assinatura: %s\n", destino)
	fmt.Println(`
PRÓXIMOS PASSOS
  1. Guarde o harvest-ti.chave no 1Password (anexo) e a senha dele no mesmo item.
     Depois pode apagar a cópia deste PC.
  2. Assine cada kit novo antes de instalar:  opção 2 (ou arraste a pasta do kit sobre o harvest-chave.exe).`)
	return nil
}

func gerarChaveAssinatura(raiz ed25519.PrivateKey, nomePadrao string) (string, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", err
	}
	cert := assinatura.EmitirCertificado(raiz, pub)
	senha, err := senhaNova()
	if err != nil {
		return "", err
	}
	a, err := assinatura.NovoArquivo(raiz.Public().(ed25519.PublicKey), cert, priv, senha)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(a, "", "  ")
	destino := perguntar("Onde salvar o arquivo da chave", filepath.Join(pastaPadrao(), nomePadrao))
	destino = limparCaminho(destino)
	if _, err := os.Stat(destino); err == nil {
		destino = strings.TrimSuffix(destino, ".chave") + "-" + time.Now().Format("20060102-150405") + ".chave"
	}
	if err := os.WriteFile(destino, b, 0o600); err != nil {
		return "", err
	}
	return destino, nil
}

// ---------------- 2) assinar kit ----------------

func assinarKit(kit, chaveArq string) error {
	titulo("Assinar o kit de uma versão")
	if kit == "" {
		kit = perguntar("Pasta do kit (arraste a pasta aqui)", "")
	}
	kit = limparCaminho(kit)
	arqs := filepath.Join(kit, "arquivos")
	if fi, err := os.Stat(arqs); err != nil || !fi.IsDir() {
		arqs = kit
	}
	agenteArq := filepath.Join(arqs, "inventario-agente.exe")
	agente, err := os.ReadFile(agenteArq)
	if err != nil {
		return fmt.Errorf("não achei %s (use o kit INSTALAR-SERVIDOR 1.21 ou mais novo)", agenteArq)
	}
	servidores, _ := filepath.Glob(filepath.Join(arqs, "*_servidor_v*.exe"))
	if len(servidores) == 0 {
		return errors.New("não achei o Inventario_servidor_v*.exe / Harvest_servidor_v*.exe na pasta arquivos")
	}
	versao := lerVersao(arqs)
	if versao == "" {
		versao = perguntar("Versão do agente deste kit (ex.: 1.21.0)", "")
	}

	priv, raiz, cert, err := abrirChave(chaveArq)
	if err != nil {
		return err
	}
	raizHex := hex.EncodeToString(raiz)
	for _, r := range assinatura.RaizesNoExecutavel(agente) {
		if r != "" && r != raizHex {
			return errors.New("este kit já foi assinado com OUTRA chave raiz — baixe o kit original de novo")
		}
	}
	original := append([]byte(nil), agente...)
	if n := assinatura.Marcar(agente, raiz); n == 0 {
		return errors.New("o agente deste kit não tem o marcador da chave raiz (versão anterior à 1.21?)")
	}
	for _, sv := range servidores {
		srv, err := os.ReadFile(sv)
		if err != nil {
			return err
		}
		if !bytes.Contains(srv, original) && !bytes.Contains(srv, agente) {
			return fmt.Errorf("%s não contém este inventario-agente.exe (arquivos de kits diferentes?)", filepath.Base(sv))
		}
		assinatura.Marcar(srv, raiz)
		if !bytes.Contains(srv, agente) {
			return fmt.Errorf("falha ao gravar a chave em %s", filepath.Base(sv))
		}
		if err := gravarSeguro(sv, srv); err != nil {
			return err
		}
		fmt.Println("  ✔ chave raiz gravada em " + filepath.Base(sv))
	}
	if err := gravarSeguro(agenteArq, agente); err != nil {
		return err
	}
	m := assinatura.AssinarAgente(priv, cert, versao, agente)
	if err := m.Verificar(raiz, 0); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(arqs, "agente.assinatura"), b, 0o644); err != nil {
		return err
	}
	fmt.Printf("  ✔ agente v%s assinado (SHA-256 %s…)\n", versao, m.SHA256[:16])
	fmt.Printf("\nKit assinado com a chave raiz %s.\n", assinatura.Impressao(raiz))
	fmt.Println("Agora copie o kit para o servidor e rode o INSTALAR-SERVIDOR.bat.")
	fmt.Println("(Se o servidor já estiver instalado com esta versão, envie arquivos\\agente.assinatura em Administração → Assinatura.)")
	return nil
}

func lerVersao(pasta string) string {
	b, err := os.ReadFile(filepath.Join(pasta, "versao-agente.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func gravarSeguro(caminho string, dados []byte) error {
	tmp := caminho + ".novo"
	if err := os.WriteFile(tmp, dados, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, caminho)
}

// ---------------- 3) migração ----------------

func ordemMigracao() error {
	titulo("Ordem de migração de servidor")
	fmt.Println("Os agentes só mudam de servidor com esta ordem assinada. Cole o resultado em")
	fmt.Println("Administração → Migrar os agentes, no servidor ANTIGO.")
	srv := strings.TrimRight(perguntar("\nEndereço do NOVO servidor (ex.: http://10.100.15.40:8081)", ""), "/")
	if !strings.HasPrefix(srv, "http://") && !strings.HasPrefix(srv, "https://") {
		return errors.New("o endereço deve começar com http:// ou https://")
	}
	token := perguntar("Token dos agentes do NOVO servidor", "")
	if len(token) < 10 {
		return errors.New("token inválido")
	}
	dias, _ := strconv.Atoi(perguntar("Validade da ordem em dias", "7"))
	if dias < 1 || dias > 90 {
		dias = 7
	}
	priv, _, cert, err := abrirChave("")
	if err != nil {
		return err
	}
	o := assinatura.AssinarMigracao(priv, cert, srv, token, time.Duration(dias)*24*time.Hour)
	arq := filepath.Join(pastaPadrao(), "ordem-migracao-"+time.Now().Format("20060102-1504")+".txt")
	_ = os.WriteFile(arq, []byte(o.Texto()+"\r\n"), 0o600)
	fmt.Print("\nORDEM (copie a linha inteira):\n\n")
	fmt.Println(o.Texto())
	fmt.Printf("\nTambém salva em %s (contém o token: apague depois de usar).\n", arq)
	fmt.Printf("Vale até %s.\n", time.Now().Add(time.Duration(dias)*24*time.Hour).Format("02/01/2006 15:04"))
	return nil
}

// ---------------- 4) recuperar acesso ----------------

func recuperar() error {
	titulo("Recuperar acesso ao painel")
	fmt.Println("Abra http://SERVIDOR/recuperar (link \"Esqueci minha senha\" no login) e copie o código de desafio.")
	d := strings.ToUpper(strings.TrimSpace(perguntar("\nCódigo de desafio", "")))
	if len(d) < 10 {
		return errors.New("código de desafio inválido")
	}
	raiz, err := lerPalavrasRaiz()
	if err != nil {
		return err
	}
	fmt.Printf("\nChave raiz: %s (confira com a mostrada no painel)\n", assinatura.Impressao(raiz.Public().(ed25519.PublicKey)))
	fmt.Print("\nRESPOSTA (cole na página, com o usuário e a nova senha):\n\n")
	fmt.Println(assinatura.ResponderDesafio(raiz, d))
	return nil
}

// ---------------- 5) nova chave ----------------

func novaChave() error {
	titulo("Nova chave de assinatura")
	fmt.Println(`Use se o arquivo .chave sumiu, a senha foi esquecida ou ele pode ter vazado.
Com as 24 palavras é emitido um certificado NOVO: assim que os agentes o receberem,
a chave antiga deixa de valer em todas as máquinas.`)
	raiz, err := lerPalavrasRaiz()
	if err != nil {
		return err
	}
	destino, err := gerarChaveAssinatura(raiz, "harvest-ti.chave")
	if err != nil {
		return err
	}
	fmt.Printf("\n✔ Nova chave salva em %s (guarde no 1Password no lugar da antiga).\n", destino)
	fmt.Println(`
PARA A CHAVE ANTIGA PARAR DE VALER:
  Assine de novo o kit da versão instalada com a chave nova (opção 2, mesma pasta do kit)
  e envie o arquivos\agente.assinatura em Administração → Assinatura das atualizações.
  Os agentes recebem o certificado novo no próximo contato e passam a recusar a chave antiga.`)
	if k := limparCaminho(perguntar("\nPasta do kit para assinar agora (Enter para pular)", "")); k != "" {
		return assinarKit(k, destino)
	}
	return nil
}

// ---------------- 6) conferir ----------------

func conferir() error {
	titulo("Conferir as 24 palavras")
	raiz, err := lerPalavrasRaiz()
	if err != nil {
		return err
	}
	rpub := raiz.Public().(ed25519.PublicKey)
	fmt.Printf("\n✔ As palavras estão corretas (verificador OK).\n  Chave raiz: %s\n", assinatura.Impressao(rpub))
	arq := limparCaminho(perguntar("Arquivo .chave para comparar (Enter para pular)", os.Getenv("HARVEST_CHAVE")))
	if arq != "" {
		b, err := os.ReadFile(arq)
		if err != nil {
			return err
		}
		a, err := assinatura.LerArquivo(b)
		if err != nil {
			return err
		}
		if a.Raiz == hex.EncodeToString(rpub) {
			fmt.Println("  ✔ São as palavras deste arquivo de chave.")
		} else {
			fmt.Println("  ✘ NÃO são as palavras deste arquivo de chave.")
		}
	}
	return nil
}

// ---------------- auxiliares ----------------

func abrirChave(arq string) (ed25519.PrivateKey, ed25519.PublicKey, assinatura.Certificado, error) {
	if arq == "" {
		arq = os.Getenv("HARVEST_CHAVE")
	}
	if arq == "" {
		padrao := filepath.Join(pastaPadrao(), "harvest-ti.chave")
		if _, err := os.Stat(padrao); err != nil {
			padrao = ""
		}
		arq = perguntar("Arquivo da chave (harvest-ti.chave — arraste aqui)", padrao)
	}
	b, err := os.ReadFile(limparCaminho(arq))
	if err != nil {
		return nil, nil, assinatura.Certificado{}, err
	}
	a, err := assinatura.LerArquivo(b)
	if err != nil {
		return nil, nil, assinatura.Certificado{}, err
	}
	for i := 0; i < 3; i++ {
		priv, raiz, err := a.Abrir(senha("Senha da chave"))
		if err == nil {
			return priv, raiz, a.Certificado, nil
		}
		fmt.Println("  " + err.Error())
		if os.Getenv("HARVEST_SENHA") != "" {
			break
		}
	}
	return nil, nil, assinatura.Certificado{}, errors.New("não foi possível abrir a chave")
}

func lerPalavrasRaiz() (ed25519.PrivateKey, error) {
	texto := os.Getenv("HARVEST_PALAVRAS")
	if texto == "" {
		fmt.Println("\nDigite as 24 palavras (pode ser numa linha só ou uma por linha; bastam as 4 primeiras letras):")
		var b strings.Builder
		for n := 0; n < 24; {
			l, err := entrada.ReadString('\n')
			b.WriteString(" " + l)
			n = len(strings.Fields(strings.NewReplacer(",", " ", ";", " ").Replace(b.String())))
			if err != nil {
				break
			}
		}
		texto = b.String()
		limparTela()
	}
	ps, err := assinatura.LerPalavras(texto)
	if err != nil {
		return nil, err
	}
	return assinatura.RaizDasPalavras(ps)
}

func mostrarPalavras(ps []string) {
	fmt.Print("\n  SUAS 24 PALAVRAS:\n\n")
	for i := 0; i < 8; i++ {
		fmt.Printf("   %2d. %-12s   %2d. %-12s   %2d. %-12s\n", i+1, ps[i], i+9, ps[i+8], i+17, ps[i+16])
	}
	fmt.Println()
}

func senhaNova() (string, error) {
	if s := os.Getenv("HARVEST_SENHA"); s != "" {
		return s, nil
	}
	fmt.Println("\nCrie uma senha para o arquivo da chave (mínimo 10 caracteres; guarde no 1Password).")
	for i := 0; i < 3; i++ {
		a := senha("Senha")
		if len([]rune(a)) < 10 {
			fmt.Println("  Muito curta.")
			continue
		}
		if senha("Repita a senha") != a {
			fmt.Println("  As senhas não são iguais.")
			continue
		}
		return a, nil
	}
	return "", errors.New("senha não definida")
}

func senha(rotulo string) string {
	if s := os.Getenv("HARVEST_SENHA"); s != "" {
		return s
	}
	fmt.Print(rotulo + ": ")
	s := lerSenhaOculta()
	fmt.Println()
	return s
}

func perguntar(rotulo, padrao string) string {
	if padrao != "" {
		fmt.Printf("%s [%s]: ", rotulo, padrao)
	} else {
		fmt.Print(rotulo + ": ")
	}
	l, _ := entrada.ReadString('\n')
	l = strings.TrimSpace(l)
	if l == "" {
		return padrao
	}
	return l
}

func limparCaminho(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	return strings.TrimRight(s, `\/`)
}

func pastaPadrao() string {
	if h, err := os.UserHomeDir(); err == nil {
		for _, d := range []string{filepath.Join(h, "Desktop"), filepath.Join(h, "Área de Trabalho"), h} {
			if fi, err := os.Stat(d); err == nil && fi.IsDir() {
				return d
			}
		}
	}
	return "."
}

func titulo(t string) {
	fmt.Println("\n== " + t + " " + strings.Repeat("=", max(0, 60-len([]rune(t)))))
}

func pausar() {
	if runtime.GOOS == "windows" && os.Getenv("HARVEST_SENHA") == "" {
		fmt.Print("\nPressione Enter para continuar...")
		_, _ = entrada.ReadString('\n')
	}
}

func limparTela() {
	if runtime.GOOS == "windows" {
		c := exec.Command("cmd", "/c", "cls")
		c.Stdout = os.Stdout
		_ = c.Run()
		return
	}
	fmt.Print("\033[H\033[2J")
}
