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

## Próxima versão planejada — 1.2.0

Identidade e integridade (em desenvolvimento):

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
