# Migrar o servidor para a nuvem (sem parar o sistema)

O servidor atual (local) **continua no ar** durante toda a migração. As máquinas passam para o novo
uma a uma, e só depois que a última passar o servidor antigo é desligado.

Pré-requisitos
- Servidor **novo** (nuvem) e **antigo** na versão **1.17 ou mais nova**.
- O endereço novo (DNS, ex.: `https://inventario.ocupacional.com.br`) precisa ser **alcançável a partir
  das máquinas da rede** (firewall/Omada liberando a porta escolhida).

## 1. Instalar o servidor novo
- Windows: kit `INSTALAR-SERVIDOR.bat` → informe IP/porta (o endereço que as máquinas vão usar).
- Linux: veja "Servidor em Linux" no LEIA-ME (systemd, `Restart=always`).

## 2. Levar o banco (dados, usuários, token dos agentes)
1. No **antigo**: Administração → Backup → **Baixar backup do banco** (`inventario_backup_<data>.db`).
2. No **novo**: Administração → **Importar banco de outro servidor** → envie o arquivo + sua senha.
   O banco é conferido, o atual vai para `backups\antes-de-importar-<data>` e o servidor reinicia.
3. Entre no novo com o **mesmo usuário e senha do antigo**. Confira máquinas, chamados e ramais.
   O endereço do novo é mantido; a configuração de migração que veio do antigo é limpa.

> Faça os passos 2 e 3 logo antes do 4: chamados abertos no antigo entre o backup e a autorização da
> migração ficam só no antigo.

## 3. Migrar as máquinas
No **antigo**: Administração → **Migrar os agentes para outro servidor**
1. Destino = endereço do novo; token = o mesmo (o banco foi copiado, então é igual).
2. (Opcional) **Canário**: alguns hostnames para testar primeiro.
3. **Autorizar** com sua senha.

A partir daí, no antigo:
- máquinas conectadas em tempo real (serviço 1.12+) recebem a ordem **na hora**;
- as demais, no próximo envio (até 1 h, no logon ou ao ligar);
- cada agente só troca de servidor **depois de confirmar que o novo responde** (senão tenta de novo);
- a página **Abrir chamado / Acompanhar** do antigo redireciona para o novo;
- o painel mostra a faixa "Migração em andamento".

Acompanhe: no antigo, Administração → Migrar (quantas faltam); no novo, Administração → **Conexões**.

## 4. Desligar o antigo
Quando nenhuma máquina mais estiver falando com o antigo:
1. Baixe um último backup do antigo (por garantia).
2. No antigo, como administrador: `inventario-servidor.exe -desinstalar` e apague a pasta.
3. Atualize atalhos/links (página de chamados) para o endereço novo.
