# Histórico de versões — Inventário

Os instaladores de cada versão ficam em `versoes/<versão>/`. Para voltar a uma versão
anterior (downgrade), pare o serviço, troque o `inventario-servidor.exe` pelo da pasta
desejada e inicie de novo. O banco de dados (`inventario.db`) é preservado entre versões.

> Procedimento de troca (Prompt de Comando como administrador, na pasta do servidor):
> ```
> schtasks /End /TN "\Inventario\Servidor"
> move /Y inventario-servidor.exe inventario-servidor.antigo.exe
> copy /Y "<pasta da versão>\inventario-servidor.exe" inventario-servidor.exe
> schtasks /Run /TN "\Inventario\Servidor"
> ```

---

## 1.22.0 — 04/11/2026

**Active Directory: comparativo AD × máquinas com o agente e usuários ativos.** Só o servidor muda (agente 1.21.0).

- Nova aba **Inventário → Active Directory**: estações ativas no AD (Windows de estação, conta habilitada, logon nos
  últimos 30 dias — ajustável), quantas **já têm o agente** e a **% de cobertura**, lista das que faltam (com último
  logon e OU), máquinas com agente fora do AD e **usuários ativos** (logon nos últimos 7 dias — ajustável).
- **Planilha .xlsx**: Resumo, Usuários ativos, Todos os usuários, Computadores do AD (com "tem o agente"), Sem o agente.
- Início: "Active Directory: X de Y com o agente (Z%)" no cartão Parque.
- **Sincronizar-AD.ps1** (baixado pelo painel já com endereço e token): roda no controlador de domínio, só LÊ o AD,
  pega o **último logon real** em cada DC (lastLogon não replica) e envia para `/api/v1/ad` com um **token próprio**
  (não é o token dos agentes). `-Agendar` cria a tarefa diária `\Harvest\Sincronizar-AD` (SYSTEM, 7h).
  `-Arquivo` gera um JSON para enviar pelo painel. O servidor não guarda senha do domínio.

---

## 1.21.0 — 04/11/2026

**Assinatura da T.I** nas atualizações e migrações + **recuperação com 24 palavras**. Servidor e agente 1.21.0.

- **Por quê:** até a 1.20 o agente (SYSTEM) instalava qualquer .exe que o servidor mandasse. Quem invadisse o
  servidor ou interceptasse o HTTP controlaria todas as máquinas. Agora só vale o que a T.I assinar no próprio PC.
- **harvest-chave.exe** (novo, roda só no PC da T.I): cria as chaves, assina cada kit, gera ordens de migração,
  recupera o acesso ao painel e troca a chave de assinatura.
- **Duas chaves (Ed25519):**
  - **chave raiz** = 24 palavras (padrão BIP39, lista oficial em português — o mesmo das carteiras de bitcoin).
    Nunca fica gravada; anote em papel.
  - **chave de assinatura** = arquivo `harvest-ti.chave` com senha (PBKDF2 600 mil voltas + AES-256-GCM), para o
    1Password. A raiz emite um certificado para ela.
- **Agente 1.21:** antes de trocar o executável confere raiz → certificado → assinatura → SHA-256 do arquivo, e
  exige versão **mais nova** que a instalada (não aceita downgrade). Migração de servidor só com **ordem assinada**
  (com validade). Recusas aparecem no log do agente e em Administração → Assinatura.
- **Revogação:** "Nova chave de assinatura" (com as 24 palavras) emite certificado com número maior; os agentes o
  recebem na resposta de cada envio e passam a recusar a chave antiga.
- **Esqueci minha senha** (`/recuperar`, link no login): o painel mostra um código de desafio, o harvest-chave
  responde com as 24 palavras (que nunca são digitadas no servidor) e a senha do administrador é redefinida.
  Desafio de uso único, 15 min, limite de tentativas, auditado.
- A chave raiz pública é gravada no kit pelo harvest-chave ("Assinar kit") — não precisa recompilar. Kit não
  assinado: o INSTALAR-SERVIDOR avisa e pede confirmação; o painel mostra o aviso; agentes funcionam como antes.
- Transição: agentes até 1.20 (e o MSI da GPO com a 1.19) não conferem assinatura — sobem para a 1.21 pelo
  auto-update normal e, a partir dela, só aceitam versões assinadas.

---

## 1.20.0 — 04/11/2026

**Ignorar máquinas.** Só o servidor muda (agente continua 1.19.0 — nada para atualizar nas estações).

- Na página da máquina, **Ignorar no inventário** com **motivo obrigatório** (sugestões: VM de testes, servidor,
  laboratório, equipamento de terceiros). Fica registrado quem ignorou e quando (e na auditoria).
- A máquina **continua no inventário recebendo os dados** do agente, mas **sai de**: contagem do parque, nível
  OK/Alerta/Crítico (Início e Painel), "Pedem atenção", notificações (sem Work Monitor, sem AnyDesk, disco cheio,
  inativa, nome genérico), "agentes em dia" e o número da aba Máquinas.
- Inventário › Máquinas: ignoradas somem da lista; filtro **Ignoradas (N)** mostra todas com o motivo; numa busca por
  texto elas aparecem com o selo "Ignorada". Aviso no topo da lista e "+ N ignoradas" no Início e no Painel.
- **Voltar a considerar** desfaz na hora. MAC/nome duplicado continuam sendo verificados (é questão de identidade).
- Exportações .xlsx/.csv: todas as máquinas, com a coluna nova **Ignorada (motivo)**.

---

## 1.19.0 — 03/11/2026

**Edição genérica (white-label)** e painel de **Marca**. Servidor e agente 1.19.0.

