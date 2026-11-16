# SDD — Harvest 1.23: Termos de Responsabilidade

> Cópia de referência da SDD (16/11/2026, Daniel). A versão viva, editável e com os diagramas, está em:
> https://claude.ai/artifact/866RY2ghFRTRZ4zxh9gUcu
> Modelos de origem: `docs/termos/Termo_Entrega_Equipamento_Harvest_v1.docx` e `docs/termos/PSI_v4_Grupo_Ocupacional.docx`.

## Contexto e objetivo

A versão 1.23 traz o módulo **Termos**. Com ele o Harvest emite, colhe assinatura, guarda e devolve termos que o colaborador assina eletronicamente. O módulo começa com dois termos e permite que o admin crie outros sem mexer no código.

**Demanda recebida:** termos no formato do LuzIA RH, preenchidos com dados do equipamento, assinados por todos. Cada pessoa consulta os próprios termos e reassina nas revisões. O RH é avisado a cada assinatura para arquivar no LuzIA RH.

| Termo inicial | Abrangência | Origem | O que exige do Harvest |
| --- | --- | --- | --- |
| Termo de Entrega, Responsabilidade e Uso de Equipamento Corporativo (TRE-v1.0) | Por equipamento | `Termo_Entrega_Equipamento_Harvest_v1.docx` | Ficha do colaborador, dados de gestão do ativo, acessórios, checklist de segurança, estado com fotos, chamado vinculado, duplo aceite, evidências e quadro de devolução |
| Termo de Ciência e Responsabilidade sobre Monitoramento (PSI v4, Anexo II) | Por pessoa | `PSI_v4_Grupo_Ocupacional.docx` | Nome, CPF ou matrícula, setor, cargo, aceite e evidências |

O Termo de Entrega remete à PSI e ao Anexo II. Por isso a PSI completa fica disponível para leitura dentro do Harvest, como documento de referência ligado ao segundo aceite.

| Já existe no Harvest | Onde |
| --- | --- |
| Fabricante, modelo, serial, patrimônio, hostname, sistema, CPU, RAM, disco, MAC | `maquinas`, `interfaces` |
| Programa monitorado (WorkMonitor rodando ou não) | `monitor.go` |
| Chamados com anexos, linha do tempo e acompanhamento | `chamados*.go`, `chamado_anexos` |
| E-mail SMTP com registro de envios | `email.go`, `email_envios` |
| Perfis, sessões, CSRF, bloqueio de login e auditoria | `autenticacao.go`, `auditoria` |

## Escopo e decisões

O colaborador assina logado numa conta do Harvest criada pelo e-mail corporativo, com um código enviado por e-mail no momento do aceite. Cada tipo de termo diz se vale por pessoa ou por equipamento, e a devolução entra já nesta versão.

| # | Decisão | Escolha |
| --- | --- | --- |
| D1 | Relação termo × alvo | Cada tipo de termo define: por equipamento (Entrega) ou por pessoa (Anexo II) |
| D2 | Identidade | T.I cadastra o e-mail corporativo; o Harvest cria uma conta perfil Usuário e envia convite para definir a senha |
| D3 | E-mails aceitos | Só domínios corporativos, lista configurável (padrão `ocupacional.com.br`) |
| D4 | Máquina compartilhada | Um responsável por equipamento, escolhido pela T.I |
| D5 | Aviso ao RH | E-mail com PDF anexo; encaixe preparado para API do LuzIA no futuro |
| D6 | Revisão do termo | Ao publicar, a T.I marca se exige reassinatura ou se é só correção |
| D7 | E-mail do RH | Configurável, vários endereços |
| D8 | Termos novos | Editor de termos no Harvest: texto, tabelas e campos declarados, com quem preenche cada um |
| D9 | Segundo fator | Código de 6 dígitos por e-mail na hora do aceite |
| D10 | Dados pessoais (CPF, matrícula, cargo, unidade, empresa, gestor, vínculo, regime, telefone) | O colaborador preenche no primeiro acesso; a T.I valida a ficha antes de emitir |
| D11 | Checklist de segurança | Agente 1.22 detecta BitLocker, antivírus, admins locais e atualizações; a T.I confirma. MFA e OneDrive são manuais |
| D12 | Fotos, chamado e divergência | Emitir um termo de entrega abre um chamado "Entrega de equipamento" com as fotos; a divergência de 48 h é registrada nele |
| D13 | Ativos e gestão | A ficha da máquina ganha tipo, condição, NF, data de aquisição e valor; dá para cadastrar um ativo manual sem agente (celular, monitor) |
| D14 | Devolução | Entra na 1.23: vistoria, fotos, encerramento técnico e confirmação do colaborador |
| D15 | Máquina de onde a pessoa assina | IP (rede local) + usuário logado no Windows (evento `quem`), com grau de confiança; opcional exigir a própria máquina |

