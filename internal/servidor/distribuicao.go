package servidor

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

// O agente é embutido no servidor na compilação (compilar.sh copia o .exe para payload/).
// Ele é genérico: não traz token embutido. O token e o endereço do servidor vão no
// config.json gerado na hora do download, sempre com os valores atuais deste servidor.
//
//go:embed payload/inventario-agente.exe
var agenteExe []byte

// baseURL devolve o endereço deste servidor como as estações o enxergam. Prioriza o endereço
// fixo configurado em Administração (para não cair em "localhost" quando o admin abre o painel
// na própria máquina do servidor); se não houver, usa o host da requisição.
func (s *Servidor) baseURL(r *http.Request) string {
	if fixo := strings.TrimRight(strings.TrimSpace(s.b.Config("endereco_servidor")), "/"); fixo != "" {
		return fixo
	}
	esquema := "http"
	if r.TLS != nil {
		esquema = "https"
	}
	return esquema + "://" + r.Host
}

func (s *Servidor) configJSON(r *http.Request) []byte {
	return []byte(fmt.Sprintf("{\n  \"servidor\": \"%s\",\n  \"token\": \"%s\"\n}\n", s.baseURL(r), s.b.Config("token_agente")))
}

func (s *Servidor) baixarAgenteExe(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="inventario-agente.exe"`)
	_, _ = w.Write(agenteExe)
}

func (s *Servidor) baixarAgenteConfig(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="config.json"`)
	_, _ = w.Write(s.configJSON(r))
}

// baixarAgenteZip entrega um pacote pronto para instalar nas estações: o executável, o
// config.json com o token atual, um instalador para uso manual, um script para implantação
// por GPO e o código WiX para quem quiser gerar um .msi.
func (s *Servidor) baixarAgenteZip(w http.ResponseWriter, r *http.Request, sess *Sessao) {
	base := s.baseURL(r)
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	add := func(nome string, dados []byte) {
		f, err := z.Create(nome)
		if err == nil {
			_, _ = f.Write(dados)
		}
	}
	add("inventario-agente.exe", agenteExe)
	add("config.json", s.configJSON(r))
	add("instalar.bat", []byte(batInstalar))
	add("gpo-instalar.bat", []byte(batGPO))
	add("wix/agente.wxs", []byte(montarWXS(s.marca().Nome, VersaoAgente)))
	add("wix/gerar-msi.ps1", []byte(ps1GerarMSI))
	add("LEIA.txt", []byte(strings.ReplaceAll(leiaAgente, "%URL%", base)))
	_ = z.Close()

	s.b.Auditar(sess.Admin.Usuario, "baixar instalador do agente", "zip")
	nome := "inventario-agente_" + time.Now().Format("2006-01-02") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+nome+`"`)
	_, _ = buf.WriteTo(w)
}

const batInstalar = "@echo off\r\n" +
	"net session >nul 2>&1\r\n" +
	"if %errorlevel% neq 0 (\r\n" +
	"  echo Clique com o botao direito neste arquivo e escolha \"Executar como administrador\".\r\n" +
	"  pause & exit /b 1\r\n" +
	")\r\n" +
	"\"%~dp0inventario-agente.exe\" -instalar\r\n"

const batGPO = "@echo off\r\n" +
	"rem === Script de inicializacao do computador (GPO) para instalar o agente ===\r\n" +
	"rem 1. Copie inventario-agente.exe e config.json para uma pasta compartilhada que as\r\n" +
	"rem    maquinas leiam, por exemplo \\\\SERVIDOR\\Inventario.\r\n" +
	"rem 2. Ajuste a linha ORIGEM abaixo para essa pasta.\r\n" +
	"rem 3. Em Diretiva de Grupo: Configuracao do Computador > Politicas > Configuracoes do\r\n" +
	"rem    Windows > Scripts > Inicializar, adicione este .bat.\r\n" +
	"set ORIGEM=\\\\SERVIDOR\\Inventario\r\n" +
	"if exist \"%ProgramFiles%\\Inventario\\inventario-agente.exe\" exit /b 0\r\n" +
	"copy /Y \"%ORIGEM%\\inventario-agente.exe\" \"%TEMP%\\inventario-agente.exe\" >nul\r\n" +
	"copy /Y \"%ORIGEM%\\config.json\" \"%TEMP%\\config.json\" >nul\r\n" +
	"\"%TEMP%\\inventario-agente.exe\" -instalar -silencioso\r\n"

