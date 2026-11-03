# CONTEXTO DO PROJETO — leia isto primeiro

> Documento de retomada. Em um chat novo, aponte este arquivo (e o `PATCH-NOTES.md`) para o
> assistente recuperar todo o contexto. Caminho na máquina do Daniel:
> `C:\Users\ti.databit\Códigos\inventario\`

## O que é

**Inventário de máquinas** — sistema interno da **Ocupacional** (responsável: Daniel, equipe de
TI) para inventariar computadores **Windows**. Substitui o módulo de inventário do GLPI, que
estava pouco configurado. Feito do zero, em Go.

Duas partes, ambas executável único (sem runtime/dependência externa):
- **Servidor** (`inventario-servidor.exe`): recebe os relatórios, guarda em **SQLite**
  (`inventario.db`), oferece **painel web** com login. Roda numa máquina da rede.
- **Agente** (`inventario-agente.exe`): roda em cada estação como **tarefa agendada (SYSTEM)**,
  por máquina (não por usuário); coleta **a cada 1 h** (desde a 1.6; antes 4 h), no logon e na
  inicialização. A partir da 1.6 ele também **se auto-atualiza** quando o servidor avisa que há
  versão nova.

> **VERSÃO ATUAL: servidor 1.18.0 / agente 1.17.0.** Pontos em aberto estão na seção "ONDE PARAMOS" logo abaixo.

## ONDE PARAMOS (30/10/2026, sessão na máquina de teste) — ler primeiro

- **Nome do projeto: HARVEST** (dado pelo Daniel; repo Harvest-OCP). 1.18: assinatura "Desenvolvido por
  Daniel Costa · T.I Ocupacional" (https://www.linkedin.com/in/ccostadaniel/) no login e rodapé;
  easter egg Konami (`static/harvest.js`, partículas formando HARVEST — arte original, sem o
  personagem do JoJo, que é protegido).

- **1.17.0 — nuvem:** `migracao_nuvem.go` (importar banco → `inventario-importar.db` aplicado no início
  por `AplicarImportacaoPendente`, `AjustarAposImportacao`; `reiniciarServidor` por SO; SSE `migrar`;
  redirect das páginas públicas; faixa no layout). Daniel vai subir para a nuvem e para o GitHub
  (repo sem binários/banco/token — pacote em v17/github). Guia: MIGRAR-PARA-NUVEM.md.

- **1.16.0 — Lixeira:** `lixeira.go` (tabela `lixeira_maquinas` com cópia JSON da máquina +
  interfaces/logins/ramais/ids de chamados; `NaLixeira(agent_id)` ignora envios), ramais com
  `excluido_em` (`ramais.go`), duplicado = mesmo número ativo em >1 lugar. Usuário local = `contaLocal`
  (sem domínio, `.\x` ou domínio = hostname do inventário) ou marcado; config `ocultar_usuarios_locais`.

- **1.14.0 — AnyDesk:** agente `cmd/agente/anydesk*.go` (system.conf: ad.anynet.id/alias; serviço no
  registro). Servidor `anydesk.go` (colunas `maquinas.anydesk_*`, `chamados.anydesk_id`; situação
  ok/portatil/sem_id/sem/""), filtro `sem_anydesk`, alerta `alerta_anydesk` (padrão ligado).
  Formato do system.conf conferido pela documentação, não num arquivo real do Daniel (PC não estava
  acessível) — confirmar numa máquina real.
- **1.15.0 — Work Monitor RESOLVIDO:** é `%AppData%\spa\work.exe` (processo `work.exe`, sem serviço,
  sem ProductName; assinado SMART Computadores e Serviços). `monitor.go` (servidor, config
  `monitor_*`, colunas `maquinas.monitor_*`) + `cmd/agente/monitor.go`/`processos_windows.go`.
  `Resposta.Monitorar` leva o alvo; vazio = desligado.

- **PADRÃO DE ENTREGA (pedido do Daniel):** TODA versão nova sai com o kit
  **Inventario-vX-INSTALAR-SERVIDOR.zip**: `INSTALAR-SERVIDOR.bat` na raiz + pasta `arquivos\` com
  `Instalar-Servidor.ps1` e `Inventario_servidor_vX.exe` (pergunta IP/porta/pasta, manter/zerar dados,
  backup, volta atrás se falhar, registro na Área de Trabalho). Modelo em pkg113/.

- **1.13.0 — Configurações** (`configuracoes.go`, `/configuracoes`): tema e menu por conta (colunas
  `admins.tema`/`admins.menu`; layout usa `html[data-tema=claro]` e `body.menu-lateral`); setores fixos
  (config `setores_lista`, define `campo_setor` no layout, `setoresFixos` no FuncMap). No CSS, cor de
  texto de destaque é `--pri-t` (no claro é verde escuro); `--pri` é para preenchimento.
- Pendente sugerido: 1.14 com assinatura ed25519 das atualizações do agente + 2FA (TOTP) no painel.

- **1.12.0 — tempo real (SSE):** agente vira também serviço `InventarioAgente` (SYSTEM,
  `cmd/agente/servico_windows.go` com x/sys/windows/svc/mgr; `eventos.go` = cliente SSE). Servidor
  `eventos.go` (hub, `/api/v1/eventos`, `/api/v1/eventos/resposta`, Admin → Conexões com teste).
  Serviço atualizado sai com os.Exit(3) → ação de recuperação do Windows reinicia com o .exe novo.
  Testado aqui só em Linux (SSE, teste, reconexão); a parte de serviço do Windows ainda não foi
  testada numa máquina real — conferir na máquina de teste.

- **1.11.2 — CAUSA do auto-update:** máquinas 1.7 recebem 401 (token antigo; servidor reinstalado
  ganhou token novo — lembrar que `-token` com `-instalar` não é gravado). Solução: `tokens.go`
  (config `tokens_aceitos`, tabela `tokens_recusados`, botão Aceitar no Admin, `Resposta.NovoToken`
  para agentes 1.11.2+). Próximo passo do Daniel: atualizar servidor e clicar Aceitar.

- **1.11.1:** Daniel decidiu **ramais SEM histórico** (agente ~8 MB). Auto-update ainda não funciona nas
  máquinas 1.7 da empresa (servidor oferece certo — testado). Criado diagnóstico por máquina
  (`autoupdate_diag.go`, colunas `au_*` em maquinas) + kit DIAGNOSTICO/REPARAR-AGENTE. Aguardando o
  diagnóstico de uma máquina presa. PC de casa do Daniel ("pc") tem agente 1.10 instalado
  apontando para http://192.168.18.3:8081 e funcionando.

- **1.11.0 (servidor + agente):** aba Ramais. Agente `cmd/agente/microsip.go` lê MicroSIP.ini (UTF-16)
  e call_log.db (SQLite via ncruces no agente) de cada perfil; servidor `ramais.go` (tabelas `ramais`,
  `ramal_ajustes`, `ramal_chamadas`). Senha SIP nunca coletada. A confirmar com o Daniel: mapeamento
  de `type` do call_log (assumido 0 feita / 1 recebida / 2 perdida) numa máquina com histórico real.
  Cuidado: o banco usa 1 conexão (SetMaxOpenConns(1)) — não fazer consulta dentro de um rows.Next().

- **1.10.1 (só servidor):** e-mail dos chamados agora registra cada envio (tabela `email_envios`,
  "Últimos envios" em Administração, evento "email" na linha do tempo) e explica o erro SMTP
  (`explicarErroSMTP` em email.go). Daniel relatou que o e-mail não chegava — aguardando ele ver o
  motivo exato na tabela (suspeita: SMTP AUTH desligado no M365 / MFA).

- **1.10.0 entregue (servidor + agente):** agente 1.10 só para TESTAR o auto-update (1.7 → 1.10);
  aviso de chamado novo no navegador (`static/avisos.js` + `/api/chamados/novidades`); e-mail por
  SMTP (`email.go`: responsável, equipe, responsável automático); chaves liga/desliga em
  Notificações (`LimitesAlertas`). **Aguardando o Daniel confirmar se as máquinas foram para a
  1.10 sozinhas** (Inventário → Versões do agente / agente.log).
- **PENDENTE — Work Monitor:** recurso "programa monitorado" escondido das telas
  (`MonitoramentoProgramaAtivo = false` em servidor.go) porque a detecção não estava confiável.
  Investigar a coleta (nome exato do programa no registro, 32/64 bits, instalação por usuário em
  HKCU — o agente roda como SYSTEM e não vê HKCU) e religar numa versão futura.
- **Notificação do Windows** (fora do navegador) só com https — depende do certificado.

- **1.9.0 entregue (só servidor): novo visual "Bento lima"** com a marca (logo, lima #ADDA23,
  petróleo #18424C, Montserrat/Inter embutidas) e a página **Início** (inicio.go/inicio.html).
  Escolhido entre 3 propostas no quadro de design "Ocupacional TI – 3 ideias de interface"
  (Ideia 3, sem o degradê). Tema em `web/static/estilo.css` (variáveis em `:root`).

- **1.8.0 entregue (só servidor): chamados Beta** — página sem login /abrir-chamado com vínculo
  automático da máquina por IP, link de acompanhamento, linha do tempo, nota interna, "Assumir",
  avisos no webhook, SLA por prioridade, relatórios, CSV e anexos (no banco). Ver PATCH-NOTES 1.8.0.
  O Daniel ainda vai testar na máquina de teste antes de aplicar em produção.
- **Versão do agente separada da do servidor**: `servidor.VersaoAgente` (gravada pelos scripts de
  compilação a partir de `cmd/agente/main.go`). O auto-update e o "desatualizado" comparam com ela.
  Ao lançar um agente novo, basta subir `versao` em `cmd/agente/main.go` e recompilar.
- **Instalador de teste** entregue (pacote com .bat que detecta o IP, pré-grava token, instala
  servidor + agente). O item 2 abaixo está resolvido.
- Continua em aberto: Bitdefender (item 1 abaixo).

### Pendências anteriores (31/10/2026)

Dois assuntos ficaram em aberto no fim da última sessão:

1. **Bitdefender GravityZone voltou a acusar o agente 1.7 como vírus ("toda hora").**
   - Causa provável: (a) a 1.6 baixou o intervalo de 4 h → **1 h**, então a coleta por PowerShell
     (SYSTEM) roda 4× mais e o motor de **comportamento (ATC / Advanced Threat Control)** dispara
     muito mais; (b) o **auto-update** baixa um `.exe` e troca o executável (comportamento de
     "dropper"); (c) a nova leitura de programas no registro soma ao padrão de "recon".
   - **O que resolve sem custo:** no GravityZone, criar **exclusões** cobrindo o módulo
     **Advanced Threat Control** (não só "arquivo/On-Access" — foi isso que faltou antes e por isso
     "voltava a remover"). Exclusões: pasta `C:\Program Files\Inventario\`, pasta
     `C:\ProgramData\Inventario\` e processo `C:\Program Files\Inventario\inventario-agente.exe`,
     com **todos os módulos marcados** (ATC/HyperDetect/Sandbox inclusive). Se já quarentenou:
     Quarentena → Restaurar + Adicionar às exclusões.
   - **Correção definitiva:** assinatura de código (Authenticode) — certificado pago, decisão da
     empresa. Integrar `signtool` no build quando houver certificado.
   - **PERGUNTA EM ABERTO (o Daniel não respondeu):** se quer que eu (1) volte o intervalo para
     ~3 h e/ou (2) deixe o **auto-update desligado por padrão** por ora, para reduzir os disparos,
     e (3) se a empresa vai providenciar o certificado. Retomar por aqui. (Obs.: já existe o
     toggle "Auto-update dos agentes" em Administração e a config `auto_update`.)

2. **Instalador de teste para uma máquina auxiliar (ficou no meio).**
   - O Daniel está numa **máquina de teste** e pediu "o instalador da versão mais recente, que leia
     o IP da minha máquina e já instale nessa máquina de teste".
   - **Limitação descoberta:** a sessão (bridge) continua ligada ao **info-pc-2** (máquina
     principal), **não** à máquina de teste — então **não dá para instalar remotamente** nela.
     Tem que **enviar um arquivo** (SendUserFile) para o Daniel rodar na máquina de teste.
   - **Detalhe técnico do token (importante):** no servidor, a flag `-token` **só grava** o token
     quando ele roda em modo normal; o `-instalar` sai antes (a tarefa roda sem `-token`). Então,
     para um teste self-contained, é preciso **pré-gravar** o token no banco (rodar o servidor uma
     vez com `-token X` para gravar em `config.token_agente`, encerrar, e aí `-instalar`), ou
     apontar o agente para o servidor de produção. `cmd/servidor/sistema_windows.go` monta a tarefa
     com `-porta %d -banco "%s"`.
   - **Plano sugerido:** gerar um **pacote de teste .zip** (servidor 1.7 + agente 1.7 + um `.bat`
     que detecta o IPv4 local via `ipconfig`, pré-grava um token fixo de teste, instala o servidor
     e instala o agente apontando para `http://<IP-local>:8080`, e abre o painel). Entregar por
     SendUserFile. Alternativa mais simples: mandar o **agente configurado** apontando para o
     servidor de produção `10.100.21.114:8080` (já sei gerar — ldflags com servidor/token).