**Dentro da 1.23:** editor de termos com versões, contas por convite, ficha do colaborador, gestão do ativo e ativos manuais, emissão com chamado, preenchimento da T.I, assinatura com código por e-mail, identificação da máquina, PDF, aviso ao RH, Meus termos, reassinatura, devolução e painel de pendências.

**Fora da 1.23:** API com o LuzIA RH (só o encaixe), assinatura ICP-Brasil, login Microsoft, sincronização de dados pessoais a partir do RH, e contrato de comodato para PJ (o Termo de Entrega não é emitido para vínculo PJ).

## Fluxos

A T.I convida e valida, o colaborador completa a ficha e aceita com código por e-mail, e o Harvest gera o PDF e avisa o RH. A devolução fecha o mesmo termo e o mesmo chamado. O Anexo II, que vale por pessoa, segue o mesmo caminho, mas sem chamado, checklist ou devolução.

```
T.I:          Convida (e-mail corporativo) → Valida a ficha e emite → Checklist, estado e 4 fotos ......→ Vistoria e encerramento
Colaborador:  Cria a senha e preenche a ficha ─┘                     → Aceita com código por e-mail     → Confirma ou registra divergência
Harvest:                                   Abre o chamado de entrega → Grava evidências e gera o PDF   → Envia o PDF a cada etapa
RH:                                                                                                    → Arquiva no LuzIA RH
```

Cada PDF também vai para o colaborador. Uma revisão publicada com reassinatura, ou a troca de responsável, muda o termo para `substituido` e emite um novo.

## Editor de termos

O admin cria e revisa tipos de termo em Termos → Modelos. Cada tipo junta um texto com marcação simples e uma lista de campos declarados, e cada campo diz quem o preenche. Os dois termos atuais já vêm cadastrados nesse formato.

**Configuração de um tipo de termo:**

| Propriedade | Exemplo (Termo de Entrega) |
| --- | --- |
| Nome e sigla da versão | Termo de Entrega, Responsabilidade e Uso · `TRE-v1.0` |
| Abrangência | Por equipamento (ou por pessoa) |
| Vínculos permitidos | CLT, estágio, aprendiz, temporário (PJ bloqueado) |
| Abre chamado | Sim, categoria "Entrega de equipamento" |
| Tem devolução | Sim (habilita o quadro de devolução) |
| Aceites | Lista de caixas obrigatórias, cada uma podendo apontar para um documento de referência (PSI) |
| Exige código por e-mail | Sim |
| Envia ao RH | Sim |
| Uso interno | Texto que aparece só no painel, nunca no PDF do colaborador (o "Quadro de validação" do modelo) |
| Exigir assinatura na própria máquina | Opcional (padrão desligado); a T.I pode liberar um termo com motivo |

**Blocos do texto:** títulos, parágrafos, itens numerados (6.1, 6.2…), tabelas de duas ou quatro colunas, destaque e quebra de página. Os campos entram como `{campo}` em qualquer lugar do texto ou da tabela.

**Tipos de campo:**

