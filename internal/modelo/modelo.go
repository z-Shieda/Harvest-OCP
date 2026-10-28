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

	Memorias []Memoria `json:"memorias"`
	Discos   []Disco   `json:"discos"`
	Volumes  []Volume  `json:"volumes"`
	GPUs     []string  `json:"gpus"`
	Redes    []Rede    `json:"redes"`

	// Usuários
	UsuarioAtual    string   `json:"usuario_atual"`     // usuário logado no console agora
	UsuariosSessao  []string `json:"usuarios_sessao"`   // usuários com sessão interativa (inclui RDP)
	UltimoLogon     string   `json:"ultimo_logon"`      // último usuário que fez logon (registro LogonUI)
	UltimoLogonNome string   `json:"ultimo_logon_nome"` // nome de exibição do último logon
	Perfis          []Perfil `json:"perfis"`
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
}

type Migracao struct {
	Servidor string `json:"servidor"`
	Token    string `json:"token"`
}