## Ambiente de produção atual (confirmar sempre, pode ter mudado)

- Servidor rodando na máquina do Daniel, endereço **http://10.100.21.114:8080**.
- Pasta do servidor: `C:\Users\ti.databit\Códigos\inventario\bin` (tarefa agendada
  `\Inventario\Servidor`, como SYSTEM). O banco `inventario.db` fica nessa pasta.
- **Token dos agentes:** `SEU_TOKEN_AQUI` (sensível; aparece no painel
  em Administração). Está embutido no agente configurado e no `config.json`.
- Login do painel: usuário `admin` (senha trocada pelo Daniel; a inicial ficava em
  `senha-admin-inicial.txt`).
- Agente: Windows apenas. Instalação por máquina em `C:\Program Files\Inventario`; dados em
  `C:\ProgramData\Inventario` (pastas com ACL restrita a SYSTEM/Administradores).

## O que coleta

Hardware (CPU, RAM por pente, discos SSD/HDD/NVMe, espaço livre, vídeo, serial), sistema
(Windows, versão, build, instalação, boot), rede (adaptadores, MAC, IPv4) e usuários (logado
agora, último logon, histórico por máquina, perfis).

## Recursos já prontos (até a v1.7)

- Detecção de **MAC duplicado** (máquina reinstalada/clonada): alerta no painel + webhook
  (Teams/Slack/Discord). Resolver em Duplicados (mesclar/excluir/ignorar).