| Tipo | Uso no Termo de Entrega |
| --- | --- |
| Automático | Nome, CPF, serial, patrimônio, MAC, nº do chamado, nº do termo, data |
| Texto | Local de uso, observações sobre o estado, pendências apuradas |
| Opção | Condição (novo / usado / reutilizado), motivo da devolução |
| Checklist sim/não | Configuração de segurança, encerramento técnico |
| Checklist com colunas | Acessórios (entregue / não / série), estado (perfeito / riscado / danificado / não se aplica) |
| Fotos | Registro fotográfico, com quantidade mínima (4 na entrega) |
| Data e hora | Entrega, devolução |

**Quem preenche:** `auto` (Harvest), `ti` (antes de enviar ao colaborador), `colaborador` (na hora de assinar) ou `devolucao` (a T.I no encerramento). Um campo automático vazio impede o envio e mostra o que falta, por exemplo "patrimônio não cadastrado".

**Versões:** salvar cria um rascunho; publicar congela o texto e os campos como uma versão nova. Termos já assinados continuam ligados à versão que a pessoa leu. A prévia mostra o PDF com uma máquina e uma pessoa reais antes de publicar.

## Modelo de dados

São oito tabelas novas e colunas novas em `admins` e `maquinas`. Tudo é criado pelas migrações de `banco.go`, sem alteração destrutiva.

| Tabela | Colunas principais | Para quê |
| --- | --- | --- |
| `termo_tipos` | id, nome, abrangencia (pessoa / equipamento), vinculos_permitidos, abre_chamado, tem_devolucao, exige_codigo, exige_propria_maquina, envia_rh, ativo | O tipo de termo e suas regras |
| `termo_versoes` | id, tipo_id, sigla (`TRE-v1.0`), corpo, campos_json, aceites_json, uso_interno, exige_reassinatura, situacao (rascunho / publicada), publicado_em, publicado_por | Texto e campos congelados de cada versão |
| `termos` | id, numero (sequencial), tipo_id, versao_id, admin_id (colaborador), maquina_id ou ativo_id (se for por equipamento), chamado_id, situacao, valores_json, criado_por, validado_ti_por, validado_ti_em, assinado_em, substituido_por | Uma instância do termo, com os valores preenchidos |
| `termo_assinaturas` | termo_id, etapa (entrega / devolucao), aceites_json, assinado_em, ip, user_agent, maquina_assinatura_id, maquina_grau (confirmada / provavel / nao_identificada), maquinas_candidatas, metodo (senha + código por e-mail), email_codigo, texto_final, hash_sha256, codigo_verificacao, pdf (BLOB) | A prova, gravada uma vez e nunca alterada |
| `termo_codigos` | admin_id, termo_id, codigo_hash, expira, tentativas | Código de 6 dígitos (10 min, até 5 tentativas) |
| `termo_envios` | termo_id, destino (pessoa / rh / luzia_api), para, situacao, erro, tentado_em | Fila de avisos, com nova tentativa |
| `ativos_manuais` | id, tipo, fabricante, modelo, serial, patrimonio, imei, linha, observacoes | Celular, monitor e outros sem agente |
| `empresas` | id, razao_social, cnpj, unidades_json | Empregadoras do grupo (mais de um CNPJ) |

**Novas colunas em `admins` (ficha do colaborador):** login_rede, cpf, matricula, cargo, setor, unidade, empresa_id, gestor, vinculo, regime, telefone, ficha_preenchida_em, ficha_validada_por, ficha_validada_em, convite_token_hash, convite_expira, ativo.

**Novas colunas em `maquinas` (gestão do ativo):** tipo (desktop / notebook), condicao, nf_numero, nf_data, valor_referencia. O agente 1.22 também passa a preencher: bitlocker, antivirus, admins_locais e ultima_atualizacao_so.

**Situações do termo:** `rascunho` (T.I preenchendo) → `aguardando` (enviado ao colaborador) → `assinado` → `em_devolucao` → `encerrado`. Fora dessa linha: `substituido` (revisão com reassinatura ou troca de responsável) e `cancelado`.

