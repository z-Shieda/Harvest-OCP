# Inventário de máquinas

Sistema interno de inventário para computadores Windows, em duas partes:

| Componente | Onde roda | O que faz |
|---|---|---|
| **Servidor** (`inventario-servidor.exe`) | 1 máquina da rede | Recebe os inventários, guarda em banco SQLite (arquivo único) e oferece o painel web com login |
| **Agente** (`inventario-agente.exe`) | Cada estação | Coleta hardware, sistema, rede e usuários e envia ao servidor (a cada 4 h, no logon e na inicialização) |

Ambos são executáveis únicos em Go: não precisam de runtime, banco externo nem IIS.

## O que coleta

Configuração completa da máquina (CPU, RAM por pente, discos SSD/HDD/NVMe, espaço livre,
vídeo, versão do Windows, número de série, rede/MAC) e os usuários (quem está logado, o último
logon, histórico por máquina e perfis). Detecta **MAC duplicado** (máquina reinstalada/clonada),
gera planilha Excel/CSV, painel com gráficos, filtros personalizados e notificações.

## Pastas

```
cmd/agente/        código do agente (coleta.ps1 = script de coleta embutido)
cmd/servidor/      código do servidor
internal/modelo/   formato do relatório trocado entre agente e servidor
internal/servidor/ banco SQLite, painel web, gráficos, exportação, migração, atualização
compilado/         binários prontos (Windows + Linux) — genéricos, sem token
PATCH-NOTES.md     histórico de versões
PLANO-CHAMADOS.md  plano para evoluir para sistema de chamados + Active Directory
LEIA-ME.md         guia de instalação e uso
```

## Compilar a partir do código

Requer [Go 1.24+](https://go.dev/dl/).

```
./compilar.sh                 # Linux/macOS (gera Windows + Linux)
.\compilar.ps1                # Windows
```

Os binários saem em `bin/`. Opcionalmente, `compilar.ps1 -Servidor http://IP:8080 -Token XXXX`
embute o endereço e o token no agente (para instalação com duplo clique).

## Instalar (resumo)

1. **Servidor:** copie `inventario-servidor.exe` para uma pasta fixa e rode como administrador
   `inventario-servidor.exe -instalar`. A senha inicial do `admin` fica em
   `senha-admin-inicial.txt`. Acesse `http://IP-DO-SERVIDOR:8080`.
2. **Agente:** baixe o pacote pelo painel (Administração → Baixar o instalador) — já vem com o
   endereço e o token preenchidos — e rode `instalar.bat` como administrador em cada máquina.

O guia completo está em `LEIA-ME.md`.

## Observações de segurança

- Os binários em `compilado/` são **genéricos**: não contêm token nem endereço. O token é gerado
  pelo servidor e entregue no `config.json` ao baixar o agente pelo painel.
- Não versione `inventario.db`, logs, backups nem `config.json` (já estão no `.gitignore`).
- HTTPS ainda não está ativo (aguardando certificado); na rede interna o tráfego é HTTP.