// montarWXS: o .wxs com o nome da marca e a versão do agente embutido. Product Id="*" + versão
// real + MajorUpgrade = cada .msi novo substitui o anterior (antes era sempre 1.0.0.0 e o
// Windows ficava com dois produtos registrados).
func montarWXS(fabricante, versao string) string {
	if fabricante == "" {
		fabricante = "Harvest"
	}
	v := strings.TrimSpace(versao)
	if v == "" || v == "dev" {
		v = "1.0.0"
	}
	r := strings.NewReplacer("{{FABRICANTE}}", html.EscapeString(fabricante), "{{VERSAO}}", html.EscapeString(v))
	return r.Replace(wixAgente)
}

// Código WiX v3 (candle/light) para gerar um .msi de 64 bits. O UpgradeCode e o GUID do
// componente são fixos para que versões novas reconheçam e substituam as antigas.
const wixAgente = `<?xml version="1.0" encoding="utf-8"?>
<Wix xmlns="http://schemas.microsoft.com/wix/2006/wi">
  <Product Id="*" Name="{{FABRICANTE}} - Agente de inventario" Language="1046" Codepage="1252"
           Version="{{VERSAO}}" Manufacturer="{{FABRICANTE}}" UpgradeCode="7D4B9F2A-1C3E-4A5B-9E8D-2F6A1B4C7D90">
    <Package InstallerVersion="500" Compressed="yes" InstallScope="perMachine" Platform="x64"
             Description="Agente de inventario de maquinas" />
    <MajorUpgrade AllowSameVersionUpgrades="yes" DowngradeErrorMessage="Uma versao mais nova ja esta instalada." />
    <MediaTemplate EmbedCab="yes" />

    <Directory Id="TARGETDIR" Name="SourceDir">
      <Directory Id="ProgramFiles64Folder">
        <Directory Id="INSTALLDIR" Name="Inventario">
          <Component Id="CmpAgente" Guid="A1B2C3D4-E5F6-4789-9ABC-DEF012345678" Win64="yes">
            <File Id="FilAgente" Source="inventario-agente.exe" KeyPath="yes" />
            <File Id="FilConfig" Source="config.json" />
            <!-- para o servico em tempo real antes de trocar/remover o executavel -->
            <ServiceControl Id="PararServico" Name="InventarioAgente" Stop="both" Wait="yes" />
          </Component>
        </Directory>
      </Directory>
    </Directory>

    <Feature Id="Principal" Title="Agente" Level="1">
      <ComponentRef Id="CmpAgente" />
    </Feature>

    <!-- Apos copiar os arquivos, o proprio agente cria a tarefa agendada e o servico. -->
    <CustomAction Id="CaInstalar" FileKey="FilAgente" ExeCommand="-instalar -silencioso"
                  Execute="deferred" Impersonate="no" Return="ignore" />
    <!-- Desinstalacao de verdade (nao em atualizacao): remove tarefa, servico e dados. -->
    <CustomAction Id="CaDesinstalar" FileKey="FilAgente" ExeCommand="-desinstalar -silencioso"
                  Execute="deferred" Impersonate="no" Return="ignore" />

    <InstallExecuteSequence>
      <Custom Action="CaInstalar" After="InstallFiles">NOT REMOVE</Custom>
      <Custom Action="CaDesinstalar" Before="RemoveFiles">REMOVE="ALL" AND NOT UPGRADINGPRODUCTCODE</Custom>
    </InstallExecuteSequence>
  </Product>
</Wix>
`