Os valores automáticos são copiados para `valores_json` na emissão. Se o cadastro mudar depois, o termo assinado continua mostrando o que a pessoa leu.

## Telas e rotas

Tudo exige login, menos a página de convite e a de verificação. As permissões seguem os papéis de `autenticacao.go`.

| Rota | Tela | Quem acessa |
| --- | --- | --- |
| `GET /termos` | Painel: aguardando, assinados, em devolução, desatualizados, fichas a validar, máquinas sem termo | Admin, Técnico |
| `GET/POST /termos/novo` | Emitir: escolher o tipo, o colaborador e o equipamento; abre o chamado | Admin, Técnico |
| `GET/POST /termos/{id}/preencher` | Preenchimento da T.I: acessórios, checklist (pré-marcado pelo agente), estado, fotos e enviar ao colaborador | Admin, Técnico |
| `GET /termos/{id}` | Detalhe: valores, evidências, PDF, envios, reenviar e cancelar | Admin, Técnico |
| `GET/POST /termos/{id}/devolucao` | Vistoria, fotos, encerramento técnico e envio ao colaborador | Admin, Técnico |
| `GET/POST /termos/modelos` | Editor de termos: tipos, versões, campos, prévia e publicação | Admin |
| `GET/POST /termos/documentos` | Documentos de referência (PSI) com versão | Admin |
| `GET/POST /admin/termos` | Domínios, e-mails do RH, empresas e unidades, prazo do convite, lembretes | Admin |
| `GET/POST /admin/colaboradores` | Convidar pelo e-mail, ver e validar fichas (inclui login de rede), desativar conta | Admin, Técnico |
| `GET/POST /convite/{token}` | Definir a senha | Pública, uso único |
| `GET/POST /minha-ficha` | Completar ou corrigir CPF, matrícula, cargo, unidade, empresa, gestor, vínculo, regime e telefone | Pessoa logada |
| `GET /meus-termos` | Pendências no topo; depois os assinados e encerrados com o PDF | Pessoa logada |
| `GET/POST /meus-termos/{id}/assinar` | Ler, preencher os campos dela, marcar os aceites, pedir e digitar o código | O colaborador do termo |
| `GET/POST /meus-termos/{id}/devolucao` | Confirmar a vistoria ou registrar divergência | O colaborador do termo |
| `GET /meus-termos/{id}/pdf` | Baixar o PDF | O colaborador, Admin, Técnico |
| `GET /verificar/{codigo}` | Conferir a autenticidade pelo código do PDF | Pública (só válido, data e versão) |

**Ficha da máquina:** ganha uma seção Gestão (tipo, condição, NF, valor), outra de Segurança (BitLocker, antivírus, admins locais) e o cartão Termo, com a situação e os botões Emitir ou Devolução. O inventário filtra "máquinas sem termo assinado".

**Chamado de entrega:** mostra o termo vinculado e as fotos. O colaborador pode registrar divergência nele em até 48 h após a assinatura; o prazo aparece na linha do tempo.

**Colaborador com perfil Usuário:** ao entrar, se a ficha estiver incompleta, cai em Minha ficha. Se houver termo pendente, cai em Meus termos. O menu mostra Chamados e Meus termos.

**Início:** "Termos: X de Y assinados" e, em "Pedem atenção", termos aguardando há mais de N dias e fichas a validar.

## Assinatura, prova e segurança

O aceite usa dois fatores: a senha da conta e um código de 6 dígitos enviado ao e-mail corporativo na hora. O registro de evidências segue o quadro "Registro de evidências do aceite" do Termo de Entrega, campo por campo, e cita o art. 10, § 2º, da MP 2.200-2/2001 e a Lei 14.063/2020, como o próprio termo.

**Passos do aceite:** a pessoa lê o termo preenchido, completa os campos dela (local de uso, se o regime for híbrido ou teletrabalho) e marca cada aceite. Depois clica em "Enviar código", digita o código recebido e confirma.