- Gestão de **máquinas** e **usuários**: editar, excluir, **mesclar duplicados** (ex.:
  `EMPRESA\joao` e `joao`; o login antigo vira apelido).
- Cadastro interno por máquina: patrimônio, setor, responsável, localização.
- **Exportação**: Excel (.xlsx, 9 abas) e CSV.
- **Painel com gráficos**: status (ativas ≤7d / ociosas 7–14d / inativas +14d), sistemas
  operacionais, máquinas por setor, **versões do agente**.
- **Filtros** na lista de máquinas: rápidos (MAC duplicado, nome genérico, agente desatualizado)
  e **personalizados salvos** (campo + operador + valor).
- **Notificações** (menu): nomes genéricos (DESKTOP-xxxx, WIN-xxxx + padrões próprios), máquinas
  inativas há +N dias, disco com -X% livre. Lista ao vivo.
- **Download do instalador** pelo painel (.exe, .zip, só config.json) + **endereço fixo** do
  servidor configurável (corrige o problema de sair "localhost").
- **Atualização do servidor pelo painel** (envia o .exe novo, troca e reinicia sozinho; guarda
  `inventario-servidor.antigo.exe`).
- **Migração de servidor** (para a nuvem): em 2 passos com **autorização por senha**; trava de
  segurança (o agente só migra após confirmar que o novo endereço responde); **fase canário**
  (migrar alguns hostnames primeiro).
