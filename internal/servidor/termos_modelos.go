package servidor

// Editor de termos (1.23, fase 2): tipos de termo, versões (rascunho → publicada) e documentos
// de referência (PSI). Publicar congela texto, campos e aceites; termos já emitidos continuam
// ligados à versão que a pessoa leu.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const esquemaTermos2 = `
CREATE TABLE IF NOT EXISTS termo_tipos (
	id INTEGER PRIMARY KEY,
	chave TEXT NOT NULL UNIQUE,
	nome TEXT NOT NULL,
	abrangencia TEXT NOT NULL DEFAULT 'pessoa',
	vinculos TEXT NOT NULL DEFAULT 'clt,estagio,aprendiz,temporario,pj',
	abre_chamado INTEGER NOT NULL DEFAULT 0,
	categoria_chamado TEXT NOT NULL DEFAULT '',
	tem_devolucao INTEGER NOT NULL DEFAULT 0,
	exige_codigo INTEGER NOT NULL DEFAULT 1,
	exige_propria_maquina INTEGER NOT NULL DEFAULT 0,
	envia_rh INTEGER NOT NULL DEFAULT 1,
	ativo INTEGER NOT NULL DEFAULT 1,
	ordem INTEGER NOT NULL DEFAULT 0,
	criado_em TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS termo_versoes (
	id INTEGER PRIMARY KEY,
	tipo_id INTEGER NOT NULL REFERENCES termo_tipos(id) ON DELETE CASCADE,
	sigla TEXT NOT NULL,
	corpo TEXT NOT NULL DEFAULT '',
	campos_def TEXT NOT NULL DEFAULT '',
	aceites_def TEXT NOT NULL DEFAULT '',
	uso_interno TEXT NOT NULL DEFAULT '',
	exige_reassinatura INTEGER NOT NULL DEFAULT 0,
	situacao TEXT NOT NULL DEFAULT 'rascunho',
	criado_em TEXT NOT NULL DEFAULT '',
	criado_por TEXT NOT NULL DEFAULT '',
	atualizado_em TEXT NOT NULL DEFAULT '',
	previa_em TEXT NOT NULL DEFAULT '',
	publicado_em TEXT NOT NULL DEFAULT '',
	publicado_por TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_termo_versoes_tipo ON termo_versoes(tipo_id);
CREATE TABLE IF NOT EXISTS termo_documentos (
	id INTEGER PRIMARY KEY,
	chave TEXT NOT NULL UNIQUE,
	titulo TEXT NOT NULL,
	versao TEXT NOT NULL DEFAULT '',
	corpo TEXT NOT NULL DEFAULT '',
	atualizado_em TEXT NOT NULL DEFAULT '',
	atualizado_por TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS termos (
	id INTEGER PRIMARY KEY,
	numero INTEGER NOT NULL UNIQUE,
	tipo_id INTEGER NOT NULL REFERENCES termo_tipos(id),
	versao_id INTEGER NOT NULL REFERENCES termo_versoes(id),
	admin_id INTEGER NOT NULL REFERENCES admins(id),
	maquina_id INTEGER REFERENCES maquinas(id) ON DELETE SET NULL,
	ativo_id INTEGER,
	equipamento TEXT NOT NULL DEFAULT '',
	chamado_id INTEGER REFERENCES chamados(id) ON DELETE SET NULL,
	situacao TEXT NOT NULL DEFAULT 'rascunho',
	valores_json TEXT NOT NULL DEFAULT '{}',
	criado_por TEXT NOT NULL DEFAULT '',
	criado_em TEXT NOT NULL DEFAULT '',
	enviado_por TEXT NOT NULL DEFAULT '',
	enviado_em TEXT NOT NULL DEFAULT '',
	assinado_em TEXT NOT NULL DEFAULT '',
	substituido_por INTEGER,
	substitui INTEGER,
	cancelado_em TEXT NOT NULL DEFAULT '',
	cancelado_por TEXT NOT NULL DEFAULT '',
	motivo TEXT NOT NULL DEFAULT '',
	liberar_maquina_motivo TEXT NOT NULL DEFAULT '',
	liberar_maquina_por TEXT NOT NULL DEFAULT '',
	lembrete_em TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_termos_admin ON termos(admin_id);
CREATE INDEX IF NOT EXISTS idx_termos_maquina ON termos(maquina_id);
CREATE TABLE IF NOT EXISTS termo_assinaturas (
	id INTEGER PRIMARY KEY,
	termo_id INTEGER NOT NULL REFERENCES termos(id),
	etapa TEXT NOT NULL DEFAULT 'entrega',
	aceites_json TEXT NOT NULL DEFAULT '[]',
	assinado_em TEXT NOT NULL,
	ip TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT '',
	maquina_assinatura_id INTEGER,
	maquina_assinatura TEXT NOT NULL DEFAULT '',
	maquina_grau TEXT NOT NULL DEFAULT '',
	maquinas_candidatas TEXT NOT NULL DEFAULT '',
	metodo TEXT NOT NULL DEFAULT '',
	email_codigo TEXT NOT NULL DEFAULT '',
	texto_final TEXT NOT NULL DEFAULT '',
	hash_sha256 TEXT NOT NULL DEFAULT '',
	codigo_verificacao TEXT NOT NULL UNIQUE,
	pdf BLOB
);
CREATE INDEX IF NOT EXISTS idx_termo_assinaturas_termo ON termo_assinaturas(termo_id);
CREATE TABLE IF NOT EXISTS termo_codigos (
	id INTEGER PRIMARY KEY,
	admin_id INTEGER NOT NULL,
	termo_id INTEGER NOT NULL,
	codigo_hash TEXT NOT NULL,
	expira TEXT NOT NULL,
	tentativas INTEGER NOT NULL DEFAULT 0,
	usado INTEGER NOT NULL DEFAULT 0,
	criado_em TEXT NOT NULL,
	ip TEXT NOT NULL DEFAULT '',
	maquina_json TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_termo_codigos ON termo_codigos(admin_id, termo_id);
CREATE TABLE IF NOT EXISTS ativos_manuais (
	id INTEGER PRIMARY KEY,
	tipo TEXT NOT NULL DEFAULT '',
	fabricante TEXT NOT NULL DEFAULT '',
	modelo TEXT NOT NULL DEFAULT '',
	serial TEXT NOT NULL DEFAULT '',
	patrimonio TEXT NOT NULL DEFAULT '',
	imei TEXT NOT NULL DEFAULT '',
	linha TEXT NOT NULL DEFAULT '',
	condicao TEXT NOT NULL DEFAULT '',
	nf_numero TEXT NOT NULL DEFAULT '',
	nf_data TEXT NOT NULL DEFAULT '',
	valor_referencia TEXT NOT NULL DEFAULT '',
	observacoes TEXT NOT NULL DEFAULT '',
	ativo INTEGER NOT NULL DEFAULT 1,
	criado_em TEXT NOT NULL DEFAULT '',
	criado_por TEXT NOT NULL DEFAULT ''
);
`

func (b *Banco) migrarTermos2() {
	_, _ = b.db.Exec(esquemaTermos2)
	for _, alter := range []string{
		// gestão do ativo (D13) e segurança informada pelo agente 1.22 (D11)
		`ALTER TABLE maquinas ADD COLUMN tipo_equip TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE maquinas ADD COLUMN condicao TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE maquinas ADD COLUMN nf_numero TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE maquinas ADD COLUMN nf_data TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE maquinas ADD COLUMN valor_referencia TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE maquinas ADD COLUMN seg_bitlocker TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE maquinas ADD COLUMN seg_antivirus TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE maquinas ADD COLUMN seg_antivirus_ok TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE maquinas ADD COLUMN seg_admins_locais TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE maquinas ADD COLUMN seg_ultima_atualizacao TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE maquinas ADD COLUMN seg_coletado_em TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = b.db.Exec(alter)
	}
	b.semearTermos()
}

// ---------------- tipos ----------------

type TipoTermo struct {
	ID                  int64
	Chave, Nome         string
	Abrangencia         string // pessoa | equipamento
	Vinculos            []string
	AbreChamado         bool
	CategoriaChamado    string
	TemDevolucao        bool
	ExigeCodigo         bool
	ExigePropriaMaquina bool
	EnviaRH             bool
	Ativo               bool
	// leitura
	Publicada *VersaoTermo
	Rascunho  *VersaoTermo
}

func (t TipoTermo) PorEquipamento() bool { return t.Abrangencia == "equipamento" }
func (t TipoTermo) AceitaVinculo(v string) bool {
	return temValor(t.Vinculos, v)
}
func (t TipoTermo) VinculosTexto() string {
	var l []string
	for _, v := range t.Vinculos {
		l = append(l, rotuloVinculo(v))
	}
	return strings.Join(l, ", ")
}

const colunasTipo = `id, chave, nome, abrangencia, vinculos, abre_chamado, categoria_chamado, tem_devolucao, exige_codigo, exige_propria_maquina, envia_rh, ativo`

func lerTipo(sc interface{ Scan(...any) error }) (*TipoTermo, error) {
	var t TipoTermo
	var vinc string
	if err := sc.Scan(&t.ID, &t.Chave, &t.Nome, &t.Abrangencia, &vinc, &t.AbreChamado, &t.CategoriaChamado, &t.TemDevolucao,
		&t.ExigeCodigo, &t.ExigePropriaMaquina, &t.EnviaRH, &t.Ativo); err != nil {
		return nil, err
	}
	t.Vinculos = dividir(vinc, ",")
	return &t, nil
}

func (b *Banco) TiposTermo(soAtivos bool) []*TipoTermo {
	q := `SELECT ` + colunasTipo + ` FROM termo_tipos`
	if soAtivos {
		q += ` WHERE ativo=1`
	}
	rows, err := b.db.Query(q + ` ORDER BY ordem, nome`)
	if err != nil {
		return nil
	}
	var l []*TipoTermo
	for rows.Next() {
		if t, err := lerTipo(rows); err == nil {
			l = append(l, t)
		}
	}
	rows.Close()
	for _, t := range l {
		t.Publicada = b.VersaoPublicada(t.ID)
		t.Rascunho = b.rascunhoDoTipo(t.ID)
	}
	return l
}

func (b *Banco) TipoTermo(id int64) (*TipoTermo, error) {
	t, err := lerTipo(b.db.QueryRow(`SELECT `+colunasTipo+` FROM termo_tipos WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	t.Publicada = b.VersaoPublicada(t.ID)
	t.Rascunho = b.rascunhoDoTipo(t.ID)
	return t, nil
}

// SalvarTipo cria (id 0) ou altera as regras de um tipo de termo.
func (b *Banco) SalvarTipo(t TipoTermo) (int64, error) {
	t.Nome = strings.Join(strings.Fields(t.Nome), " ")
	if t.Nome == "" || len([]rune(t.Nome)) > 160 {
		return 0, errors.New("informe o nome do termo")
	}
	if t.Abrangencia != "equipamento" {
		t.Abrangencia = "pessoa"
	}
	var vs []string
	for _, v := range t.Vinculos {
		if temValor(vinculosFicha, v) {
			vs = append(vs, v)
		}
	}
	if len(vs) == 0 {
		return 0, errors.New("marque pelo menos um vínculo que pode assinar")
	}
	if t.Abrangencia == "pessoa" {
		t.TemDevolucao, t.ExigePropriaMaquina = false, false
	}
	t.CategoriaChamado = strings.TrimSpace(t.CategoriaChamado)
	if t.AbreChamado && t.CategoriaChamado == "" {
		t.CategoriaChamado = "Entrega de equipamento"
	}
	if t.ID > 0 {
		_, err := b.db.Exec(`UPDATE termo_tipos SET nome=?, abrangencia=?, vinculos=?, abre_chamado=?, categoria_chamado=?, tem_devolucao=?, exige_codigo=?, exige_propria_maquina=?, envia_rh=?, ativo=? WHERE id=?`,
			t.Nome, t.Abrangencia, strings.Join(vs, ","), t.AbreChamado, t.CategoriaChamado, t.TemDevolucao, t.ExigeCodigo, t.ExigePropriaMaquina, t.EnviaRH, t.Ativo, t.ID)
		return t.ID, err
	}
	if t.Chave == "" {
		t.Chave = "tipo" + aleatorio(4)
	}
	x, err := b.db.Exec(`INSERT INTO termo_tipos(chave,nome,abrangencia,vinculos,abre_chamado,categoria_chamado,tem_devolucao,exige_codigo,exige_propria_maquina,envia_rh,ativo,ordem,criado_em)
		VALUES(?,?,?,?,?,?,?,?,?,?,?, (SELECT COALESCE(MAX(ordem),0)+1 FROM termo_tipos), ?)`,
		t.Chave, t.Nome, t.Abrangencia, strings.Join(vs, ","), t.AbreChamado, t.CategoriaChamado, t.TemDevolucao, t.ExigeCodigo, t.ExigePropriaMaquina, t.EnviaRH, t.Ativo, agora())
	if err != nil {
		return 0, err
	}
	return x.LastInsertId()
}

// ---------------- versões ----------------

type VersaoTermo struct {
	ID                 int64
	TipoID             int64
	Sigla              string
	Corpo              string
	CamposDef          string
	AceitesDef         string
	UsoInterno         string
	ExigeReassinatura  bool
	Situacao           string // rascunho | publicada | arquivada
	CriadoEm, CriadoPor string
	AtualizadoEm       string
	PreviaEm           string
	PublicadoEm        string
	PublicadoPor       string
}

func (v *VersaoTermo) Campos() []CampoTermo   { c, _ := LerCampos(v.CamposDef); return c }
func (v *VersaoTermo) Aceites() []AceiteTermo { a, _ := LerAceites(v.AceitesDef); return a }

// CamposDe devolve os campos de quem preenche (ti, colaborador, devolucao).
func (v *VersaoTermo) CamposDe(quem string) []CampoTermo {
	var l []CampoTermo
	for _, c := range v.Campos() {
		if c.Quem == quem {
			l = append(l, c)
		}
	}
	return l
}

const colunasVersao = `id, tipo_id, sigla, corpo, campos_def, aceites_def, uso_interno, exige_reassinatura, situacao, criado_em, criado_por, atualizado_em, previa_em, publicado_em, publicado_por`

func lerVersao(sc interface{ Scan(...any) error }) (*VersaoTermo, error) {
	var v VersaoTermo
	err := sc.Scan(&v.ID, &v.TipoID, &v.Sigla, &v.Corpo, &v.CamposDef, &v.AceitesDef, &v.UsoInterno, &v.ExigeReassinatura, &v.Situacao,
		&v.CriadoEm, &v.CriadoPor, &v.AtualizadoEm, &v.PreviaEm, &v.PublicadoEm, &v.PublicadoPor)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (b *Banco) Versao(id int64) (*VersaoTermo, error) {
	return lerVersao(b.db.QueryRow(`SELECT `+colunasVersao+` FROM termo_versoes WHERE id=?`, id))
}

func (b *Banco) VersaoPublicada(tipoID int64) *VersaoTermo {
	v, err := lerVersao(b.db.QueryRow(`SELECT `+colunasVersao+` FROM termo_versoes WHERE tipo_id=? AND situacao='publicada' ORDER BY publicado_em DESC, id DESC LIMIT 1`, tipoID))
	if err != nil {
		return nil
	}
	return v
}

func (b *Banco) rascunhoDoTipo(tipoID int64) *VersaoTermo {
	v, err := lerVersao(b.db.QueryRow(`SELECT `+colunasVersao+` FROM termo_versoes WHERE tipo_id=? AND situacao='rascunho' ORDER BY id DESC LIMIT 1`, tipoID))
	if err != nil {
		return nil
	}
	return v
}

func (b *Banco) VersoesDoTipo(tipoID int64) []*VersaoTermo {
	rows, err := b.db.Query(`SELECT `+colunasVersao+` FROM termo_versoes WHERE tipo_id=? ORDER BY id DESC`, tipoID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []*VersaoTermo
	for rows.Next() {
		if v, err := lerVersao(rows); err == nil {
			l = append(l, v)
		}
	}
	return l
}

// AbrirRascunho devolve o rascunho do tipo; sem rascunho, cria um a partir da versão publicada.
func (b *Banco) AbrirRascunho(tipoID int64, por string) (*VersaoTermo, error) {
	if r := b.rascunhoDoTipo(tipoID); r != nil {
		return r, nil
	}
	base := b.VersaoPublicada(tipoID)
	v := VersaoTermo{TipoID: tipoID, Sigla: "v1.0"}
	if base != nil {
		v = *base
		v.Sigla = proximaSigla(base.Sigla)
		v.ExigeReassinatura = false
	}
	x, err := b.db.Exec(`INSERT INTO termo_versoes(tipo_id,sigla,corpo,campos_def,aceites_def,uso_interno,exige_reassinatura,situacao,criado_em,criado_por,atualizado_em)
		VALUES(?,?,?,?,?,?,0,'rascunho',?,?,?)`, tipoID, v.Sigla, v.Corpo, v.CamposDef, v.AceitesDef, v.UsoInterno, agora(), por, agora())
	if err != nil {
		return nil, err
	}
	id, _ := x.LastInsertId()
	return b.Versao(id)
}

// proximaSigla: "TRE-v1.0" → "TRE-v1.1".
func proximaSigla(s string) string {
	i := strings.LastIndex(s, ".")
	if i < 0 {
		return s + ".1"
	}
	var n int
	if _, err := fmt.Sscanf(s[i+1:], "%d", &n); err != nil {
		return s + ".1"
	}
	return fmt.Sprintf("%s.%d", s[:i], n+1)
}

// SalvarRascunho grava o rascunho (só rascunho pode mudar). Devolve avisos do conferidor.
func (b *Banco) SalvarRascunho(id int64, sigla, corpo, campos, aceites, usoInterno string, reassinatura bool) ([]string, error) {
	v, err := b.Versao(id)
	if err != nil {
		return nil, err
	}
	if v.Situacao != "rascunho" {
		return nil, errors.New("só o rascunho pode ser alterado; versões publicadas ficam congeladas")
	}
	sigla = strings.TrimSpace(sigla)
	if sigla == "" || len(sigla) > 40 {
		return nil, errors.New("informe a sigla da versão (ex.: TRE-v1.1)")
	}
	var outra int64
	_ = b.db.QueryRow(`SELECT id FROM termo_versoes WHERE tipo_id=? AND sigla=? AND id<>?`, v.TipoID, sigla, id).Scan(&outra)
	if outra > 0 {
		return nil, errors.New("já existe uma versão com essa sigla")
	}
	if len(corpo) > 200000 || len(campos) > 50000 || len(aceites) > 10000 || len(usoInterno) > 50000 {
		return nil, errors.New("texto grande demais")
	}
	avisos, err := ConferirModelo(corpo, campos, aceites, b.chavesDocumentos())
	if err != nil {
		return nil, err
	}
	_, err = b.db.Exec(`UPDATE termo_versoes SET sigla=?, corpo=?, campos_def=?, aceites_def=?, uso_interno=?, exige_reassinatura=?, atualizado_em=?, previa_em='' WHERE id=?`,
		sigla, strings.ReplaceAll(corpo, "\r", ""), strings.ReplaceAll(campos, "\r", ""), strings.ReplaceAll(aceites, "\r", ""), strings.ReplaceAll(usoInterno, "\r", ""), reassinatura, agora(), id)
	return avisos, err
}

// MarcarPrevia registra que a prévia em PDF do rascunho atual foi gerada (exigida antes de publicar).
func (b *Banco) MarcarPrevia(id int64) {
	_, _ = b.db.Exec(`UPDATE termo_versoes SET previa_em=? WHERE id=? AND situacao='rascunho'`, agora(), id)
}

// PublicarVersao congela o rascunho. A anterior vira "arquivada" (termos antigos continuam ligados a ela).
func (b *Banco) PublicarVersao(id int64, por string) (*VersaoTermo, error) {
	v, err := b.Versao(id)
	if err != nil {
		return nil, err
	}
	if v.Situacao != "rascunho" {
		return nil, errors.New("essa versão já foi publicada")
	}
	if v.PreviaEm == "" {
		return nil, errors.New("gere a prévia em PDF antes de publicar (ela confere o texto com uma pessoa e uma máquina reais)")
	}
	if _, err := ConferirModelo(v.Corpo, v.CamposDef, v.AceitesDef, b.chavesDocumentos()); err != nil {
		return nil, err
	}
	tx, err := b.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE termo_versoes SET situacao='arquivada' WHERE tipo_id=? AND situacao='publicada'`, v.TipoID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE termo_versoes SET situacao='publicada', publicado_em=?, publicado_por=? WHERE id=?`, agora(), por, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return b.Versao(id)
}

// DescartarRascunho apaga um rascunho não publicado.
func (b *Banco) DescartarRascunho(id int64) error {
	_, err := b.db.Exec(`DELETE FROM termo_versoes WHERE id=? AND situacao='rascunho'`, id)
	return err
}

// ---------------- documentos de referência ----------------

type DocumentoRef struct {
	ID            int64
	Chave, Titulo string
	Versao        string
	Corpo         string
	AtualizadoEm  string
	AtualizadoPor string
}

func (b *Banco) Documentos() []DocumentoRef {
	rows, err := b.db.Query(`SELECT id, chave, titulo, versao, '', atualizado_em, atualizado_por FROM termo_documentos ORDER BY titulo`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var l []DocumentoRef
	for rows.Next() {
		var d DocumentoRef
		if rows.Scan(&d.ID, &d.Chave, &d.Titulo, &d.Versao, &d.Corpo, &d.AtualizadoEm, &d.AtualizadoPor) == nil {
			l = append(l, d)
		}
	}
	return l
}

func (b *Banco) Documento(chave string) (*DocumentoRef, error) {
	var d DocumentoRef
	err := b.db.QueryRow(`SELECT id, chave, titulo, versao, corpo, atualizado_em, atualizado_por FROM termo_documentos WHERE chave=?`, chave).
		Scan(&d.ID, &d.Chave, &d.Titulo, &d.Versao, &d.Corpo, &d.AtualizadoEm, &d.AtualizadoPor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("documento não encontrado")
	}
	return &d, err
}

func (b *Banco) chavesDocumentos() map[string]bool {
	m := map[string]bool{}
	for _, d := range b.Documentos() {
		m[d.Chave] = true
	}
	return m
}

func (b *Banco) titulosDocumentos() map[string]string {
	m := map[string]string{}
	for _, d := range b.Documentos() {
		m[d.Chave] = d.Titulo
	}
	return m
}

func (b *Banco) SalvarDocumento(chave, titulo, versao, corpo, por string) error {
	chave = strings.ToLower(strings.TrimSpace(chave))
	if !reChave.MatchString(chave) {
		return errors.New("chave inválida (letras minúsculas, números e _)")
	}
	titulo = strings.TrimSpace(titulo)
	if titulo == "" || strings.TrimSpace(corpo) == "" {
		return errors.New("informe o título e o texto do documento")
	}
	if len(corpo) > 400000 {
		return errors.New("texto grande demais")
	}
	_, err := b.db.Exec(`INSERT INTO termo_documentos(chave,titulo,versao,corpo,atualizado_em,atualizado_por) VALUES(?,?,?,?,?,?)
		ON CONFLICT(chave) DO UPDATE SET titulo=excluded.titulo, versao=excluded.versao, corpo=excluded.corpo, atualizado_em=excluded.atualizado_em, atualizado_por=excluded.atualizado_por`,
		chave, titulo, strings.TrimSpace(versao), strings.ReplaceAll(corpo, "\r", ""), agora(), por)
	return err
}
