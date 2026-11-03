// Package modelo define o formato do relatório enviado pelo agente ao servidor.
package modelo

// Relatorio é o inventário completo de uma máquina.
type Relatorio struct {
	AgentID       string `json:"agent_id"`
	AgentVersion  string `json:"agent_version"`
	ServidorAtual string `json:"servidor_atual"` // endereço que este agente está usando agora
	ColetadoEm    string `json:"coletado_em"`    // RFC3339 UTC

	Hostname   string `json:"hostname"`
	Dominio    string `json:"dominio"`
	NoDominio  bool   `json:"no_dominio"`
	Fabricante string `json:"fabricante"`
	Modelo     string `json:"modelo"`
	Serial     string `json:"serial"`
	HWUUID     string `json:"hw_uuid"`
	BIOS       string `json:"bios"`

	SOName        string `json:"so_nome"`
	SOVersao      string `json:"so_versao"`
	SOBuild       string `json:"so_build"`
	SOArquitetura string `json:"so_arquitetura"`
	SOInstalacao  string `json:"so_instalacao"`
	UltimoBoot    string `json:"ultimo_boot"`

	CPU        string `json:"cpu"`
	CPUNucleos int    `json:"cpu_nucleos"`
	CPUThreads int    `json:"cpu_threads"`
	RAMBytes   uint64 `json:"ram_bytes"`

	Memorias  []Memoria  `json:"memorias"`
	Discos    []Disco    `json:"discos"`
	Programas []Programa `json:"programas"`
	Volumes   []Volume   `json:"volumes"`
	GPUs      []string   `json:"gpus"`
	Redes     []Rede     `json:"redes"`

	// Usuários
	UsuarioAtual    string   `json:"usuario_atual"`     // usuário logado no console agora
	UsuariosSessao  []string `json:"usuarios_sessao"`   // usuários com sessão interativa (inclui RDP)
	UltimoLogon     string   `json:"ultimo_logon"`      // último usuário que fez logon (registro LogonUI)
	UltimoLogonNome string   `json:"ultimo_logon_nome"` // nome de exibição do último logon
	Perfis          []Perfil `json:"perfis"`

	// MicroSIP (softphone): contas SIP de cada perfil do Windows e o histórico de chamadas.
	// Nunca leva a senha SIP.
	MicroSIP []MicroSIP `json:"microsip,omitempty"`

	// AnyDesk (1.14+): IDs encontrados nos system.conf e se o AnyDesk está instalado (serviço).
	AnyDesk           []AnyDesk `json:"anydesk,omitempty"`
	AnyDeskInstalado  bool      `json:"anydesk_instalado,omitempty"`
	AnyDeskVerificado bool      `json:"anydesk_verificado,omitempty"` // o agente sabe procurar (1.14+)

	// Programa monitorado (1.15+, ex.: Work Monitor): onde está instalado e se está rodando.
	Monitor *SituacaoMonitor `json:"monitor,omitempty"`

	// Último erro do auto-update (1.11+), para o servidor mostrar o motivo.
	AutoUpdateErro string `json:"auto_update_erro,omitempty"`
}

// MicroSIP é a instalação do MicroSIP de um perfil do Windows (ou portátil).
type MicroSIP struct {
	Perfil   string       `json:"perfil"`  // pasta do perfil do Windows (ex.: daniel.costa); vazio = portátil
	Caminho  string       `json:"caminho"` // pasta onde está o microsip.ini
	Versao   string       `json:"versao"`
	Contas   []ContaSIP   `json:"contas"`
	Chamadas []ChamadaSIP `json:"chamadas,omitempty"`
	Erro     string       `json:"erro,omitempty"`
}

type ContaSIP struct {
	Secao    string `json:"secao"`    // Account1, Account2...
	Rotulo   string `json:"rotulo"`   // label
	Usuario  string `json:"usuario"`  // username = número do ramal
	AuthID   string `json:"auth_id"`  // authID
	Nome     string `json:"nome"`     // displayName
	Servidor string `json:"servidor"` // server
	Dominio  string `json:"dominio"`  // domain
}

type ChamadaSIP struct {
	ID      string `json:"id"` // callId do MicroSIP
	Numero  string `json:"numero"`
	Nome    string `json:"nome"`
	Tipo    int    `json:"tipo"`    // 0 = feita, 1 = recebida, 2 = perdida (como o MicroSIP grava)
	Inicio  int64  `json:"inicio"`  // unix (segundos)
	Duracao int    `json:"duracao"` // segundos
	Info    string `json:"info,omitempty"`
}

type Memoria struct {
	Capacidade uint64 `json:"capacidade"`
	Velocidade int    `json:"velocidade"`
	Fabricante string `json:"fabricante"`
	PartNumber string `json:"part_number"`
	Slot       string `json:"slot"`
}

type Disco struct {
	Modelo     string `json:"modelo"`
	Serial     string `json:"serial"`
	Tamanho    uint64 `json:"tamanho"`
	Tipo       string `json:"tipo"`       // SSD / HDD
	Barramento string `json:"barramento"` // NVMe, SATA, USB...
}