| Evidência do termo | Como o Harvest preenche |
| --- | --- |
| Aceito por (nome / login) | Conta logada e nome da ficha |
| Autenticação | "Senha + código por e-mail para fulano@ocupacional.com.br" |
| Data e hora do aceite | Relógio do servidor, fuso de Brasília (UTC-3) |
| Endereço IP e dispositivo / navegador | Requisição do aceite (IP real atrás do proxy, se houver) |
| Assinado na máquina | Máquina identificada e grau (ver seção seguinte) |
| Código de verificação | Derivado do hash, conferido em `/verificar/{codigo}` |
| Hash SHA-256 do documento | Calculado sobre o texto final e os valores, antes do PDF |
| E-mail de envio da cópia | E-mail corporativo da conta |
| Entregue por e data da validação T.I | Técnico que enviou o termo e a hora do envio |

**PDF:** gerado em Go puro (pacote novo `internal/pdf`), sem dependência externa, com acentuação, tabelas, caixas marcadas e o logo da Marca. As fotos ficam no chamado e o PDF cita a quantidade. O texto de uso interno nunca entra no PDF do colaborador.

**Segurança:**

- convite de uso único com validade de 7 dias; o banco guarda só o hash do token;
- código de 6 dígitos válido por 10 minutos, até 5 tentativas e no máximo 3 envios por hora;
- só o colaborador do termo assina; formulários com CSRF; o bloqueio de login atual continua valendo;
- emitir, preencher, enviar, assinar, devolver, cancelar e publicar versão vão para a auditoria;
- HTTPS obrigatório para o módulo. O termo cita `harvest-ocp.up.railway.app`; se a produção estiver no Railway, ele já tem HTTPS. O servidor local na 8081 precisa de certificado ou proxy.

**LGPD:** o tratamento segue o item 11 do Termo de Entrega. Os termos e as evidências ficam durante o vínculo e até 5 anos depois, e são vistos só pela T.I, pelo RH e pela Diretoria. Pela minimização do modelo, o Harvest não pede cópia de documento, foto do colaborador, dados bancários nem endereço residencial. A ficha pede só o local de uso e só em teletrabalho ou híbrido. Uma conta desligada é desativada, nunca apagada antes do prazo.

## Identificação da máquina na assinatura

No aceite, o Harvest registra de qual máquina a pessoa assinou, cruzando o IP com o usuário logado no Windows. Isso não muda a máquina do termo: ela continua sendo a escolhida pela T.I na emissão, e é por ela que o inventário mostra "Termo assinado".

**O que já existe:** o agente envia o usuário da tela (`usuario_atual`) e as sessões abertas, inclusive RDP (`usuarios_sessao`). O envio acontece a cada hora, no boot e no logon. O servidor guarda cada login de rede e as máquinas onde ele entrou (tabelas `usuarios` e `logins`). O canal SSE já pergunta e recebe resposta na hora (evento `teste`). `MaquinaPorIP` já reconhece a máquina pelo IP na rede local.

**O que muda:**

1. **Login de rede na ficha (servidor).** A conta do Harvest ganha o campo `login_rede`, ligado à tabela `usuarios`. O Harvest sugere o vínculo quando a parte do e-mail antes do @ bate com um login do inventário, e a T.I confirma ao validar a ficha. Sem o vínculo, a máquina fica "não identificada".
2. **Evento `quem` (servidor + agente 1.22).** Ao clicar em "Enviar código", o servidor pergunta pelo SSE a todas as máquinas conectadas quem está logado agora, e espera até 2 segundos. Cada agente responde com o hostname e os usuários das sessões. Agentes antigos, ou máquinas sem SSE, entram pelo último relatório, se tiver menos de 2 horas.
3. **IP.** Com o servidor na rede local, `MaquinaPorIP` aponta a máquina do navegador. Na nuvem (Railway) o IP é o público do escritório, igual para todos, e não é usado.