- **Backup automático** diário (pasta `backups/`, retém 14 dias) + backup manual no painel.
- **Rotação de log**, endpoint **/saude**, **limite de concorrência**.
- **Agente menos detectável por antivírus** (sem PowerShell `-EncodedCommand`; tarefa via XML do
  schtasks) e **instalação limpa** (remove a versão anterior antes de instalar a nova).
- **Versões do agente visíveis**: coluna na lista + gráfico no painel + filtro "desatualizado".
- **Usuários locais**: marcar conta padrão do Windows (Administrador, Convidado etc.) para não
  contar como duplicado (selo "usuário local (ignorado)" na ficha; reversível).
- **Filtros por hardware**: processador (CPU), tem SSD (sim/não), e numéricos RAM (GB) / disco
  total (GB) / núcleos com operadores ≥ e ≤.
- **Relatório de nível das máquinas** (saúde do parque): rosca no painel (OK/Alerta/Crítico),
  coluna "Nível" na lista e cartão detalhado na ficha. Pontuação 0–10 por geração/linha da CPU,
  RAM e SSD; classes OK ≥6 / Alerta ≥4 / Crítico <4 ou RAM ≤4GB; **critérios ajustáveis** em
  Administração.

## Versões (ver PATCH-NOTES.md para o detalhe)