type Programa struct {
	Nome       string `json:"nome"`
	Versao     string `json:"versao"`
	Fabricante string `json:"fabricante"`
}

type Volume struct {
	Letra   string `json:"letra"`
	Rotulo  string `json:"rotulo"`
	Sistema string `json:"sistema"`
	Tamanho uint64 `json:"tamanho"`
	Livre   uint64 `json:"livre"`
}

type Rede struct {
	Nome      string   `json:"nome"`
	MAC       string   `json:"mac"`
	IPv4      []string `json:"ipv4"`
	Conectada bool     `json:"conectada"`
	Fisica    bool     `json:"fisica"`
}

type Perfil struct {
	Usuario   string `json:"usuario"`
	SID       string `json:"sid"`
	Caminho   string `json:"caminho"`
	UltimoUso string `json:"ultimo_uso"`
}

// Resposta é o que o servidor devolve ao agente após receber um relatório.
type Resposta struct {
	OK           bool     `json:"ok"`
	MaquinaID    int64    `json:"maquina_id"`
	Nova         bool     `json:"nova"`
	MACDuplicado []string `json:"mac_duplicado"`
	// Migrar, quando preenchido, instrui o agente a passar a usar outro servidor.
	Migrar *Migracao `json:"migrar,omitempty"`
	// Atualizar, quando preenchido, avisa que há uma versão mais nova do agente disponível
	// no servidor; o agente baixa e troca o próprio executável.
	Atualizar *Atualizacao `json:"atualizar,omitempty"`
	// Monitorar (1.15+): o que o agente deve procurar (programa monitorado). Vazio = desligado.
	Monitorar *AlvoMonitor `json:"monitorar,omitempty"`
	// NovoToken (1.11.2+): o agente usou um token anterior aceito; deve passar a usar este.
	NovoToken string `json:"novo_token,omitempty"`
}

type Atualizacao struct {
	Versao string `json:"versao"`
	URL    string `json:"url,omitempty"`
}

type Migracao struct {
	Servidor string `json:"servidor"`
	Token    string `json:"token"`
}

// ---------- eventos em tempo real (1.12, SSE em /api/v1/eventos) ----------

// EventoVersao ("versao"): a versão do agente que o servidor distribui.
type EventoVersao struct {
	Versao    string `json:"versao"`
	Atualizar bool   `json:"atualizar"` // auto-update ligado no servidor
}

// EventoTeste ("teste"): o painel pediu um teste de conexão.
type EventoTeste struct {
	ID        string `json:"id"`
	Versao    string `json:"versao"`
	Atualizar bool   `json:"atualizar"`
}

// RespostaTeste: o que o agente responde ao teste (POST /api/v1/eventos/resposta).
type RespostaTeste struct {
	TesteID       string `json:"teste_id"`
	AgentID       string `json:"agent_id"`
	Hostname      string `json:"hostname"`
	Versao        string `json:"versao"`
	PodeAtualizar bool   `json:"pode_atualizar"` // a troca do executável funcionaria
	JaAtual       bool   `json:"ja_atual"`       // já está na versão do servidor
	Motivo        string `json:"motivo"`
	Usuario       string `json:"usuario"` // conta do serviço (deve ser SYSTEM)
}

// AnyDesk: um system.conf encontrado na máquina.
type AnyDesk struct {
	ID      string `json:"id"`
	Alias   string `json:"alias,omitempty"`
	Tipo    string `json:"tipo"`             // "servico" (ProgramData) ou "portatil" (AppData do usuário)
	Perfil  string `json:"perfil,omitempty"` // usuário do Windows, no caso do portátil
	Caminho string `json:"caminho"`
}

// AlvoMonitor: programa que o painel quer acompanhar em cada máquina.
type AlvoMonitor struct {
	Nome           string   `json:"nome"`
	CaminhosPerfil []string `json:"caminhos_perfil,omitempty"` // relativos à pasta do usuário (AppData\Roaming\spa\work.exe)
	CaminhosFixos  []string `json:"caminhos_fixos,omitempty"`  // caminhos completos (C:\Program Files\...)
	Processos      []string `json:"processos,omitempty"`       // nomes do executável (work.exe)
}

type SituacaoMonitor struct {
	Nome        string              `json:"nome"`
	Instalacoes []InstalacaoMonitor `json:"instalacoes,omitempty"`
	Rodando     []ProcessoMonitor   `json:"rodando,omitempty"`
}

type InstalacaoMonitor struct {
	Perfil     string `json:"perfil,omitempty"`
	Caminho    string `json:"caminho"`
	Tamanho    int64  `json:"tamanho"`
	Modificado string `json:"modificado"`
}

type ProcessoMonitor struct {
	Nome    string `json:"nome"`
	Caminho string `json:"caminho,omitempty"`
	PID     uint32 `json:"pid"`
}