- **Duas edições do mesmo código** (escolhidas na compilação, `-X inventario/internal/servidor.Edicao=generica`):
  - `inventario-servidor.exe` — **Ocupacional**: logo, cores (petróleo #18424C + lima #ADDA23) e textos de sempre. Nada muda.
  - `harvest-servidor.exe` — **genérica (Harvest)**: logo própria do Harvest (símbolo dourado com três barras),
    cores neutras (dourado #E3B341 + grafite #1F2A37), sem menção à Ocupacional, sem Work Monitor pré-configurado,
    placeholders `@empresa.com.br`.
- **Configurações › Marca** (só administradores; vale para todos):
  - nome da empresa/sistema (título das abas, rodapé, e-mails, instalador MSI);
  - **cor principal** e **cor secundária** (seletor + hex) com **prévia ao vivo**;
  - upload de **logo para fundo escuro**, **logo para fundo claro** e **favicon** (PNG, SVG, JPG, WEBP, ICO; até 1 MB);
    se só uma logo for enviada, ela vale nos dois temas; "remover" por arquivo; **Voltar ao padrão**.
  - O servidor **calcula a paleta inteira** (fundos, bordas, textos, links) a partir das duas cores, nos temas
    escuro e claro, garantindo contraste mínimo de leitura (WCAG 4,5:1) — dá para escolher qualquer cor.
  - Funciona nas duas edições: a Ocupacional também pode trocar a marca pelo painel.
- **Segurança do upload:** tipo detectado pelo conteúdo, SVG com script/`javascript:`/`onload=` é recusado,
  arquivos servidos em `/marca/...` com CSP `sandbox`; corpo do envio limitado antes da leitura.
- E-mails usam a cor secundária no cabeçalho e a principal no botão.
- Agente: nome do serviço do Windows passa a "Harvest - Agente de inventário".
- Compilação: `compilar.sh` / `compilar.ps1` geram as duas edições.
- **Gerador de MSI do agente corrigido** (pasta `wix` do pacote do agente):
  - pacote marcado como **64 bits** (antes o `light.exe` parava no erro ICE80 por usar a pasta de 64 bits num MSI de 32);
  - o `gerar-msi.ps1` acha o WiX v3 sozinho (o instalador do WiX não põe no PATH) e confere o resultado de cada passo;
  - versão do MSI = versão do agente (era sempre 1.0.0.0, então um MSI novo não substituía o anterior);
  - para o serviço "InventarioAgente" antes de trocar o executável; numa atualização não roda mais o
    `-desinstalar` do produto antigo (que apagava a pasta do agente segundos depois de o novo ser copiado);
  - nome e fabricante do MSI seguem a Marca.

---

## 1.18.0 — 03/11/2026

Projeto **Harvest** (nome dado por Daniel Costa). Só o servidor muda (agente continua 1.17.0).

- **Assinatura:** no login, "Desenvolvido por Daniel Costa · T.I Ocupacional" (link para o LinkedIn);
  no rodapé do painel, "Harvest vX · desenvolvido por Daniel Costa — T.I". Discretas.
- **Easter egg:** código Konami (↑ ↑ ↓ ↓ ← → ← → B A) em qualquer tela, inclusive o login — um enxame
  de partículas douradas "colhe" a tela e forma HARVEST, com versão e crédito. Esc ou clique fecha; o
  mouse espanta o enxame; respeita "reduzir movimento" do sistema. Se o código for digitado dentro de
  um campo, as letras não ficam no campo. (`static/harvest.js`)

---

## 1.17.0 — 03/11/2026

Foco: **ida para a nuvem sem parar** (banco junto, servidor antigo no ar até o fim). Servidor e agente
mudam (agente 1.17.0). Passo a passo em `MIGRAR-PARA-NUVEM.md`.

- **Importar banco de outro servidor** (Administração): envia o `.db` baixado do antigo + senha; o
  arquivo é conferido (SQLite, tabelas, token, `integrity_check`), o banco atual vai para
  `backups\antes-de-importar-<data>` e o servidor reinicia sozinho usando o importado. Mantém o endereço
  do servidor novo, limpa a migração que veio do antigo e as sessões.
- **Download manual do banco** continua em Administração → Backup (cópia consistente com o sistema em uso).
- **Migração com o antigo no ar**: ao autorizar, os agentes conectados em tempo real recebem a ordem na
  hora (evento `migrar`); os outros, no próximo envio. O agente só troca depois de confirmar que o novo
  responde e reconecta o tempo real direto no novo.
- Durante a migração, o antigo **redireciona** Abrir chamado / Acompanhar para o novo e mostra a faixa
  "Migração em andamento".

---

## 1.16.0 — 03/11/2026

Foco: **excluir com segurança (Lixeira)**, ramais duplicados e usuários locais. Só o servidor muda
(agente continua 1.15.0).

**Lixeira (Inventário → Lixeira)**
- **Excluir máquina** agora move para a Lixeira, com cópia de tudo (cadastro, placas de rede, logins,
  ramais e o vínculo com os chamados). **Restaurar** devolve com o mesmo número (#) e religa os chamados.
- Enquanto está na lixeira, o agente dela pode continuar enviando: o envio é aceito e ignorado (a máquina
  não volta sozinha) e a Lixeira mostra "ainda enviando".
- **Excluir permanentemente** (só administrador) apaga a cópia de vez. Se o agente ainda estiver
  instalado, ela volta como máquina nova no próximo envio (o aviso de confirmação diz isso).
- A Lixeira também lista os **ramais excluídos**, com Restaurar e Excluir permanentemente.

**Ramais**
- Botão **excluir** por ramal (todas as ocorrências) na lista e por ocorrência (máquina + usuário) na
  página do ramal — vão para a Lixeira. Envios novos do agente não "ressuscitam" um ramal excluído.
- **Aviso de ramal duplicado**: o mesmo número ativo em mais de um lugar aparece em vermelho na aba
  Ramais (com onde está), com selo "duplicado", e em Notificações.

**Usuários locais**
- Inventário → Usuários ganhou a chave **Esconder usuários locais** (ligada por padrão): contas da própria
  máquina (PC-XX\Usuario, .\Admin, sem domínio) e as marcadas como local somem da lista e da contagem.
  Desligada, elas aparecem com o selo "local".

---

## 1.15.0 — 03/11/2026

Foco: **Work Monitor de volta** (programa monitorado). Servidor e agente mudam (agente 1.15.0).

- Diagnóstico na máquina do Daniel: o Work Monitor **não cria serviço do Windows**; roda como processo
  do usuário em `%AppData%\spa\work.exe` (C:\Users\<usuário>\AppData\Roaming\spa), assinado pela
  SMART – Computadores e Serviços Ltda (Bauru/SP). Por isso a detecção antiga (lista de programas
  instalados, que o agente como SYSTEM não vê por usuário) falhava.
- **Configurações → Programa monitorado** (admin): nome, processo(s), caminho dentro da pasta do usuário
  e caminhos completos opcionais. Já vem com o Work Monitor. O servidor manda isso aos agentes na
  resposta de cada envio; quando muda, o agente confere e reenvia na hora.
- Agente 1.15: procura o arquivo na pasta de cada perfil e confere os processos em execução (com o
  caminho completo). Situação na máquina: **rodando** · **parado** (usuário logado e programa fechado)
  · **instalado** (ninguém logado) · **sem** (não instalado) · não verificado.
- Inventário: selo na lista ("sem Work Monitor" / "Work Monitor parado"), filtros Sem/Com, Painel e
  Início com a contagem, cartão na página da máquina, e **Notificações → Work Monitor ausente ou
  parado** (chave liga/desliga).

---

## 1.14.0 — 03/11/2026

Foco: **AnyDesk**. Servidor e agente mudam (agente 1.14.0 chega pelo auto-update).

**Agente 1.14.0**
- Lê o ID do AnyDesk em `C:\ProgramData\AnyDesk\system.conf` (instalado/serviço) e em
  `C:\Users\<usuário>\AppData\Roaming\AnyDesk\system.conf` de cada perfil (portátil). Também
  procura pastas de cliente personalizado (`AnyDesk-xxxx`, subpastas `ad_xxxx`).
- Lê **só** `ad.anynet.id` e `ad.anynet.alias` — nenhuma senha/permissão.
- Confere se o AnyDesk está instalado pelo serviço do Windows (AnyDesk*) ou pelo AnyDesk.exe em
  Arquivos de Programas.

**Inventário**
- Situação por máquina: instalado · só portátil · instalado sem ID · não instalado · não verificado
  (agente antigo).
- Lista de máquinas mostra o ID (ou "sem AnyDesk"); filtro **Sem AnyDesk**; Painel e Início com a
  contagem; página da máquina com cartão **Acesso remoto** (ID, Copiar, **Conectar** — abre o AnyDesk).
- **Notificações → AnyDesk ausente** (chave liga/desliga, ligada por padrão) com a lista das máquinas.

**Chamados**
- Ao abrir um chamado com a máquina identificada (pelo IP na página sem login, ou escolhida pela TI),
  o **ID do AnyDesk fica gravado no chamado** (evento na linha do tempo) e aparece com Copiar/Conectar.
- Trocar a máquina do chamado troca o AnyDesk. Se a máquina só reportar o AnyDesk depois, os chamados
  em aberto dela recebem o ID sozinhos.
- O e-mail de chamado novo/atribuído inclui o AnyDesk.

---

## 1.13.1 — 31/10/2026

- **"Atualizar o servidor" pelo painel mais robusto:** o script de troca agora espera o processo antigo
  liberar o .exe (até ~60 s, antes eram 4 s) e grava cada passo em `atualizar-servidor.log` na pasta do
  servidor. Antes, se o arquivo ainda estivesse preso (antivírus/Windows), a troca falhava calada e o
  servidor voltava na versão anterior (caso relatado: painel continuou na 1.12).
- Servidor aceita `-endereco http://IP:PORTA` junto com `-instalar` (grava o endereço dos agentes no banco).
- Kit **INSTALAR-SERVIDOR** (pergunta IP, porta, pasta e manter/zerar dados; backup e volta atrás se falhar).
- Kit **ATUALIZAR-SERVIDOR**: atualiza pela linha de comando/duplo clique — acha a instalação pela
  tarefa, faz backup do banco, troca o .exe, confere a versão em /saude e desfaz se não subir.

---

## 1.13.0 — 31/10/2026

Foco: **Configurações** (aba com engrenagem). Só o servidor muda (agente continua 1.12.0).

- **Tema claro/escuro** por conta (salvo no banco: vale em qualquer computador). No claro, o logo
  colorido e uma paleta própria; o lima fica nos botões e destaques, e os textos de destaque usam um
  verde escuro legível.
- **Menu principal no topo ou na lateral** (por conta). A lateral é fixa à esquerda com o usuário no pé;
  em telas estreitas (até 900 px) volta para o topo. No topo, em telas até ~1560 px a aba
  Configurações mostra só a engrenagem.
- **Setores (só administrador):** lista de setores permitidos (um por linha, ordenar, importar os já
  usados). Com a lista preenchida, o campo Setor vira lista de escolha em: abrir chamado (inclusive a
  página sem login), novo chamado, usuários, máquinas e ramais. Registros antigos com setor fora da
  lista continuam e aparecem como "fora da lista". A página sem login só aceita setores da lista.
  Lista vazia = texto livre, como antes.

---

## 1.12.0 — 31/10/2026

Foco: **atualização em tempo real** (SSE) e **teste de conexão** no painel. Servidor e agente mudam.

**Agente 1.12.0 — serviço "InventarioAgente"**
- Novo serviço do Windows (conta **SYSTEM**, início automático atrasado, reinicia sozinho se cair)
  que fica conectado ao servidor em `/api/v1/eventos` (Server-Sent Events, só biblioteca padrão).
- Quando o servidor distribui uma versão nova, o serviço baixa e troca o executável **na hora**
  (com espera aleatória de até 90 s para não baixarem todas juntas) e reinicia já na versão nova.
- **Sem pedir permissão de administrador nas atualizações:** a permissão só é pedida na instalação
  (que cria o serviço). Daí em diante tudo roda como SYSTEM.
- Máquinas já instaladas **ganham o serviço sozinhas**: a tarefa agendada (SYSTEM) cria o serviço
  quando o agente chega na 1.12 pelo auto-update de hora em hora.
- A tarefa agendada continua (inventário de hora em hora, logon e boot) como plano B. Se a tarefa
  trocar o executável, o serviço percebe em até 2 min e reinicia na versão nova. Trava de arquivo
  evita que os dois troquem o .exe ao mesmo tempo.
- Reconexão automática com espera crescente (5 s → 2 min); 90 s sem sinal = reconecta.

**Servidor — Administração → Conexões (nova)**
- Máquinas conectadas agora (versão, IP, desde quando).
- **Testar conexão com as máquinas:** manda um teste para todas; cada uma responde se recebeu e se
  **aceitaria a atualização** (está em Program Files, roda como SYSTEM, consegue criar e renomear
  arquivos na pasta — sem atualizar nada). Resultado: recebeu / aceitaria / já atual / sem resposta.
- **Mandar atualizar agora:** avisa todas as conectadas da versão distribuída.
- Lista das máquinas que mandaram inventário mas não estão conectadas, com o motivo.
- Ligar o auto-update avisa as conectadas na hora; ao reiniciar o servidor (atualização), todas
  reconectam e recebem a versão nova.
- As conexões de eventos não ocupam as 24 vagas de requisições simultâneas.

**Kits:** REINSTALAR e REPARAR param/removem o serviço antes de mexer na pasta; DIAGNOSTICO mostra o
estado do serviço.

---

## 1.11.2 — 31/10/2026

**Causa encontrada do "não atualiza sozinho":** o diagnóstico da máquina DESKTOP-9EBRJQP mostrou
`servidor respondeu 401: token inválido`. O servidor foi reinstalado e ganhou um token novo; as máquinas
antigas continuam com o token anterior, são recusadas e, sem conseguir enviar, nunca recebem o auto-update
(ficam "paradas" na 1.7 no painel).

- **Tokens anteriores aceitos:** o servidor anota as tentativas recusadas (token abreviado, máquina, IP,
  versão) em Administração → Token dos agentes. Botão **Aceitar** libera aquele token — todas as máquinas
  que o usam voltam a enviar no próximo contato e recebem o auto-update.
- Agentes **1.11.2+** que usam um token anterior recebem o token atual na resposta e passam a usá-lo
  sozinhos. Depois que ninguém mais aparecer recusado, dá para remover o token antigo.
- Diagnóstico do auto-update ganhou a etapa **"Token recusado"**.
- Kit do agente: DIAGNOSTICO aponta a causa 401; REPARAR confere o token no servidor antes de reinstalar
  e pede o atual se for recusado.

---

## 1.11.1 — 31/10/2026

Foco: **descobrir por que os agentes não se atualizam sozinhos** e ramais **sem histórico**.
Servidor e agente mudam (agente 1.11.1).

**Diagnóstico do auto-update (Administração → "Diagnóstico do auto-update")**
- O servidor passa a anotar, por máquina: quantas vezes ofereceu a versão nova, quando o .exe foi
  baixado (pelo IP) e o erro que o agente informar. A tela mostra em que etapa cada máquina parou:
  *sem contato* · *aguardando contato* · *oferecida, nunca baixou* (agente fora de
  C:\Program Files\Inventario) · *baixou, mas não trocou* (antivírus) · *baixou — trocando*.
- O servidor.log registra cada oferta e cada download ("auto-update: …").
- Agente 1.11.1 grava o motivo de qualquer falha do auto-update (`auto-update-erro.txt`) e manda no
  inventário seguinte — inclusive quando roda fora da pasta padrão (antes saía calado).
- Kit **Inventario-v1.11-AGENTE**: DIAGNOSTICO-AGENTE.bat (relatório na Área de Trabalho, não altera
  nada) e REPARAR-AGENTE.bat (reinstala o agente com o mesmo servidor/token do config.json).

**Ramais sem histórico**
- O agente não lê mais o call_log.db (sem SQLite no agente): volta a ter ~8 MB. A aba Ramais mostra
  só ramal, nome, setor e máquina.

---

## 1.11.0 — 31/10/2026

Foco: **aba Ramais** (MicroSIP). **Servidor e agente mudam** (agente 1.11.0 — chega pelo auto-update).

**Agente 1.11.0**
- Lê o MicroSIP de todos os perfis da máquina (`C:\Users\*\AppData\Roaming\MicroSIP\MicroSIP.ini`,
  UTF-16) e da versão portátil em `Arquivos de Programas\MicroSIP` / `C:\MicroSIP`.
- Envia as contas SIP (`[AccountN]`: username/ramal, authID, displayName, server/domain, label) —
  **a senha SIP nunca é lida/enviada**.
- Histórico: `call_log.db` (SQLite, tabela `call_log`) — últimos 60 dias, até 300 chamadas por perfil.
  Lê uma cópia temporária, então funciona com o MicroSIP aberto.

**Aba Ramais (nova, admin e técnico)**
- Lista "Daniel Costa - Ramal 2010 - Setor TI", com máquina, usuário do Windows, chamadas dos últimos
  30 dias (feitas / recebidas / perdidas) e quando foi visto. Busca, inativos e exportação .csv.
- Nome: ajuste manual → cadastro de Usuários → displayName do MicroSIP → pasta do Windows
  (`daniel.costa` → Daniel Costa). Setor: ajuste manual → cadastro do usuário → setor da máquina.
- Página do ramal: onde está, correção de nome/setor e histórico de chamadas (90 dias no servidor),
  com filtro por tipo e o nome do ramal interno quando a ligação foi para outro ramal.
- Tipos de chamada como o MicroSIP grava: 0 = feita, 1 = recebida, 2 = perdida.

---

## 1.10.1 — 31/10/2026

Foco: **saber se o e-mail dos chamados saiu ou não**. Só o servidor muda (agente continua 1.10.0).

- **Histórico de envios** em Administração → E-mail dos chamados → "Últimos envios": data, para quem,
  assunto e **enviado / falhou** com o motivo.
- **No próprio chamado**, a linha do tempo mostra "✉ e-mail enviado para …" ou "e-mail NÃO enviado
  para …: motivo".
- **Erros explicados em português** (Microsoft 365): SMTP autenticado desligado (5.7.139), usuário/senha
  recusados ou MFA sem senha de app (535 / 5.7.3), remetente diferente da conta (5.7.60), porta
  bloqueada (timeout), endereço errado, certificado.
- O **e-mail de teste** diz claramente se o servidor SMTP aceitou ou o motivo da falha.
- Tempo-limite de 30 s na conversa com o SMTP (antes podia ficar pendurado sem avisar).

---

## 1.10.0 — 31/10/2026

Foco: **testar o auto-update dos agentes**, avisos de chamado (som no navegador e e-mail),
chaves liga/desliga nas notificações e retirada temporária do Work Monitor.
**Servidor e agente mudam** (agente 1.10.0 — as máquinas na 1.7 se atualizam sozinhas).

**Agente 1.10.0 (teste do auto-update)**
- Sem mudança de coleta. O log (`C:\ProgramData\Inventario\agente.log`) passa a mostrar a versão em
  cada envio: "inventário enviado para … (agente v1.10.0)".
- Com o servidor 1.10 e o "Auto-update dos agentes" ligado, cada máquina na 1.7 baixa a 1.10 no
  próximo contato (até 1 h) e passa a usá-la na execução seguinte. Acompanhe em Inventário →
  "Versões do agente".
- Proteção nova no servidor: se o agente embutido não for da versão anunciada (servidor compilado
  sem o compilar.sh/ps1), o auto-update não é oferecido — evita as máquinas baixarem o mesmo .exe
  em laço.

**Aviso de chamado novo no navegador**
- Com o sistema aberto em qualquer aba, a cada 20 s o navegador confere se chegou chamado. Chegando,
  **toca um som**, mostra um **aviso no canto** (clicável), atualiza o contador do menu e, se a aba
  estiver em segundo plano, o **título da aba** mostra "(1) Novo chamado". Destaca "Chamado para
  você" quando já veio atribuído a você.
- Botão de **alto-falante** no topo liga/desliga (vale por navegador). A notificação do Windows fora
  do navegador só funciona em https (limitação dos navegadores).

**E-mail dos chamados (novo)**
- Configuração SMTP em Administração → E-mail dos chamados (Microsoft 365, Gmail ou relay interno;
  STARTTLS, TLS ou sem segurança; AUTH LOGIN/PLAIN), com botão de e-mail de teste.
- **E-mail para o responsável** quando um chamado é direcionado a ele; **e-mail para toda a equipe**
  a cada chamado novo (opcional); **responsável automático**: chamados novos sem responsável já
  chegam atribuídos a quem você escolher (inclusive os da página sem login).
- Cada pessoa cadastra o próprio e-mail em **Minha conta**; o admin também edita nome/e-mail de
  qualquer usuário na lista de Administração.

**Notificações com chaves**
- Nomes genéricos, máquinas inativas e disco quase cheio agora têm **chave liga/desliga** cada um
  (salva ao clicar), com o valor (dias / % livre / padrões) ao lado. O valor não se perde ao desligar.

**Work Monitor (pendente)**
- O "programa monitorado" saiu das telas (Início, Painel, filtros de máquinas e Administração)
  porque a detecção não estava confiável. O código continua no lugar
  (`MonitoramentoProgramaAtivo` em servidor.go) para voltar numa versão futura. A lista de
  programas instalados e o filtro "Programa instalado" continuam.
- No Início, o lugar virou "Agente desatualizado".

> **Atualização:** aplique o servidor 1.10 (Administração → Atualizar o servidor). Confira que o
> "Auto-update dos agentes" está **ligado** em Administração. Os agentes 1.7 vão para a 1.10 sozinhos.
> Se o Bitdefender bloquear a troca do .exe, a máquina continua na 1.7 — veja o agente.log.

---

## 1.9.0 — 30/10/2026

Foco: **novo visual com a marca Ocupacional** (direção "Bento lima", escolhida entre 3 propostas).
**Só o servidor muda**: o agente continua na 1.7.0.

**Novo visual**
- Tema escuro grafite com o **lima** (#ADDA23) e o **petróleo** (#18424C) da marca, blocos
  arredondados, botões e abas em pílula. Fontes **Montserrat** (títulos) e **Inter** (texto),
  servidas pelo próprio servidor (funcionam sem internet; licença OFL em `static/fontes/`).
- **Logo da Ocupacional** no topo, na tela de entrada e nas páginas públicas de chamado; símbolo
  como ícone da aba do navegador.
- Topo novo: logo + menu em pílula (**Início · Chamados · Inventário · Relatórios ·
  Administração**, conforme o perfil) + sino de notificações + avatar com nome e perfil.
- Todas as telas existentes (inventário, máquinas, usuários, duplicados, administração,
  chamados, relatórios) passaram para o novo tema sem mudar o funcionamento.

**Página Início (nova, admin e técnico)**
- Bloco lima com os **chamados em aberto** (sem responsável, atrasados, vencendo) e atalhos para a
  fila e para abrir chamado.
- **% resolvido no prazo** (30 dias) e tempo médio; **servidor** (versão, endereço, último backup,
  agentes em dia); **parque** (máquinas e nível OK/Alerta/Crítico); **pedem atenção** (MAC
  duplicado, sem Work Monitor, disco < 10%, nome genérico).
- **Fila** com os 5 chamados mais urgentes, **semana** (chamados abertos por dia), **atalhos**
  (Excel, duplicados, agente, backup, copiar link de chamados, relatórios) e **por categoria**.
- Perfil "usuário" continua indo direto para os próprios chamados.

**Ajustes**
- Gráfico "abertos × resolvidos" dos relatórios com cores distintas no tema novo.
- A faixa de MAC duplicado não aparece mais para o perfil "usuário" (ele não acessa o inventário).

> **Atualização:** só o servidor (Administração → Atualizar o servidor, com
> `Inventario_servidor_v1.9.exe`). Nada muda no banco.

---

## 1.8.0 — 30/10/2026

Foco: **sistema de chamados Beta** — funcionários abrem chamado sem login, histórico, avisos,
prazos (SLA), relatórios e anexos. **Só o servidor muda**: o agente continua na 1.7.0.

**Chamado sem login (novo)**
- Página **/abrir-chamado** para qualquer funcionário da rede interna, sem conta. A pessoa escolhe o
  nome da lista do inventário (ou digita), o setor, o tipo de problema e o quanto atrapalha
  (baixa/normal/alta — "urgente" fica com a TI).
- O **computador é vinculado sozinho** pelo IP de onde o chamado foi aberto (ou pelo último login da
  pessoa) e a página já sugere o nome de quem está logado nele.
- Depois de enviar, a pessoa recebe um **link de acompanhamento** (/acompanhar/…) para ver o andamento
  e responder, também sem login. Responder um chamado resolvido **reabre** o chamado.
- Proteções: liga/desliga em Administração, limite de 10 chamados/hora por computador, campo isca
  contra robôs. Link "Abra um chamado sem login" na tela de entrada.

**Histórico e avisos (novo)**
- **Linha do tempo** em cada chamado: abertura, mudanças de status, prioridade, categoria,
  responsável e máquina (quem fez e quando), junto com os comentários.
- **Nota interna**: comentário que o solicitante não vê.
- Botão **Assumir** (atribui a você e passa para "em andamento").
- **Avisos no webhook** (Teams/Slack/Discord): chamado novo, atribuição, resolução e resposta do
  solicitante. Pode usar um **webhook só dos chamados** (ex.: canal de suporte) ou o mesmo do MAC duplicado.

**Prazos (SLA) e relatórios (novo)**
- Prazo por prioridade, em horas corridas (padrão: urgente 4 h, alta 8 h, normal 24 h, baixa 72 h),
  ajustável em Administração. Selo "vence em…" / "atrasado há…" na lista e no chamado; mudar a
  prioridade recalcula o prazo.
- Lista com atalhos **Em aberto / Meus / Sem responsável / Atrasados / Resolvidos**, busca por número,
  título, solicitante, setor ou máquina, e **exportação CSV**.
- Página **Relatórios**: abertos × resolvidos no tempo, tempo médio e mediana até resolver, % dentro do
  prazo, distribuição por categoria/setor/prioridade e desempenho por responsável (7/30/90 dias, 12 meses).

**Anexos (novo)**
- Arquivos e prints na abertura e nos comentários (até 5 por envio, 10 MB cada). Dá para **colar um
  print com Ctrl+V** ou arrastar. Imagens aparecem na página; o resto é baixado.
- Executáveis, scripts e páginas são recusados. Os anexos ficam **dentro do banco**, então entram no
  backup diário e na migração sem pasta extra.

**Outros**
- A versão do **agente embutido** passou a ser separada da do servidor (`VersaoAgente`, gravada pelos
  scripts de compilação). Assim o servidor 1.8 **não dispara auto-update** nas máquinas (que já estão
  na 1.7) nem as marca como desatualizadas. Agentes 1.6 continuam sendo levados para a 1.7.
- A barra de subseções (Painel / Máquinas / …) ganhou estilo — antes aparecia como links soltos.
- Mensagens de sucesso/erro não se perdem mais em endereços com âncora (#).

> **Atualização:** só o servidor (Administração → Atualizar o servidor, enviando o
> `Inventario_servidor_v1.8.exe`). O banco é migrado sozinho na primeira inicialização (chamados
> antigos ganham prazo e link de acompanhamento). Antes de divulgar o link /abrir-chamado, confira em
> Administração o **endereço fixo do servidor** — é ele que vai nos links e avisos.

---

## 1.7.0 — 29/10/2026

Foco: **programas instalados** (incl. Work Monitor), **sistema de chamados (Alpha)** e **perfis de acesso**.

**Programas instalados (novo)**
- O agente passa a coletar a **lista de programas instalados** na máquina (nome, versão,
  fabricante — das chaves de desinstalação do Windows, 64 e 32 bits). Aparece na **ficha** de cada
  máquina.
- **Programa monitorado:** em Administração dá para definir um programa (padrão **Work Monitor**).
  O painel mostra **quantas máquinas estão sem ele**, e há filtros rápidos "Com/Sem Work Monitor"
  na lista de máquinas. No filtro personalizado também há o campo **Programa instalado** (contém/
  não contém).

**Perfis de acesso (novo)**
- Três perfis: **Administrador** (tudo), **Técnico** (vê inventário e gerencia todos os chamados) e
  **Usuário** (só abre/vê os próprios chamados, sem inventário).
- O **inventário** agora é restrito a admin/técnico; a **Administração** só a admin. Cada usuário
  tem **e-mail** e **provedor de login** no cadastro — estrutura pronta para, no futuro, o login vir
  do **e-mail corporativo (Microsoft)**. Por ora, contas **locais**.
- "Minha conta" para cada usuário trocar a própria senha.

**Sistema de chamados — Alpha (novo)**
- Menu principal alterna entre **Chamados** e **Inventário**.
- Chamado com **título, descrição, categoria, prioridade, status** (aberto / em andamento /
  resolvido / fechado), **atribuição a um técnico**, **comentários/andamento** e **vínculo com uma
  máquina** do inventário.
- Lista com filtro por status; contador de chamados abertos no menu.

> **Atualização:** aplique o servidor 1.7 (`ATUALIZAR-para-v1.7.bat`). Para os agentes coletarem a
> lista de programas, eles precisam estar na **1.7** — com o auto-update (1.6+) isso acontece
> sozinho; máquinas ainda em versões antigas pegam os programas depois de chegarem à 1.7. Perfis,
> chamados e o painel de Work Monitor já funcionam assim que o servidor é atualizado.

---

## 1.6.0 — 29/10/2026

Foco: **auto-update dos agentes** (as máquinas se atualizam sozinhas) e intervalo de contato menor.

**Auto-update (novo)**
- O agente passa a **se atualizar sozinho**: quando entra em contato com o servidor e está numa
  versão anterior à que o servidor distribui, ele **baixa o novo executável e troca a si mesmo**
  (a troca usa renomeação, então vale na próxima execução da tarefa — não dá conflito com o
  processo em uso). **Não precisa reinstalar máquina por máquina:** basta atualizar o servidor.
- Endpoint novo, autenticado pelo token do agente: `GET /api/v1/agente.exe` (é por ele que o
  agente baixa a versão nova). Ninguém sem o token baixa o executável.
- Liga/desliga em **Administração → Atualizar o servidor → Auto-update dos agentes** (vem ligado).

**Intervalo de contato**
- Padrão caiu de **4 h para 1 h** (além de continuar no logon e na inicialização). Instalações
  antigas que estavam em 4 h **migram para 1 h** sozinhas quando o agente roda. Assim a frota se
  atualiza e reporta mais rápido, sem depender de acesso remoto às estações.
- A tarefa agendada se **reconfigura sozinha** (uma vez por versão) para aplicar o novo intervalo,
  sem derrubar a execução em curso.

> **Importante (primeira vez):** o auto-update só funciona a partir de agentes **1.6 em diante** —
> as versões antigas não têm esse código, então não se atualizam sozinhas para a 1.6. Então o
> primeiro salto para a 1.6 ainda é manual nas estações (rodar o `Inventario_agente_v1.6.exe`, ou
> por GPO). **Daí em diante** (1.6 → 1.7 → …) é tudo automático: você só atualiza o servidor.
>
> Sugestão para colocar a frota na 1.6 de uma vez: distribua o `Inventario_agente_v1.6.exe` pela
> sua rotina de sempre (GPO/script de logon). Depois dessa, nunca mais precisa tocar nas máquinas
> para atualizar o agente.

---

## 1.5.1 — 29/10/2026

Foco: correção da leitura da geração do processador no relatório de nível (só servidor).

**Correção**
- **Geração de processadores Intel 10ª–14ª (modelos de 4 dígitos).** Chips de notebook como
  `i5-1035G1`, `i3-1005G1`, `i7-1065G7`, `i5-1135G7`, `i5-1235U`, `i7-1260P` estavam sendo lidos
  como **1ª geração** (pegava só o primeiro dígito do "1035"), o que derrubava máquinas ótimas
  para **Alerta/Crítico**. Agora são lidos corretamente (10ª, 11ª, 12ª…). Um i5 de 10ª com SSD
  volta a dar **OK**.
- **AMD Ryzen passa a contar geração.** Antes o Ryzen recebia 0 ponto de geração (a regra só
  reconhecia Intel). Agora a série (5000/4000/3000/2000) é pontuada de forma equivalente.
- **Detecção de SSD:** confirmado que já usa o mesmo dado que aparece na aba "Discos" da ficha —
  **não precisa reinstalar agente**. O problema era só a leitura da geração acima.

**Melhoria**
- A verificação de "agente desatualizado" passou a comparar por **série (maior.menor)**: uma
  correção de revisão do servidor (1.5.0 → 1.5.1) **não** marca mais os agentes como
  desatualizados. Agentes 1.5.x continuam "na versão atual".

> **Atualização só do servidor.** O agente não mudou; não é preciso mexer nas estações. Aplique o
> `Inventario_servidor_v1.5.1.exe` (pelo `.bat` ou pelo painel) e o relatório de nível se corrige
> sozinho para todas as máquinas já cadastradas.

---

## 1.5.0 — 29/10/2026

Foco: correções apontadas em uso, usuários locais, mais filtros e **relatório de nível das máquinas**.

**Correção**
- **Versão na Administração.** A tela de Administração mostra a versão real do servidor em
  execução. Se aparecia "1.2.0", era porque o servidor ainda não tinha sido atualizado — não era
  bug de tela. Com a 1.5 aplicada, todas as telas (Administração, rodapé, `/saude`, selo "servidor
  v…") exibem **1.5.0**.

**Usuários locais (novo)**
- Opção para **marcar um usuário como local / padrão do Windows** (Administrador, Convidado,
  defaultuser0 etc.). Usuário marcado deixa de contar como **duplicado** e ganha um selo
  "usuário local (ignorado)" na ficha. Dá para voltar atrás a qualquer momento.

**Mais filtros**
- Filtro personalizado agora aceita **processador (CPU)** e **tem SSD (sim/não)** como campos de
  texto, e **RAM (GB)**, **disco total (GB)** e **núcleos da CPU** como campos numéricos, com os
  operadores **≥** e **≤**. Ex.: "RAM ≤ 4", "Processador contém i7", "Tem SSD = não".

**Relatório de nível das máquinas (novo)**
- No **painel**, cartão "Nível do parque (saúde das máquinas)": rosca com OK / Alerta / Crítico e
  atalhos que filtram a lista por nível.
- Na **lista de máquinas**, coluna **Nível** (selo + pontos/10). Na **ficha** de cada máquina, um
  cartão com a classe, a pontuação e o detalhamento item a item.
- **Pontuação (máx. 10):** geração do processador (2ª–3ª=0, 4ª–5ª=1, 6ª–7ª=2, 8ª+=3), linha
  i3=0 / i5=1 / i7+=2, RAM (≤4GB=0, 6–8GB=1, 10–12GB=2, 16GB+=3), armazenamento (sem SSD=0,
  SSD<200GB=1, SSD≥200GB=2).
- **Classes (padrão):** OK ≥ 6 · Alerta ≥ 4 · Crítico < 4 **ou** RAM ≤ 4 GB.
- **Critérios ajustáveis** em Administração → "Critérios de nível das máquinas" (os limites de OK,
  Alerta e o RAM que força Crítico). As regras de pontos podem ser ajustadas no código numa versão
  futura, se necessário.

---

## 1.4.0 — 28/10/2026

Foco: correção do endereço no instalador e visibilidade das versões do agente.

**Correção**
- **Endereço do servidor para os agentes** (Administração): campo fixo para o endereço que as
  estações usam. Antes, o instalador baixado usava o endereço pelo qual o admin abriu o painel —
  abrindo em `localhost`, o agente saía apontando para localhost. Agora, com o endereço
  preenchido, o `config.json`, o pacote .zip e o comando de instalação usam sempre o endereço
  certo. O agente para download é sempre o da versão do servidor em execução (atualize o servidor
  para que o download acompanhe).

**Versões do agente (novo)**
- No **painel**: gráfico "Versões do agente" (verde = versão atual do servidor), cartão
  "agentes desatualizados" e linha em "Precisam de atenção".
- Na **lista de máquinas**: coluna **Agente** com a versão de cada máquina e um aviso ⚠ quando
  está desatualizada; filtro rápido "Agente desatualizado".
- Cada máquina já informava a versão do agente; agora isso fica visível e comparável com a versão
  atual do servidor.

---

## 1.3.0 — 28/10/2026

Foco: instalação do agente mais limpa e nomes de arquivo versionados.

**Agente**
- **Instalação limpa:** ao instalar, o agente agora **para e remove a versão anterior**
  (encerra e apaga a tarefa agendada antiga e remove executáveis sobrando) antes de colocar a
  nova. Evita conflito de tarefa/arquivo ao atualizar as estações.
- **Confirmado: instalação por máquina, não por usuário.** O agente fica em
  `C:\Program Files\Inventario` e roda como **SYSTEM**, com gatilho no logon de **qualquer**
  usuário, na inicialização e a cada 4 h. Funciona mesmo que o funcionário troque de usuário na
  máquina. (Já era assim; nesta versão a reinstalação ficou à prova de sobras.)

**Entrega**
- Os arquivos de atualização passam a vir **nomeados com a versão**, por exemplo
  `Inventario_servidor_v1.3.exe` e `Inventario_agente_v1.3.exe`. Ao instalar, o agente se copia
  para o nome fixo interno (`inventario-agente.exe`) de qualquer forma, então o nome do arquivo
  de distribuição pode conter a versão sem problema.

**Observação:** a segurança avançada (token por máquina, 2FA, criptografia do banco) passa a ser
planejada para a 1.4.

---

## 1.2.0 — 28/10/2026

Foco: painel com gráficos, filtros personalizados e notificações.

**Painel**
- **Gráficos** no painel: rosca com o status das máquinas (ativas até 7 dias, ociosas 7–14,
  inativas +14), barras de sistemas operacionais e de máquinas por setor.
- Cartão "Precisam de atenção": disco quase cheio, sem setor/responsável no cadastro, atalho
  para os alertas.

**Filtros**
- Filtros rápidos na lista de máquinas: "Com MAC duplicado" e "Com nome genérico".
- **Filtros personalizados salvos**: crie filtros por campo (nome, usuário, SO, setor, IP, MAC,
  modelo, patrimônio) e operador (contém, não contém, começa com, igual). Ex.: "Notebooks EMC".

**Notificações (menu novo)**
- Avisos configuráveis: nomes genéricos (ex.: DESKTOP-A1B2C3D, WIN-XXXX, mais padrões próprios),
  máquinas inativas há mais de N dias e disco com menos de X% livre. A página mostra, ao vivo,
  as máquinas que se encaixam em cada regra.

**Observação de desempenho:** veja a seção "Desempenho" ao final deste arquivo.

---

## 1.1.0 — 28/10/2026

Foco: segurança e robustez (sem mudar o fluxo de instalação dos agentes).

**Segurança**
- **Proteção da pasta do agente (ACL).** Na instalação, `C:\Program Files\Inventario` e
  `C:\ProgramData\Inventario` passam a permitir escrita só para SYSTEM e Administradores;
  usuários comuns só leem. Fecha uma brecha de escalonamento local (um usuário comum não
  consegue mais trocar o `coleta.ps1`, que roda como SYSTEM).

**Confiabilidade**
- **Backup automático diário** do banco, na pasta `backups/` ao lado do `inventario.db`,
  com retenção de 14 dias (os mais antigos são apagados). Continua existindo o backup
  manual no painel.
- **Rotação do log do servidor** (`servidor.log`): ao passar de 5 MB, gira para `.1`…`.5`.
  Não cresce mais sem limite.
- **Endpoint `/saude`** (público, sem login): responde um JSON com status, versão, número
  de máquinas e hora. Útil para monitoração.

**Migração**
- **Migração em fases (canário).** No destino da migração dá para informar uma lista de
  hostnames que migram primeiro; depois é só limpar o campo (migra todas) e autorizar de
  novo.

**Adiado**
- HTTPS/TLS: aguardando a empresa ter um certificado. Até lá, o servidor segue em HTTP.

---

## 1.0.0 — base (rodadas anteriores)

Primeira versão em uso. Principais recursos:

- Agente Windows (coleta de hardware, sistema, rede e usuários) instalado como tarefa
  agendada (SYSTEM), enviando a cada 4 h, no logon e na inicialização.
- Servidor executável único com banco SQLite e painel web com login (senha PBKDF2,
  proteção CSRF, bloqueio após tentativas, registro de ações).
- Detecção de MAC duplicado com alerta no painel e por webhook (Teams/Slack etc.).
- Gestão de máquinas e usuários: editar, excluir, mesclar duplicados.
- Exportação para Excel (.xlsx) e CSV; backup do banco pelo painel.
- Download do instalador do agente pelo painel (.exe, .zip e código WiX para .msi).
- Atualização do servidor pelo próprio painel (envio do novo executável).
- Migração dos agentes para outro servidor, em dois passos com autorização por senha.
- Correção de estabilidade: motor SQLite em modo interpretador + limite de requisições
  simultâneas (o servidor não cai mais sob carga).
- Agente menos propenso a falso-positivo de antivírus (sem PowerShell codificado; tarefa
  criada via XML do schtasks).

---

## Próxima versão planejada — segurança avançada

Identidade e integridade (planejado):

- Token por máquina, com transição suave (o servidor aceita o token único antigo por um
  tempo e dá a cada agente a sua credencial) e revogação individual.
- 2FA (código de aplicativo autenticador) no login do painel.
- Auto-update do servidor com verificação de assinatura.
- Ordem de migração assinada (o agente só aceita migrar se a ordem for autêntica).
- Criptografia do banco e dos backups, com a chave em arquivo protegido no servidor.

---

## Desempenho — avaliação (28/10/2026)

No tamanho atual (dezenas a poucas centenas de máquinas), o sistema é **leve**: o servidor usa
pouca memória e cada envio de agente tem poucos KB. Os gráficos são desenhados no servidor em
SVG (sem bibliotecas), então não pesam no navegador.

Pontos a observar conforme crescer, e o que dá para enxugar:

- **`dados_json` por máquina:** o servidor guarda o relatório completo de cada máquina em JSON.
  Páginas que leem todas as máquinas (painel, notificações, exportação) percorrem esse JSON. Até
  ~1.000 máquinas é tranquilo. Acima disso, vale paginar a lista e pré-calcular os números do
  painel. (Fácil de fazer quando chegar a hora.)
- **Tabela de auditoria:** cresce sem limite. Sugestão para uma próxima versão: manter só os
  últimos N meses.
- **Modo interpretador do SQLite:** trocamos para ele na 1.0 por estabilidade (o modo compilador
  caía sob carga). É um pouco mais lento, mas imperceptível nesta escala. Não mexer.
- **Nada a remover hoje.** Todas as funções atuais têm uso. Se quiser reduzir o tamanho do
  executável, dá para separar o agente embutido do servidor (hoje o servidor carrega o agente
  para oferecer o download) — economiza ~8 MB no binário do servidor, mas aí o download do agente
  sai do painel. Não recomendo: o ganho é pequeno e perde comodidade.

Resumo: pode seguir tranquilo. Os únicos itens que viram tarefa quando o parque crescer muito são
paginar a lista de máquinas e limpar a auditoria antiga.