- 1.0 base · 1.1 segurança/robustez (ACL, backup, /saude, canário) · 1.2 painel/gráficos,
  filtros, notificações · 1.3 instalação limpa + nomes versionados · 1.4 endereço fixo do
  instalador + painel de versões do agente · 1.5 correção da versão exibida + usuários locais +
  mais filtros + relatório de nível das máquinas · 1.5.1 correção da leitura da
  geração do processador (Intel 10ª–14ª de 4 dígitos eram lidos como 1ª) + Ryzen passa a contar
  geração + comparação de versão do agente por série · 1.6.0 auto-update dos agentes
  (as máquinas se atualizam sozinhas ao contatar o servidor; endpoint `/api/v1/agente.exe` com
  token) + intervalo de contato 4 h → 1 h + tarefa se reconfigura sozinha. Obs.: auto-update só
  vale de agentes 1.6+ em diante; o 1º salto para a 1.6 ainda é manual/GPO · **1.7.0 (atual)**
  coleta de programas instalados + programa monitorado (Work Monitor) no painel/filtros; perfis de
  acesso (admin/técnico/usuário) com inventário restrito; sistema de chamados Alpha (categoria,
  prioridade, status, atribuição, comentários, vínculo com máquina); menu Chamados/Inventário;
  e-mail+provedor no usuário prontos para login Microsoft · **1.8.0 (atual, só servidor)** chamados
  Beta: abertura sem login, acompanhamento por link, histórico, nota interna, avisos, SLA,
  relatórios, anexos; versão do agente separada da do servidor · **1.9.0 (atual, só servidor)**
  novo visual com a marca e página Início · **1.10.0 (atual)** agente 1.10 (teste do
  auto-update), aviso sonoro de chamado novo, e-mail SMTP, chaves nas notificações, Work Monitor
  escondido (pendente).

