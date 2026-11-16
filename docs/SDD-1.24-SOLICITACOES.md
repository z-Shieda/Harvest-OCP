# SDD — Harvest 1.24: Solicitações de Equipamento e perfis personalizados

> 16/11/2026, Daniel. Base: 1.23.1 (testada). Agente continua 1.22.0 (sem mudança).

## Contexto e objetivo

Hoje o pedido de compra de T.I vai direto ao **Suprimentos**: a T.I não fica sabendo, e o Suprimentos precisa chamar a T.I
para validar o que comprar. A 1.24 cria o menu **Solicitações de equipamento**, separado dos chamados, em que o pedido passa
primeiro pela T.I, depois pelo **gestor** que autoriza a compra, e só então chega ao Suprimentos — que registra o que foi
comprado e para qual setor. A T.I fecha com a entrega ao solicitante.

Junto vêm os **perfis personalizados**: além de Administrador, Técnico e Usuário, o admin cria perfis (Gestor, Gestor que
autoriza, Suprimentos…) escolhendo as áreas do menu e os poderes de cada um.

## Decisões

| # | Decisão | Escolha |
| --- | --- | --- |
| D1 | Quem pede | Qualquer pessoa logada; a T.I também pode pedir em nome de alguém |
| D2 | Etapas | Solicitada → T.I valida → Gestor autoriza → Suprimentos compra → T.I entrega |
| D3 | O que se pede | Catálogo mantido pela T.I (item + especificação sugerida) ou item livre |
| D4 | SLA | Sem SLA. Conta nos números de atendimento (Início e Relatórios) junto dos chamados |
| D5 | Autorização do gestor | E-mail com link, **mas só autoriza logado** (quem recebe o e-mail por engano não autoriza) |
| D6 | Gestor do pedido | Sugerido pelo setor do solicitante (gestor ligado a setores); a T.I confirma ou troca na validação |
| D7 | Perfis | Admin e Técnico fixos; o admin cria perfis = áreas do menu + poderes. Vêm criados: Gestor, Gestor que autoriza, Suprimentos |
| D8 | Poderes | `ver_setor` (vê as solicitações dos setores dele), `autorizar` (autoriza compras), `comprar` (registra compras) |
| D9 | Suprimentos | Perfil com o poder `comprar`. Recebe o e-mail só depois da T.I validar e do gestor autorizar |
| D10 | Registro da compra | Obrigatório: comprado/não comprado + setor de destino. Opcional: fornecedor, valor, nº do pedido/NF, previsão, anexos |
| D11 | Entrega | A T.I marca entregue; atalhos opcionais para cadastrar o ativo (com NF e valor da compra) e emitir o Termo de Entrega |
| D12 | Avisos | T.I: pedido novo (e-mail + aviso sonoro) e "comprado"; gestor: pedido para autorizar + lembrete a cada N dias; Suprimentos: autorizado; solicitante: cada mudança |
| D13 | Dispensa do gestor | Só o Administrador pode mandar direto ao Suprimentos, com motivo (fica na auditoria e na linha do tempo) |

## Fluxo

```
Solicitante:  Pede (catálogo ou livre, quantidade, justificativa, urgência, para quem) ──────────────────────┐
T.I:              Valida a especificação, escolhe o gestor ─ ou recusa (motivo) ─────────────────┐            │
Gestor:                         Recebe e-mail → entra no Harvest → Autoriza / Recusa (motivo)     │            │
Suprimentos:                                     Recebe e-mail → Em compra → Comprado / Não comprado + setor   │
T.I:                                                               Recebe → Entregue (+ ativo, + termo opcional)
Solicitante recebe e-mail em cada passo; pode cancelar enquanto não foi comprado.
```

**Situações:** `solicitada` → `aguardando_gestor` → `em_compra` → `comprada` | `nao_comprada` → `entregue`.
Fora da linha: `recusada` (T.I ou gestor) e `cancelada` (solicitante, antes da compra).

## Modelo de dados

| Tabela | Colunas principais |
| --- | --- |
| `perfis` | id, chave, nome, areas (lista), poderes (lista), sistema |
| `admins` (+) | perfil_id, setores_gestor (lista de setores pelos quais a pessoa responde) |
| `sol_catalogo` | id, nome, categoria, especificacao, ativo, ordem |
| `solicitacoes` | id, numero, solicitante_id, para_quem, setor, catalogo_id, item, especificacao, quantidade, justificativa, urgencia, situacao, ti_por/ti_em/ti_obs, gestor_id, autorizado_por/em/ip, motivo_recusa, dispensa_gestor, comprado, setor_destino, fornecedor, valor, pedido_nf, previsao, compra_por/em, entregue_por/em, ativo_id, termo_id, criado_em, atualizado_em, lembrete_em |
| `sol_eventos` | linha do tempo e comentários (autor, tipo, texto) |
| `sol_anexos` | anexos (orçamento, NF, foto) |

## Telas e rotas

| Rota | Tela | Quem |
| --- | --- | --- |
| `GET /solicitacoes` | Lista: minhas, do meu setor (`ver_setor`), para autorizar (`autorizar`), para comprar (`comprar`), todas (T.I) | Logado com a área |
| `GET/POST /solicitacoes/nova` | Pedir (catálogo ou livre) | Logado com a área |
| `GET /solicitacoes/{id}` | Detalhe com linha do tempo, anexos e a ação do papel de quem vê | Quem pode ver |
| `POST /solicitacoes/{id}/validar` · `/recusar` | T.I valida (especificação final, gestor) ou recusa | Admin, Técnico |
| `POST /solicitacoes/{id}/autorizar` | Gestor autoriza ou recusa | O gestor do pedido (`autorizar`) |
| `POST /solicitacoes/{id}/compra` | Registro da compra | `comprar`, Admin |
| `POST /solicitacoes/{id}/entregar` | Entregue (+ ativo, + termo) | Admin, Técnico |
| `POST /solicitacoes/{id}/comentar` · `/cancelar` | Comentário/anexo · cancelar | Envolvidos · solicitante |
| `GET/POST /admin/solicitacoes` | Catálogo, e-mails extras do Suprimentos, lembrete do gestor | Admin |
| Administração → Perfis e permissões | Criar perfil, áreas, poderes; ligar contas e setores do gestor | Admin |

## Segurança

- Toda ação exige login + CSRF; o link do e-mail do gestor só abre a solicitação (sem token de ação).
- Só o gestor escolhido (ou um Admin) autoriza; registra quem, quando e o IP. Tudo vai para a auditoria.
- Gestor com `ver_setor` só vê solicitações dos setores ligados à conta dele.

## Critérios de aceite

- [ ] Usuário pede um item do catálogo e um item livre
- [ ] A T.I recebe e-mail e aviso; valida e escolhe o gestor sugerido pelo setor
- [ ] O gestor recebe e-mail, abre o link, precisa entrar e só então autoriza
- [ ] O Suprimentos só recebe o e-mail depois da autorização; marca comprado + setor e, se quiser, fornecedor, valor, NF, previsão e anexos
- [ ] A T.I é avisada da compra e marca entregue; atalho cadastra o ativo com NF e valor; atalho do termo é opcional
- [ ] O solicitante recebe e-mail em cada passo
- [ ] Solicitações contam no Início e nos Relatórios, sem SLA
- [ ] O admin cria um perfil novo com áreas e poderes e o atribui a uma conta
- [ ] Gestor com `ver_setor` vê só as do próprio setor; quem não é o gestor do pedido não autoriza