| Situação | Grau registrado no termo |
| --- | --- |
| IP e usuário logado apontam a mesma máquina | **Confirmada** |
| Só o usuário logado bate, numa única máquina | **Provável** |
| Logado em mais de uma máquina, nenhuma bate, ou sem login de rede | **Não identificada** (lista as candidatas) |

**No termo e no painel:** o quadro de evidências ganha "Assinado na máquina: OCP-123 (confirmada)". Se a máquina de onde a pessoa assinou for diferente da máquina do termo, o termo e o painel mostram um alerta, sem bloquear.

**Opção no tipo de termo, "Exigir assinatura na própria máquina":** o aceite só é liberado com grau Confirmada ou Provável na máquina do termo. Isso prova que a pessoa recebeu e ligou o equipamento. O padrão é desligado, e a T.I pode liberar um termo específico com motivo registrado na auditoria (pessoa sem acesso à máquina, por exemplo).

**Limite:** o usuário logado prova que a pessoa estava logada naquela máquina no momento do aceite. Ele não prova que o navegador estava nela. Por isso o grau fica registrado, e não só o nome da máquina.

## Devolução

A devolução fecha o mesmo termo e o mesmo chamado, como pede o "Quadro de devolução" do modelo. Ela só vale para tipos de termo com devolução ligada.

1. A T.I abre a devolução pela ficha da máquina ou pelo termo, com o motivo: desligamento, troca ou outro. O termo passa a `em_devolucao`.
2. A T.I faz a vistoria comparando com a entrega: cada componente marcado como igual, desgaste natural, dano novo ou faltante. Anexa as fotos e registra pendências e valor (item 7).
3. A T.I marca o encerramento técnico: acessos bloqueados, backup conferido, formatação segura e ativo atualizado.
4. O colaborador recebe e-mail e, em Meus termos, confirma o resultado com código por e-mail ou registra divergência, que vai para o chamado.
5. Com a confirmação, o Harvest gera o PDF final (entrega e devolução), envia ao RH e à pessoa, e o termo vira `encerrado`. A máquina fica sem responsável.

Se o colaborador já tiver saído e não tiver mais acesso, a T.I encerra com o motivo "sem confirmação do colaborador", e isso fica registrado no PDF.

## Mudanças no agente (1.22.0)

O agente ganha uma versão pequena para pré-marcar o checklist de segurança e responder ao evento `quem`. O resto do termo usa o que ele já coleta.

| Item do checklist | Como o agente detecta | Na falta do dado |
| --- | --- | --- |
| Criptografia do disco (BitLocker) | `Win32_EncryptableVolume`, situação da proteção no volume do sistema | T.I marca |
| Antivírus ativo e atualizado | `root/SecurityCenter2` → `AntiVirusProduct` (nome e estado) | T.I marca (servidores não têm SecurityCenter2) |
| Agente de inventário | Sempre sim, porque é ele quem informa | — |
| WorkMonitor instalado | Módulo Programa monitorado, que já existe | T.I marca |
| Usuário sem privilégio de administrador | Membros do grupo Administradores local comparados ao responsável | T.I marca |
| Sistema operacional atualizado | Data do último hotfix instalado (`Win32_QuickFixEngineering`) | T.I marca |
| MFA ativo na conta e backup no OneDrive | Fora do alcance do agente | Sempre manual |

O agente 1.22 também responde ao evento `quem` (hostname e usuários das sessões, na hora), usado para identificar a máquina na assinatura. Implementação: novo `case "quem"` em `tratarEvento` (`cmd/agente/eventos.go`), no mesmo molde do `teste`.

Antes de liberar, é preciso conferir se o Bitdefender ATC não passa a reagir às consultas novas, como já aconteceu com a coleta (pendência do CONTEXTO).

## E-mail e integração com o RH

Todo o módulo depende do e-mail: convite, código do aceite, cópia para o colaborador e aviso ao RH. Os e-mails saem pelo SMTP dos chamados, que passa a aceitar anexo e ganha uma fila com nova tentativa.