const ps1GerarMSI = `# Gera o inventario-agente.msi (64 bits) a partir do agente.wxs.
# Requer o WiX Toolset v3 (gratuito, https://wixtoolset.org/ ; precisa do .NET 3.5).
# Rode na pasta "wix" do pacote (o .exe e o config.json ficam na pasta de cima).
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

function Achar($nome) {
    $c = Get-Command $nome -ErrorAction SilentlyContinue
    if ($c) { return $c.Source }
    # o instalador do WiX v3 nao poe no PATH: usa a variavel WIX ou a pasta padrao
    $locais = @()
    if ($env:WIX) { $locais += (Join-Path $env:WIX 'bin') }
    $locais += Get-ChildItem "${env:ProgramFiles(x86)}\WiX Toolset v3*\bin", "$env:ProgramFiles\WiX Toolset v3*\bin" -Directory -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending | ForEach-Object { $_.FullName }
    foreach ($d in $locais) { $f = Join-Path $d $nome; if (Test-Path $f) { return $f } }
    return $null
}

$candle = Achar 'candle.exe'; $light = Achar 'light.exe'
if (-not $candle -or -not $light) {
    Write-Host 'WiX Toolset v3 nao encontrado. Instale em https://wixtoolset.org/ (versao 3.14) e rode de novo.' -ForegroundColor Red
    exit 1
}
foreach ($a in 'inventario-agente.exe', 'config.json') {
    if (Test-Path "..\$a") { Copy-Item "..\$a" .\ -Force }
    if (-not (Test-Path ".\$a")) { Write-Host "Falta $a (descompacte o pacote inteiro)." -ForegroundColor Red; exit 1 }
}
Remove-Item .\agente.wixobj, .\inventario-agente.msi, .\inventario-agente.wixpdb -ErrorAction SilentlyContinue

& $candle -nologo -arch x64 agente.wxs
if ($LASTEXITCODE -ne 0) { Write-Host 'candle falhou.' -ForegroundColor Red; exit 1 }
& $light -nologo agente.wixobj -o inventario-agente.msi
if ($LASTEXITCODE -ne 0) { Write-Host 'light falhou.' -ForegroundColor Red; exit 1 }
Remove-Item .\agente.wixobj, .\inventario-agente.wixpdb -ErrorAction SilentlyContinue
Write-Host "Pronto: $PSScriptRoot\inventario-agente.msi" -ForegroundColor Green
Write-Host 'Instalar em silencio:  msiexec /i inventario-agente.msi /qn'
Write-Host 'Remover em silencio:   msiexec /x inventario-agente.msi /qn'
`

const leiaAgente = `AGENTE DE INVENTARIO - PACOTE DE INSTALACAO
Servidor: %URL%

Conteudo:
  inventario-agente.exe  - o agente
  config.json            - endereco do servidor e token (ja preenchidos)
  instalar.bat           - instalacao manual em uma maquina
  gpo-instalar.bat       - modelo para implantacao em massa por GPO
  wix\                   - codigo para gerar um instalador .msi

INSTALAR EM UMA MAQUINA
  Mantenha inventario-agente.exe e config.json na mesma pasta.
  Clique com o botao direito em instalar.bat e escolha "Executar como administrador".
  (Ou rode o inventario-agente.exe como administrador.)

IMPLANTAR EM VARIAS MAQUINAS (GPO)
  1. Copie inventario-agente.exe e config.json para uma pasta compartilhada da rede.
  2. Edite gpo-instalar.bat e ajuste a linha ORIGEM para essa pasta.
  3. Em Diretiva de Grupo, adicione o .bat como script de inicializacao do computador.

GERAR UM .MSI (opcional, para implantar o MSI por GPO/Intune)
  Precisa do WiX Toolset v3 (gratuito, versao 3.14, e do .NET Framework 3.5) no Windows.
  Entre na pasta wix e rode:  powershell -ExecutionPolicy Bypass -File gerar-msi.ps1
  Sera criado o inventario-agente.msi (64 bits, com a versao do agente).
  Instalar: msiexec /i inventario-agente.msi /qn    Remover: msiexec /x inventario-agente.msi /qn
  Um .msi de versao nova substitui o anterior sem desinstalar.

OBS.: se voce gerar um novo token no painel, baixe este pacote de novo: o config.json
velho deixa de funcionar.
`
