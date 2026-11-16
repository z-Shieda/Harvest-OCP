package servidor

import (
	"os"
	"strings"
	"testing"
)

func TestSementesValidas(t *testing.T) {
	b := bancoTeste(t)
	tipos := b.TiposTermo(false)
	if len(tipos) != 2 {
		t.Fatalf("esperava 2 tipos semeados, veio %d", len(tipos))
	}
	for _, tp := range tipos {
		v := tp.Publicada
		if v == nil {
			t.Fatalf("%s sem versão publicada", tp.Chave)
		}
		avisos, err := ConferirModelo(v.Corpo, v.CamposDef, v.AceitesDef, b.chavesDocumentos())
		if err != nil {
			t.Fatalf("%s: %v", tp.Chave, err)
		}
		t.Logf("%s avisos: %v", tp.Chave, avisos)
	}
	if d, err := b.Documento("psi"); err != nil || !strings.Contains(d.Corpo, "ANEXO II") {
		t.Fatal("PSI não semeada")
	}
}

func TestMarcacao(t *testing.T) {
	bs := lerMarcacao("# Título\n\n## 1. Seção\nlinha um\nlinha dois\n\n6.1 Item **forte**\n- marcador\n> destaque\n| a | b |\n|---|---|\n| {x} | [x] |\n{lista}\n[[devolucao]]\nsó na devolução\n[[/devolucao]]\n===")
	tipos := []string{}
	for _, b := range bs {
		tipos = append(tipos, b.tipo)
	}
	esperado := "titulo secao par item marcador destaque tabela campo par quebra"
	if strings.Join(tipos, " ") != esperado {
		t.Fatalf("blocos: %v", tipos)
	}
	if bs[2].texto != "linha um linha dois" || bs[6].cabecalho != 1 || bs[8].cond != "devolucao" {
		t.Fatalf("detalhes: %+v", bs)
	}
}

func TestCamposDSL(t *testing.T) {
	_, err := LerCampos("x | X | inexistente | ti")
	if err == nil {
		t.Fatal("tipo desconhecido deveria falhar")
	}
	if _, err := LerCampos("lista | L | checklist | ti"); err == nil {
		t.Fatal("checklist sem itens deveria falhar")
	}
	if _, err := LerCampos("nao_sei | X | auto | auto"); err == nil {
		t.Fatal("automático fora do catálogo deveria falhar")
	}
	if _, err := ConferirModelo("texto {sem_declarar}", "", "", nil); err == nil {
		t.Fatal("campo não declarado no texto deveria falhar")
	}
	cs, err := LerCampos("local | Local | texto | colaborador | se=regime:hibrido,teletrabalho\nl | Lista | checklist | ti | itens=A;B | colunas=Sim;Não")
	if err != nil {
		t.Fatal(err)
	}
	v := novosValores()
	v.Texto["colaborador_regime_chave"] = "presencial"
	if p := Pendencias(cs, v, "colaborador", 0); len(p) != 0 {
		t.Fatalf("presencial não precisa de local: %v", p)
	}
	v.Texto["colaborador_regime_chave"] = "hibrido"
	if p := Pendencias(cs, v, "colaborador", 0); len(p) != 1 {
		t.Fatalf("híbrido precisa de local: %v", p)
	}
	form := map[string]string{"cl_l_0": "Sim", "cl_l_1": "Talvez"}
	if err := LerValoresForm(cs, "ti", &v, func(k string) string { return form[k] }); err == nil {
		t.Fatal("coluna inválida deveria falhar")
	}
	form["cl_l_1"] = "Não"
	if err := LerValoresForm(cs, "ti", &v, func(k string) string { return form[k] }); err != nil || len(Pendencias(cs, v, "ti", 0)) != 0 {
		t.Fatalf("checklist completa: %v", err)
	}
}

