package servidor

// Termos que já vêm cadastrados (1.23): Termo de Entrega (TRE-v1.0, por equipamento) e Anexo II
// da PSI v4 (por pessoa), mais a PSI v4 completa como documento de referência. Textos em
// web/termos/*.txt (convertidos dos modelos em docs/termos/). Só entram num banco sem tipos.

import "log"

func textoSemente(nome string) string {
	b, err := arquivosWeb.ReadFile("web/termos/" + nome)
	if err != nil {
		log.Printf("termos: semente %s ausente: %v", nome, err)
		return ""
	}
	return string(b)
}

func (b *Banco) semearTermos() {
	var n int
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM termo_documentos`).Scan(&n)
	if n == 0 {
		_ = b.SalvarDocumento("psi", "Política de Segurança da Informação (PSI)", "v4", textoSemente("psi_v4.txt"), "Harvest")
	}
	_ = b.db.QueryRow(`SELECT COUNT(*) FROM termo_tipos`).Scan(&n)
	if n > 0 {
		return
	}
	sementes := []struct {
		tipo                   TipoTermo
		sigla, corpo, campos   string
		aceites, usoInterno    string
	}{
		{
			tipo: TipoTermo{Chave: "entrega", Nome: "Termo de Entrega, Responsabilidade e Uso de Equipamento Corporativo", Abrangencia: "equipamento",
				Vinculos: []string{"clt", "estagio", "aprendiz", "temporario"}, AbreChamado: true, CategoriaChamado: "Entrega de equipamento",
				TemDevolucao: true, ExigeCodigo: true, EnviaRH: true, Ativo: true},
			sigla: "TRE-v1.0", corpo: textoSemente("tre_corpo.txt"), campos: textoSemente("tre_campos.txt"),
			aceites: textoSemente("tre_aceites.txt"), usoInterno: textoSemente("tre_uso_interno.txt"),
		},
		{
			tipo: TipoTermo{Chave: "anexo2", Nome: "Termo de Ciência e Responsabilidade sobre Monitoramento (PSI v4, Anexo II)", Abrangencia: "pessoa",
				Vinculos: []string{"clt", "estagio", "aprendiz", "temporario", "pj"}, ExigeCodigo: true, EnviaRH: true, Ativo: true},
			sigla: "PSI4-AII-v1.0", corpo: textoSemente("anexo2_corpo.txt"), campos: textoSemente("anexo2_campos.txt"),
			aceites: textoSemente("anexo2_aceites.txt"),
		},
	}
	for _, s := range sementes {
		if s.corpo == "" {
			continue
		}
		id, err := b.SalvarTipo(s.tipo)
		if err != nil {
			log.Printf("termos: semente %s: %v", s.tipo.Chave, err)
			continue
		}
		_, err = b.db.Exec(`INSERT INTO termo_versoes(tipo_id,sigla,corpo,campos_def,aceites_def,uso_interno,situacao,criado_em,criado_por,atualizado_em,previa_em,publicado_em,publicado_por)
			VALUES(?,?,?,?,?,?,'publicada',?,'Harvest',?,?,?,'Harvest')`, id, s.sigla, s.corpo, s.campos, s.aceites, s.usoInterno, agora(), agora(), agora(), agora())
		if err != nil {
			log.Printf("termos: semente %s: %v", s.tipo.Chave, err)
		}
	}
}
