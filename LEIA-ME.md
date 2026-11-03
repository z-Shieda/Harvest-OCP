# Inventário de máquinas

Sistema interno de inventário para computadores Windows, em duas partes:

| Arquivo | Onde roda | O que faz |
|---|---|---|
| `bin\inventario-servidor.exe` | 1 máquina/servidor da rede | Recebe os inventários, guarda em banco SQLite (`inventario.db`) e oferece o painel web com login |
| `bin\inventario-agente.exe` | Cada estação | Coleta a configuração da máquina e os usuários e envia ao servidor (a cada 4 h, a cada logon e na inicialização) |

Os dois são executáveis únicos: não precisam de instalação de runtime, banco de dados externo ou IIS.

## O que é coletado

- **Máquina:** nome, domínio, fabricante, modelo, número de série, UUID de hardware, BIOS
- **Sistema:** Windows (edição, versão 22H2/24H2, build), data de instalação, último boot
- **Hardware:** processador (núcleos/threads), RAM total e por pente, discos (SSD/HDD/NVMe, serial), volumes com espaço livre, placa de vídeo
- **Rede:** adaptadores, MAC address, IPv4, se é físico ou virtual
- **Usuários:** usuário conectado no momento, sessões ativas (inclusive RDP), **último usuário que fez logon** e perfis existentes na máquina com a data do último uso

## 1. Instalar o servidor

1. Copie `inventario-servidor.exe` para uma pasta fixa, por exemplo `C:\Inventario\`.
2. Abra o **Prompt de Comando como administrador** nessa pasta e rode:
   ```
   inventario-servidor.exe -instalar
   ```
   Isso faz o servidor iniciar junto com o Windows (tarefa agendada como SYSTEM, reinício automático em caso de falha) e libera a porta 8080 no firewall.
   Para testar sem instalar: `inventario-servidor.exe` (fica rodando no console).
3. Na primeira execução é criado o usuário **admin** com uma senha aleatória, gravada em `C:\Inventario\senha-admin-inicial.txt`. Entre, troque a senha em **Administração** e apague o arquivo.
4. Acesse `http://IP-DO-SERVIDOR:8080`.

Opções: `-porta 9000`, `-banco D:\dados\inventario.db`, `-cert cert.pem -chave chave.pem` (HTTPS), `-redefinir-senha admin` (gera senha nova se você esquecer), `-desinstalar`.

## 2. Instalar o agente nas máquinas

Pegue o comando pronto em **Administração → Instalação do agente** e execute em cada estação, como administrador:

```
inventario-agente.exe -instalar -servidor http://192.168.0.10:8080 -token SEU_TOKEN
```