> Obs.: a produção pode ainda estar numa versão anterior — o Daniel aplica as atualizações no
> ritmo dele (via `ATUALIZAR-para-vX.Y.bat` na pasta `bin`, ou pelo painel). Confirmar a versão
> real em `/saude` ou no rodapé do painel ao retomar.

## Perfis e chamados (v1.7)

- Tabela `admins` ganhou `papel` (admin|tecnico|usuario), `email`, `nome`, `provedor` (local|
  microsoft). Permissões por método em `Admin` (EhAdmin/PodeInventario/PodeGerenciarChamados).
  Rotas de inventário usam `pInv`, de administração `pAdm`, de chamados `p` (qualquer logado).
- Chamados: tabelas `chamados` e `chamado_comentarios`. Código em `internal/servidor/chamados.go`.
- **Login Microsoft (próximo):** criar provedor OAuth2/OIDC do Entra ID; no retorno, casar pelo
  `email` do `admins` (ou criar com papel padrão), setar `provedor='microsoft'`. O resto (perfis,
  sessão, permissões) já está pronto. O login local continua como alternativa/fallback.
- Binários de cada versão em `versoes/<n>/` (para downgrade). Arquivos nomeados
  `Inventario_servidor_vX.Y.exe` e `Inventario_agente_vX.Y.exe`.

## Como compilar / atualizar

- Compilar (precisa Go 1.24+): `./compilar.sh http://IP:8080 TOKEN` (Linux) ou
  `.\compilar.ps1` (Windows; servidor/token nos parâmetros). Gera `bin/`.
- Aplicar servidor novo: menu Administração → Atualizar o servidor (ou trocar o .exe manualmente
  parando a tarefa `\Inventario\Servidor`). Banco é preservado.
- Aplicar agente novo nas estações: rodar `Inventario_agente_vX.Y.exe` como admin (remove o
  antigo e instala o novo).

## GitHub

- Repositório privado: **github.com/z-Shieda/Harvest-OCP** (conta z-Shieda).
- IMPORTANTE: a sessão do assistente **não consegue dar push** (o ambiente só acessa repos
  pré-configurados e não cria/anexa repo). O Daniel sobe pelo git local. Repo local pronto em
  `C:\...\repo` (ou extrair `inventario-repo.zip`). Commits já feitos: v1.2, v1.3, v1.4, v1.5,
  v1.5.1, v1.6.0, v1.7.0 (o `git push` é o Daniel que faz).
  Fluxo: `git add -A && git commit -m "vX.Y" && git push`.
