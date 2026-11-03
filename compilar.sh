#!/bin/sh
# Compila no Linux/macOS: agente e servidor para Windows + servidor para Linux.
#   ./compilar.sh [http://servidor:8080] [TOKEN]
set -e
cd "$(dirname "$0")"
mkdir -p bin internal/servidor/payload
export CGO_ENABLED=0

# 1) Agente GENÉRICO (sem token) embutido no servidor: o servidor entrega esse e gera o
#    config.json com o token atual na hora do download.
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H=windowsgui" -o internal/servidor/payload/inventario-agente.exe ./cmd/agente

# 2) Servidor (embute o agente do passo 1). A versão do agente embutido é lida de
#    cmd/agente/main.go e gravada no servidor (o auto-update compara com ela, não com a
#    versão do servidor — assim uma versão só de servidor não dispara atualização nas máquinas).
VAG=$(sed -n 's/^[[:space:]]*versao[[:space:]]*= "\([0-9.]*\)".*/\1/p' cmd/agente/main.go | head -1)
[ -n "$VAG" ] || { echo "não achei a versão do agente em cmd/agente/main.go"; exit 1; }
LDS="-s -w -X inventario/internal/servidor.VersaoAgente=$VAG"
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDS" -o bin/inventario-servidor.exe ./cmd/servidor
GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "$LDS" -o bin/inventario-servidor-linux ./cmd/servidor
echo "Agente embutido: v$VAG"

# 3) Agente avulso para instalar direto numa máquina (com servidor/token embutidos, se informados).
LD="-s -w -H=windowsgui"
[ -n "$1" ] && LD="$LD -X main.servidorPadrao=$1"
[ -n "$2" ] && LD="$LD -X main.tokenPadrao=$2"
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LD" -o bin/inventario-agente.exe ./cmd/agente

echo "Pronto: arquivos em bin/"