O agente é copiado para `C:\Program Files\Inventario\`, a configuração fica em `C:\ProgramData\Inventario\` e é criada a tarefa agendada `\Inventario\Agente` (roda como SYSTEM, também na bateria). Uma mensagem confirma o primeiro envio.

### Instalação só com duplo clique (recomendado para muitas máquinas)

Compile o agente com o endereço e o token embutidos:

```
.\compilar.ps1 -Servidor http://192.168.0.10:8080 -Token SEU_TOKEN
```

Depois basta clicar com o botão direito no `inventario-agente.exe` → **Executar como administrador**. Sem permissão de administrador, ele apenas envia o inventário uma vez.
Para distribuir via **GPO**, use um script de inicialização do computador apontando para o executável em um compartilhamento, com o parâmetro `-instalar -silencioso`.

### Outros comandos do agente

| Comando | Função |
|---|---|
| `inventario-agente.exe` | Envia o inventário agora e mostra o resultado |
| `inventario-agente.exe -relatorio` | **Inventário local**: gera e abre um relatório HTML da máquina, sem precisar do servidor |
| `inventario-agente.exe -json > maquina.json` | Exporta o inventário em JSON |
| `inventario-agente.exe -desinstalar` | Remove a tarefa e os arquivos (como administrador) |

O último inventário coletado também fica salvo em `C:\ProgramData\Inventario\ultimo_inventario.json` e o log em `agente.log`, na mesma pasta.

## 3. Usando o painel

- **Painel:** totais, máquinas sem contato há mais de 7 dias, alertas de duplicidade, distribuição de versões do Windows.
- **Máquinas:** busca por nome, usuário, IP, MAC, serial, patrimônio, setor ou modelo. Na ficha da máquina você vê toda a configuração, o último usuário, o histórico de logins e pode preencher **patrimônio, setor, responsável, localização e observações**.
- **Usuários:** todos os logins detectados, com as máquinas que cada um usou. É possível **editar** (login, nome, e-mail, setor), **excluir** e **mesclar** duplicados (ex.: `EMPRESA\joao` e `joao`). O login mesclado vira um *apelido*, então os próximos envios com o nome antigo já caem no cadastro certo.
- **Duplicados:**
  - **MAC address duplicado:** quando duas máquinas cadastradas informam o mesmo MAC físico, aparece uma faixa vermelha em todas as páginas e o grupo é listado aqui. Clique em **Manter este** no registro correto para mesclar os outros nele, ou **Excluir**. O registro mantido sempre fica ligado ao agente que comunicou por último.
  - Se o MAC pertence a uma dock ou adaptador USB usado por várias máquinas, clique em **Ignorar este MAC**.
  - Também lista máquinas com o mesmo nome e possíveis usuários duplicados.
- **Exportar:** planilha Excel `.xlsx` com as abas Máquinas, Usuários, Logins, Rede, Discos, Volumes, Memória, Perfis e MAC duplicado; ou CSV (separador `;`, abre direto no Excel em português).
- **Administração:** token dos agentes, administradores do painel, troca de senha, **aviso por webhook** (Teams, Google Chat, Slack ou Discord recebem uma mensagem quando surge um MAC duplicado), backup do banco e registro de todas as ações feitas no painel.

### Por que aparecem duplicados?

Cada instalação do agente gera um identificador próprio. Se o Windows for reinstalado, ou se uma imagem de disco com o agente for clonada para outra máquina, nasce um novo registro — e o MAC repetido denuncia isso. O agente também detecta clonagem pelo UUID de hardware e gera um novo identificador automaticamente nesse caso.

## Backup

Em **Administração → Baixar backup do banco** (pode ser feito com o sistema em uso). Para restaurar, pare o servidor (`schtasks /End /TN \Inventario\Servidor`), substitua o `inventario.db` e inicie de novo (`schtasks /Run /TN \Inventario\Servidor`).

## Segurança

- O painel exige login; senhas guardadas com PBKDF2-SHA256; bloqueio após 5 tentativas erradas em 10 minutos; proteção CSRF nos formulários.
- Os agentes só conseguem enviar dados com o token. Se o token vazar, gere outro em Administração e reinstale os agentes.
- Na rede interna o HTTP costuma ser suficiente. Para HTTPS, use `-cert` e `-chave` com um certificado que as estações confiem.

## Compilar a partir do código-fonte

Requer [Go 1.24 ou superior](https://go.dev/dl/).

```
.\compilar.ps1                      (Windows)
./compilar.sh                       (Linux/macOS — também gera o servidor para Linux)
```

Estrutura:

```
cmd/agente/          agente (coleta.ps1 é o script de coleta embutido no .exe)
cmd/servidor/        executável do servidor
internal/modelo/     formato do relatório enviado pelo agente
internal/servidor/   banco SQLite, painel web, exportação .xlsx/.csv
internal/servidor/web/  páginas HTML e CSS (embutidos no .exe)
```

### Servidor em Linux (opcional)

`bin/inventario-servidor-linux` funciona igual. Exemplo de serviço systemd (`/etc/systemd/system/inventario.service`):

```
[Unit]
Description=Inventario
After=network.target

[Service]
ExecStart=/opt/inventario/inventario-servidor-linux -porta 8080 -banco /opt/inventario/inventario.db
Restart=always

[Install]
WantedBy=multi-user.target
```
