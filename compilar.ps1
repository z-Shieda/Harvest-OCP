# Compila o agente e o servidor para Windows (64 bits). Requer Go 1.24+: https://go.dev/dl/
#   .\compilar.ps1
# Servidor e token do agente avulso (deixe em branco para um agente sem dados embutidos):
param(
    [string]$Servidor = "",   # ex.: http://192.168.0.10:8080
    [string]$Token    = ""    # token exibido no painel (Administracao)
)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw "Go nao encontrado. Instale em https://go.dev/dl/ e abra um novo PowerShell." }
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
New-Item -ItemType Directory -Force -Path bin, internal\servidor\payload | Out-Null

# 1) Agente generico embutido no servidor (sem token; o servidor gera o config.json na hora).
go build -trimpath -ldflags "-s -w -H=windowsgui" -o internal\servidor\payload\inventario-agente.exe .\cmd\agente
if ($LASTEXITCODE -ne 0) { throw "Falha ao compilar o agente embutido." }

# 2) Servidor (embute o agente do passo 1).
go build -trimpath -ldflags "-s -w" -o bin\inventario-servidor.exe .\cmd\servidor
if ($LASTEXITCODE -ne 0) { throw "Falha ao compilar o servidor." }

# 3) Agente avulso para instalar direto numa maquina.
$ld = "-s -w -H=windowsgui"
if ($Servidor) { $ld += " -X main.servidorPadrao=$($Servidor.TrimEnd('/'))" }
if ($Token)    { $ld += " -X main.tokenPadrao=$Token" }
go build -trimpath -ldflags $ld -o bin\inventario-agente.exe .\cmd\agente
if ($LASTEXITCODE -ne 0) { throw "Falha ao compilar o agente." }

Write-Host "Pronto: bin\inventario-servidor.exe e bin\inventario-agente.exe"