| E-mail | Para quem | Quando | Anexo |
| --- | --- | --- | --- |
| Convite | Colaborador | Cadastro da conta | — |
| Termo para assinar | Colaborador | Envio pela T.I, reenvio e revisão com reassinatura | — (link para Meus termos) |
| Código de verificação | Colaborador | Ao pedir o código no aceite | — (sai na hora, fora da fila) |
| Lembrete | Colaborador | A cada N dias enquanto estiver aguardando (padrão 3) | — |
| Cópia do termo assinado | Colaborador | Na assinatura | PDF |
| Termo assinado | RH | Na assinatura | PDF |
| Resultado da vistoria | Colaborador | Devolução enviada pela T.I | — |
| Termo encerrado | RH e colaborador | Devolução confirmada | PDF final |

**Mudanças em `email.go`:** `enviarEmail` passa a montar `multipart/mixed` quando houver anexo, mantendo o `multipart/alternative` de hoje dentro dele. Cada envio fica em `termo_envios` e em "Últimos envios". Se o SMTP falhar, a fila tenta de novo a cada 15 minutos, até 5 vezes, e depois mostra o erro com o botão Reenviar. O código de verificação não passa pela fila: se falhar, a tela avisa na hora.

**Encaixe para o LuzIA RH:** a fila chama uma interface `DestinoTermo`. Hoje há uma implementação, `destinoEmailRH`; uma futura `destinoLuziaAPI` entra sem mexer na assinatura, se o fornecedor oferecer API. O assunto padronizado ajuda o arquivamento manual: `[Termo assinado] TRE-v1.0 · Nº 0042 · Nome · PATRIMÔNIO`.

## Plano de entrega (roadmap) e aceite

Uma única liberação em produção: servidor 1.23.0 com agente 1.22.0, no kit `Inventario-vX-INSTALAR-SERVIDOR.zip` de sempre. As fases 1 a 4 são internas e testadas na máquina de teste.

| Fase | O que entra |
| --- | --- |
| 0 · Pré-requisitos | SMTP enviando · produção definida (Railway ou local com HTTPS) · empresas e CNPJs · jurídico valida o item 7 e a assinatura eletrônica |
| ◆ Portão | E-mail testado e jurídico de acordo |
| 1 · Base | Módulo Termos · contas por convite · Minha ficha com login de rede · validação da ficha pela T.I · empresas e unidades · e-mail com anexo |
| 2 · Editor e PDF | Editor de termos com versões e prévia · PSI como referência · gerador de PDF · Termo de Entrega e Anexo II cadastrados |
| 3 · Entrega e assinatura | Gestão do ativo · agente 1.22 (checklist e evento `quem`) · chamado · aceite com código · máquina identificada · PDF ao RH · Meus termos |
| 4 · Devolução e painel | Vistoria, encerramento e confirmação do colaborador · PDF final · painel de pendências · lembretes · `/verificar` · cartão no Início |
| ◆ Portão | Testes na máquina de teste e Bitdefender sem alerta |
| 5 · Piloto | A T.I assina os dois termos · o RH confere o PDF e o e-mail · o jurídico revisa um PDF real |
| ◆ Portão | RH e jurídico aprovam |
| 6 · Implantação | Servidor 1.23.0 e agente 1.22.0 no kit · convites por setor · Anexo II para todos · Termo de Entrega máquina a máquina |

A fase 0 pode correr em paralelo com o início da fase 1, mas a fase 3 não começa sem o e-mail funcionando.

**Testes automatizados (Go):** campos do editor e validação de campos obrigatórios, recusa de domínio, convite expirado ou já usado, código errado, expirado e acima do limite, hash estável, PDF válido com acentos e tabelas, permissão (ninguém assina o termo de outro), bloqueio de vínculo PJ, fila de e-mail com falha e nova tentativa, transições de situação do termo, grau de identificação da máquina e detecção do agente (BitLocker e antivírus com saídas de exemplo).