func TestPDFDoTermoDeEntrega(t *testing.T) {
	b := bancoTeste(t)
	s, _ := Novo(b, "teste")
	var tp *TipoTermo
	for _, x := range b.TiposTermo(false) {
		if x.Chave == "entrega" {
			tp = x
		}
	}
	v := tp.Publicada
	vals := novosValores()
	for k, r := range camposAuto {
		vals.Texto[k] = "valor de " + r
	}
	vals.Texto["colaborador_nome"] = "José da Conceição Ávila"
	vals.Checklists["acessorios"] = map[string]ValorChecklist{"Mouse": {V: "Entregue", Obs: "Logitech M90"}, "Headset": {V: "Não"}}
	m := novaMontagem(v.CamposDef, v.AceitesDef, vals)
	m.marcados = []bool{true, true}
	m.evid = &Evidencias{AceitoPor: "José (jose@ocupacional.com.br)", Hash: strings.Repeat("ab", 32), Codigo: "ABCD-EF12-3456", Maquina: "OCP-123 (confirmada)"}
	bs := m.resolver(v.Corpo)
	txt := textoPuro(bs)
	if !strings.Contains(txt, "José da Conceição Ávila") || !strings.Contains(txt, "| Mouse | [X] | [ ] | Logitech M90 |") {
		t.Fatalf("texto puro sem os valores:\n%s", txt[:min(len(txt), 1500)])
	}
	if strings.Contains(txt, "QUADRO DE DEVOLUÇÃO") {
		t.Fatal("quadro de devolução não deveria aparecer antes da devolução")
	}
	if h := string(paraHTML(bs, b.titulosDocumentos())); !strings.Contains(h, "td-tabela") || strings.Contains(h, "<script") {
		t.Fatal("HTML do termo")
	}
	p := paraPDF(bs, CabecalhoPDF{Logo: s.logoPDF(), NomeMarca: "Ocupacional", CorSec: hexPDF("#18424C"), CorPri: hexPDF("#ADDA23"), Direita: "Termo nº 0001 · TRE-v1.0", Rodape: "Código de verificação ABCD-EF12-3456"})
	if len(p) < 2000 || !strings.HasPrefix(string(p), "%PDF") {
		t.Fatal("PDF inválido")
	}
	if os.Getenv("PDF_SAIDA") != "" {
		_ = os.WriteFile(os.Getenv("PDF_SAIDA"), p, 0o644)
	}
}

func TestPropriaMaquina(t *testing.T) {
	tp := &TipoTermo{ExigePropriaMaquina: true}
	tm := &Termo{MaquinaID: 7, Equipamento: "OCP-7"}
	if conferirPropriaMaquina(tp, tm, MaquinaAssinatura{ID: 7, Nome: "OCP-7", Grau: "provavel"}) != nil {
		t.Fatal("provável na própria máquina deveria passar")
	}
	if conferirPropriaMaquina(tp, tm, MaquinaAssinatura{ID: 8, Nome: "OCP-8", Grau: "confirmada"}) == nil {
		t.Fatal("outra máquina deveria ser recusada")
	}
	if conferirPropriaMaquina(tp, tm, MaquinaAssinatura{Grau: "nao_identificada"}) == nil {
		t.Fatal("não identificada deveria ser recusada")
	}
	tm.LiberarMaquinaMotivo = "sem acesso"
	if conferirPropriaMaquina(tp, tm, MaquinaAssinatura{Grau: "nao_identificada"}) != nil {
		t.Fatal("liberado pela T.I deveria passar")
	}
	if formatarReais("4500") != "R$ 4.500,00" || formatarReais("1234567.5") != "R$ 1.234.567,50" {
		t.Fatal("formatarReais")
	}
	if v, err := normalizarValor("R$ 3.500,90"); err != nil || v != "3500.90" {
		t.Fatalf("normalizarValor: %v %v", v, err)
	}
	if codigoVerificacao("abcdef0123456789") != "ABCD-EF01-2345" {
		t.Fatal("código de verificação")
	}
}
