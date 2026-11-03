# Plano: transformar o Inventário em um sistema de chamados (com Active Directory no futuro)

Este documento é um **plano**, não código pronto. Ele descreve o caminho recomendado,
as opções para controlar os usuários que abrem chamados e como encaixar o Active Directory
mais tarde — do jeito mais simples que atende.

## A grande vantagem que já temos

O sistema **já sabe quem usa cada máquina** (último usuário, histórico de logins) e **toda a
configuração** dela. Então, quando alguém abrir um chamado, o sistema pode **anexar
automaticamente** a máquina da pessoa e o hardware/So dela. Isso é algo que o GLPI só faz com
muita configuração — aqui sai de graça.

## Modelo de dados proposto (tabela `chamados`)

- id, título, descrição
- **setor** e **solicitante** (quem abriu) — exatamente o que você pediu
- máquina vinculada (opcional, sugerida automaticamente pelo último login da pessoa)
- categoria (ex.: Hardware, Software, Rede, Acesso, Impressora)
- prioridade (Baixa, Média, Alta)
- status (Aberto, Em andamento, Aguardando, Resolvido, Fechado)
- responsável (técnico da TI)
- datas (aberto em, atualizado em, resolvido em) — para medir tempo de atendimento
- histórico de comentários/andamentos (tabela `chamado_mensagens`)

Com isso dá para ter: fila de chamados, filtro por setor/status/responsável, e relatórios
(quantos por setor, tempo médio de resolução) reaproveitando os gráficos que já fizemos.

## Como controlar os usuários que abrem chamados — 3 caminhos

### Opção A — Sem senha para o solicitante (mais simples, recomendado para começar)
Uma página pública `/abrir-chamado` onde a pessoa:
- escolhe o nome dela de uma lista (os usuários que o inventário **já conhece**) ou digita e-mail,
- escolhe o setor, a categoria e descreve o problema.

A TI vê tudo no painel e trata. Sem gerenciar senhas, sem integração. É o caminho mais rápido
para ter chamados funcionando. Risco: qualquer um na rede pode abrir em nome de outro — aceitável
internamente no início, e dá para exigir confirmação por e-mail depois.

### Opção B — Conta local para cada solicitante
Cada pessoa recebe usuário e senha no sistema. Mais controle, porém **mais trabalho** (criar,
resetar senha, desativar quando alguém sai). Só vale se vocês não forem usar o AD.

### Opção C — Login pelo Active Directory (a sua pretensão)
A pessoa entra com o **mesmo usuário e senha do Windows/domínio**. É o ideal a médio prazo.
Como funciona, do mais simples ao mais completo:

1. **LDAP "simple bind" (recomendado).** A pessoa digita usuário e senha; o servidor tenta se
   conectar ao controlador de domínio (DC) com essas credenciais. Se conectar, a senha está
   certa. Simples, sem instalar nada nas máquinas.
   - Usa LDAP sobre TLS (LDAPS, porta 636) para a senha não trafegar aberta.
   - Precisa de: endereço do DC, base de busca (ex.: `DC=empresa,DC=local`), e o atributo de
     login (`sAMAccountName`). Dá para puxar automaticamente o **setor** da pessoa do campo
     `department` do AD, e o nome do `displayName`.
   - No Go, usa-se a biblioteca `go-ldap` (dependência externa a adicionar).
2. **Papéis por grupo do AD.** Quem está no grupo "TI" entra como técnico (vê e trata todos os
   chamados); os demais entram como solicitantes (só os próprios). O servidor lê os grupos do
   usuário no AD.
3. **SSO total (login automático sem digitar senha), via Kerberos/IWA.** É o mais confortável,
   mas bem mais complexo de configurar (SPN, keytab). **Não recomendo no início** — o ganho não
   paga a complexidade. O LDAP bind do item 1 já resolve 95% do valor.

### Recomendação
Começar pela **Opção A** (chamados funcionando rápido, sem senha), e depois ligar a **Opção C
item 1 (LDAP bind) + item 2 (grupos)**. Pular a Opção B e o SSO Kerberos. Assim a TI usa o login
que já existe no domínio, sem criar senhas novas, e sem a complexidade do SSO.

## Roadmap sugerido

- **Fase A — Chamados básico:** modelo de dados, página de abertura (sem login), fila no painel,
  status/prioridade/responsável, vínculo automático da máquina pelo último login, relatórios.
- **Fase B — Autenticação AD (LDAP bind):** login do solicitante e do técnico pelo AD; setor
  preenchido automaticamente pelo `department`.
- **Fase C — Papéis e automações:** papéis por grupo do AD; notificação de novo chamado no
  Teams/Slack (reaproveitando o webhook); métricas de SLA.

## Pontos de atenção

- O AD exige que o servidor **enxergue o DC na rede** (porta 636). Se o servidor for para a nuvem,
  isso precisa de VPN ou um conector — vale decidir isso junto com a migração para a nuvem.
- Adicionar LDAP traz uma dependência externa (biblioteca `go-ldap`); é madura e estável.
- O e-mail para confirmação/aviso de chamado precisa de um servidor SMTP da empresa (a definir).

Quando você quiser, começamos pela Fase A e eu já deixo o gancho para o AD na Fase B.