**Critérios de aceite:**

- [ ] Cadastro com e-mail fora dos domínios aceitos é recusado
- [ ] Convite → senha → Minha ficha → T.I valida, sem ajuda da T.I no caminho
- [ ] Admin cria um terceiro tipo de termo pelo editor e o publica sem mudar código
- [ ] O Termo de Entrega sai com os dados da máquina e da pessoa preenchidos e o checklist pré-marcado
- [ ] O envio exige o mínimo de 4 fotos no chamado
- [ ] O aceite sem o código por e-mail é recusado
- [ ] Ao assinar, o RH recebe o PDF em até 1 minuto (SMTP funcionando)
- [ ] O PDF reproduz o quadro de evidências do modelo e passa em `/verificar`
- [ ] Publicar uma versão com reassinatura deixa os termos aguardando e reenvia o aviso
- [ ] A devolução fecha o mesmo termo e o mesmo chamado e gera o PDF final
- [ ] Desligar o módulo esconde tudo e mantém os dados
- [ ] Ao assinar, o termo registra a máquina e o grau (confirmada, provável ou não identificada), e alerta se for outra máquina
- [ ] Com "Exigir assinatura na própria máquina" ligado, o aceite em outra máquina é recusado

## Riscos e dependências

O escopo cresceu com a devolução, o editor e a ficha do colaborador; esta versão é maior que o módulo de chamados. O e-mail continua sendo a dependência que trava tudo.

| Risco | Impacto | O que fazer |
| --- | --- | --- |
| SMTP AUTH bloqueado no M365 (suspeita da 1.10.1) | Sem convite, sem código de aceite, sem aviso ao RH | Confirmar em "Últimos envios" antes da fase 1 |
| Colaborador preenche empresa, vínculo e gestor | CNPJ errado invalida o desconto do item 7.3; vínculo PJ assinando reforça vínculo empregatício | Empresa e unidade escolhidas de lista; a T.I valida a ficha antes de emitir; o tipo de termo bloqueia vínculos não permitidos |
| Item 7 (desconto em folha) sem aval do jurídico | Cobrança contestada | O próprio modelo pede validação antes do primeiro aceite; o piloto só começa depois dela |
| Painel local em HTTP | Senha e código trafegam sem criptografia | Usar a instância do Railway (HTTPS) ou pôr certificado no servidor local; o módulo recusa ligar sem HTTPS |
| Consultas novas do agente disparam o Bitdefender | Agente posto em quarentena | Testar a 1.22 na máquina de teste com o GravityZone ativo antes de liberar o auto-update |
| Fotos com dados de pacientes (tela ligada) | Dado sensível no chamado | Aviso na tela de fotos, como o modelo pede |
| Editor de termos complexo demais | Atraso e erro do admin | Os dois termos vêm cadastrados; a prévia em PDF é obrigatória antes de publicar |
| LuzIA sem API | Sem integração automática | O e-mail com PDF continua sendo o caminho |

**Pendências com o Daniel e a empresa:**

- [ ] Confirmar se o SMTP está enviando hoje
- [ ] Confirmar se a produção será o Railway (`harvest-ocp.up.railway.app`, citado no termo) ou o servidor local
- [ ] Jurídico validar o item 7 (desconto) e a assinatura eletrônica com código por e-mail
- [ ] Fechar os prazos marcados no modelo: devolução em 5 dias úteis e afastamento acima de 30 dias
- [ ] Definir quem faz a orientação ergonômica do item 9.2 (sugestão do modelo: material anexado ao chamado)
- [ ] Preencher o e-mail do canal de privacidade que está em branco no Anexo II (usar o mesmo canal do item 11.3 do Termo de Entrega)
- [ ] Informar as empresas do grupo (razão social, CNPJ) e as unidades
- [ ] Decidir se o Anexo I da PSI (Termo de Responsabilidade geral) também entra como termo por pessoa
- [ ] Perguntar ao fornecedor do LuzIA se existe API para receber documentos