- Atribuição nos commits (lembrete da sessão): terminar a mensagem com
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` e a linha `Claude-Session: ...`.
- O que vai pro GitHub: código-fonte + binários **genéricos** (sem token) em `compilado/`.
  Token/IP foram removidos do código versionado (compilar.ps1 com defaults vazios).

## Roadmap / pendências

- **Resolver o Bitdefender na 1.7** (ver "ONDE PARAMOS") — exclusão de ATC e/ou ajustes de
  intervalo/auto-update, e decisão sobre certificado de assinatura. **Prioridade atual.**
- **Instalador de teste** para a máquina auxiliar (ver "ONDE PARAMOS").
- **Login Microsoft (Entra ID / OAuth2-OIDC):** próximo passo de autenticação combinado com o
  Daniel. A estrutura já está pronta (coluna `email` e `provedor` em `admins`, perfis, sessão).
  No retorno do OAuth, casar pelo `email` (criar se não existir, papel padrão "usuário"), setar
  `provedor='microsoft'`. Manter o login local como fallback.
- **Chamados — próximos passos (pós-1.8):** aviso por **e-mail** ao solicitante (precisa SMTP da
  empresa), SLA em **horário comercial** (hoje horas corridas) e pausa do prazo em "aguardando
  usuário", categorias configuráveis, pesquisa de satisfação, e exigir login (Microsoft/AD) na
  página /abrir-chamado quando o login estiver pronto. Código: `chamados.go`,
  `chamados_publico.go`, `chamados_relatorio.go`, `anexos.go`.
- **Segurança avançada (1.8+):** token por máquina com transição suave + revogação; 2FA no painel;
  auto-update e ordem de migração **assinados**; **criptografia do banco** com chave em arquivo
  protegido. (Decisões já tomadas: entrega em etapas; cripto com chave em arquivo; token por
  máquina com transição suave.)
- **Active Directory (alternativa/complemento ao Microsoft):** ver `PLANO-CHAMADOS.md` — login
  **LDAP** no AD (bind) + papéis por grupo; evitar SSO Kerberos.
- **HTTPS:** adiado — a empresa ainda não tem certificado. Hoje é HTTP na rede interna.
- **Migração para a nuvem:** recomendado usar um **nome de DNS** (ex.:
  `inventario.ocupacional...`) no campo de endereço desde já; aí mudar para a nuvem é só apontar o
  DNS, sem tocar nos agentes (mantendo o mesmo token). Alternativa: recurso de Migração do painel.
  Se o servidor for para a nuvem e precisar do AD, o servidor terá que enxergar o controlador de
  domínio (VPN/conector).

## Restrições conhecidas / decisões técnicas

- **SQLite roda em WebAssembly (wazero) no modo INTERPRETADOR** — foi o que estabilizou o servidor
  sob carga (o modo compilador derrubava). Não reverter.
- **Bitdefender GravityZone (ATC)** marca o agente como suspeito (comportamento tipo malware).
  Voltou a acusar na 1.7 "toda hora" (ver "ONDE PARAMOS" para causa e correção). A exclusão **tem
  que incluir o módulo Advanced Threat Control**, não só arquivo. Solução definitiva = **assinatura
  de código (Authenticode)**, certificado pago, a cargo da empresa.
- **Agente configurado (com servidor/token embutidos):** é gerado por entrega, só para o
  dispositivo do Daniel (nunca commitado — tem o token). Build:
  `GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H=windowsgui -X main.servidorPadrao=http://10.100.21.114:8080 -X main.tokenPadrao=<TOKEN>" -o Inventario_agente_vX.Y_CONFIGURADO.exe ./cmd/agente`.
  O genérico (sem token, para o painel/GitHub) é o build sem esses `-X`.
- **MSI:** não dá para gerar no ambiente do assistente (sem WiX). O pacote .zip inclui o código
  WiX (`wix/agente.wxs` + `gerar-msi.ps1`) para gerar o .msi numa máquina com WiX. Para GPO sem
  MSI, há `gpo-instalar.bat`.
- O assistente trabalha numa sessão em nuvem com acesso à pasta `C:\Users\ti.databit\Códigos` da
  máquina do Daniel (bridge de dispositivo). Não roda Windows real — mudanças específicas de
  Windows precisam ser testadas pelo Daniel em 1 máquina antes de espalhar.

## Como retomar em um chat novo

Diga algo como: "Leia `C:\Users\ti.databit\Códigos\inventario\CONTEXTO.md` e o `PATCH-NOTES.md`
para retomar o projeto de inventário." O assistente lê a pasta conectada e continua de onde
paramos.

**Próximos passos imediatos (na ordem):**
1. Resolver o **Bitdefender/GravityZone** na 1.7 (exclusão de ATC; decidir intervalo/auto-update;
   certificado de assinatura). — ver "ONDE PARAMOS", item 1.
2. Entregar o **instalador de teste** para a máquina auxiliar. — ver "ONDE PARAMOS", item 2.
3. Começar o **login Microsoft (Entra ID)**.

Ambiente da última sessão: bridge ligado ao **info-pc-2**; produção em `10.100.21.114:8080`;
token `SEU_TOKEN_AQUI`; repo local em `C:\...\repo` com v1.7.0 commitada
(sem push ainda).
