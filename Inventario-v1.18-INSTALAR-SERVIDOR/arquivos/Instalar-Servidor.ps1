# Inventário — INSTALA (ou reinstala) o servidor nesta máquina, perguntando IP e porta.
# Se já existir uma instalação, usa a mesma pasta e oferece MANTER o banco (token dos agentes,
# usuários, chamados). Remove a instalação anterior antes, com backup. Registro na Área de Trabalho.
$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'
$origem = Split-Path -Parent $MyInvocation.MyCommand.Path
$carimbo = Get-Date -Format 'yyyyMMdd-HHmmss'
$registro = Join-Path ([Environment]::GetFolderPath('Desktop')) "instalar-servidor-$carimbo.txt"
Start-Transcript -Path $registro -ErrorAction SilentlyContinue | Out-Null
$tarefa = '\Inventario\Servidor'

function Titulo([string]$t) { Write-Host ''; Write-Host "== $t ==" -ForegroundColor Cyan }
function Ok([string]$t) { Write-Host "   $t" -ForegroundColor Green }
function Info([string]$t) { Write-Host "   $t" }
function Aviso([string]$t) { Write-Host "   $t" -ForegroundColor Yellow }
function Perguntar([string]$p, [string]$padrao) { $r = Read-Host "   $p [$padrao]"; if ([string]::IsNullOrWhiteSpace($r)) { return $padrao }; return $r.Trim() }
function Fim([string]$t, [bool]$erro) {
    if ($erro) { Write-Host "   $t" -ForegroundColor Red } else { Write-Host "   $t" -ForegroundColor Green }
    Write-Host ''; Write-Host "Registro salvo em: $registro"
    Stop-Transcript -ErrorAction SilentlyContinue | Out-Null
    if ($erro) { exit 1 } else { exit 0 }
}
function Versao([int]$p, [int]$seg) { for ($i = 0; $i -lt $seg; $i++) { Start-Sleep 1; try { return (Invoke-RestMethod -TimeoutSec 3 "http://127.0.0.1:$p/saude").versao } catch { } }; return $null }

Titulo 'Instalar o servidor do Inventário'
$novo = Get-ChildItem -Path $origem -Filter 'Inventario_servidor_v*.exe' | Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not $novo) { Fim 'Não achei Inventario_servidor_v*.exe na pasta "arquivos" (descompacte o .zip inteiro).' $true }
Unblock-File $novo.FullName -ErrorAction SilentlyContinue
Info "Versão a instalar: $($novo.Name)"

# ---------- instalação existente ----------
Titulo 'Instalação atual'
$exeAtual = $null; $argumentos = ''
$xml = schtasks /Query /TN $tarefa /XML 2>$null
if ($LASTEXITCODE -eq 0 -and $xml) {
    $tx = [xml]($xml -join "`n")
    $exeAtual = ([string]$tx.Task.Actions.Exec.Command).Trim('"')
    $argumentos = [string]$tx.Task.Actions.Exec.Arguments
}
$procs = @(Get-CimInstance Win32_Process -Filter "Name='inventario-servidor.exe' OR Name LIKE 'Inventario_servidor%'" -ErrorAction SilentlyContinue)
foreach ($p in $procs) { Info "Rodando agora (PID $($p.ProcessId)): $($p.CommandLine)" }
if (-not $exeAtual) {
    # ignora cópias abertas por engano de dentro de pastas de kit (Inventario-v1.x-...)
    $p = $procs | Where-Object { $_.ExecutablePath -and $_.ExecutablePath -notlike "$origem*" -and $_.ExecutablePath -notmatch 'Inventario-v1\.' } | Select-Object -First 1
    if ($p) { $exeAtual = $p.ExecutablePath; $cl = [string]$p.CommandLine; $i = $cl.ToLower().IndexOf('.exe'); if ($i -ge 0) { $argumentos = $cl.Substring($i + 4).Trim().TrimStart('"').Trim() } }
}
$bancoAtual = $null; $portaAtual = $null
if ($argumentos -match '-porta[ =]+(\d+)') { $portaAtual = [int]$Matches[1] }
if ($argumentos -match '-banco[ =]+"([^"]+)"') { $bancoAtual = $Matches[1] } elseif ($argumentos -match '-banco[ =]+(\S+)') { $bancoAtual = $Matches[1] }
if (-not $bancoAtual -and $exeAtual) { $bancoAtual = Join-Path (Split-Path -Parent $exeAtual) 'inventario.db' }
if ($bancoAtual -and -not (Test-Path $bancoAtual)) { $bancoAtual = $null }
if (-not $bancoAtual) {
    foreach ($c in 'C:\InventarioServidor\inventario.db', 'C:\Inventario\inventario.db', 'C:\InventarioTeste\inventario.db') { if (Test-Path $c) { $bancoAtual = $c; break } }
}
if ($exeAtual) { Info "Executável: $exeAtual" } else { Info 'Nenhuma instalação registrada (tarefa \Inventario\Servidor não existe).' }
if ($bancoAtual) { Info "Banco: $bancoAtual ($([math]::Round((Get-Item $bancoAtual).Length/1MB,1)) MB)" }
if ($portaAtual) { Info "Porta: $portaAtual" }

# ---------- IP, porta, pasta ----------
Titulo 'Endereço do servidor'
$ips = @()
try { $ips = @(Get-NetIPConfiguration -ErrorAction Stop | Where-Object { $_.IPv4DefaultGateway -and $_.NetAdapter.Status -eq 'Up' } | ForEach-Object { $_.IPv4Address } | ForEach-Object { $_.IPAddress }) } catch { }
if ($ips.Count -eq 0) { $ips = @(Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } | ForEach-Object { $_.IPAddress }) }
if ($ips.Count -eq 0) { $ip = Read-Host '   Não detectei o IP. Digite o IP desta máquina' }
elseif ($ips.Count -eq 1) { $ip = Perguntar "IP detectado: $($ips[0]). Enter para usar, ou digite outro" $ips[0] }
else {
    Info 'Encontrei mais de um IP:'
    for ($i = 0; $i -lt $ips.Count; $i++) { Info "  [$($i+1)] $($ips[$i])" }
    $esc = Perguntar 'Qual usar? (número ou digite um IP)' '1'
    if ($esc -match '^\d+$' -and [int]$esc -ge 1 -and [int]$esc -le $ips.Count) { $ip = $ips[[int]$esc - 1] } else { $ip = $esc }
}
if ($ip -notmatch '^[A-Za-z0-9\.\-]+$') { Fim "Endereço inválido: $ip" $true }
$padraoPorta = if ($portaAtual) { "$portaAtual" } else { '8081' }
$porta = Perguntar 'Porta do servidor' $padraoPorta
if ($porta -notmatch '^\d+$' -or [int]$porta -lt 1 -or [int]$porta -gt 65535) { Fim 'Porta inválida.' $true }
$porta = [int]$porta
$url = "http://${ip}:$porta"

$pastaPadrao = if ($bancoAtual) { Split-Path -Parent $bancoAtual } elseif ($exeAtual) { Split-Path -Parent $exeAtual } else { 'C:\InventarioServidor' }
$pasta = Perguntar 'Pasta de instalação' $pastaPadrao
if ($pasta -like "$origem*") { Fim 'Não instale dentro da pasta do .zip; escolha outra pasta (ex.: C:\InventarioServidor).' $true }
$banco = Join-Path $pasta 'inventario.db'
$exe = Join-Path $pasta 'inventario-servidor.exe'

$manter = $false
if ($bancoAtual) {
    Write-Host ''
    Info "Já existe um banco com os dados atuais (token dos agentes, usuários, chamados, inventário)."
    $r = Perguntar '[M]anter esses dados, ou começar do [Z]ero?' 'M'
    if ($r.ToUpper() -eq 'Z') {
        $c = Read-Host '   Começar do ZERO? As máquinas vão parar de enviar até receberem o token novo. Digite ZERO para confirmar'
        if ($c -ne 'ZERO') { Fim 'Cancelado. Nada foi alterado.' $false }
    } else { $manter = $true }
}

Titulo 'Resumo'
Info "Endereço : $url"
Info "Pasta    : $pasta"
Info "Dados    : $(if ($manter) {"MANTER ($bancoAtual)"} elseif ($bancoAtual) {'COMEÇAR DO ZERO (o atual vai para backup)'} else {'novo'})"
$r = Perguntar 'Remover a instalação anterior e instalar? (S/N)' 'S'
if ($r.ToUpper() -ne 'S') { Fim 'Cancelado. Nada foi alterado.' $false }

# ---------- para tudo ----------
Titulo 'Parando o servidor'
schtasks /End /TN $tarefa 2>$null | Out-Null
Get-CimInstance Win32_Process -Filter "Name='inventario-servidor.exe' OR Name LIKE 'Inventario_servidor%'" -ErrorAction SilentlyContinue | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
Start-Sleep 2
$ocupada = Get-NetTCPConnection -LocalPort $porta -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
if ($ocupada) { $op = Get-Process -Id $ocupada.OwningProcess -ErrorAction SilentlyContinue; Fim "A porta $porta está em uso por $($op.Path). Libere ou escolha outra porta. Nada foi removido." $true }
Ok 'Parado.'

# ---------- backup ----------
Titulo 'Backup'
$bk = $null
if ($bancoAtual -or $exeAtual) {
    $baseBk = if ($bancoAtual) { Split-Path -Parent $bancoAtual } else { Split-Path -Parent $exeAtual }
    $bk = Join-Path $baseBk "backups\antes-de-reinstalar-$carimbo"
    New-Item -ItemType Directory -Force -Path $bk | Out-Null
    if ($bancoAtual) { Get-ChildItem -Path (Split-Path -Parent $bancoAtual) -Filter 'inventario.db*' | Copy-Item -Destination $bk -ErrorAction SilentlyContinue
        if (-not (Test-Path (Join-Path $bk 'inventario.db'))) { Fim "Falhou o backup do banco. Nada foi removido." $true } }
    if ($exeAtual -and (Test-Path $exeAtual)) { Copy-Item $exeAtual (Join-Path $bk 'inventario-servidor.exe') -ErrorAction SilentlyContinue }
    Ok "Backup em $bk"
} else { Info 'Nada para guardar.' }

# ---------- remove ----------
Titulo 'Removendo a instalação anterior'
schtasks /Delete /F /TN $tarefa 2>$null | Out-Null
& netsh advfirewall firewall delete rule name=InventarioServidor 2>&1 | Out-Null
if ($exeAtual -and (Test-Path $exeAtual)) { Remove-Item $exeAtual -Force -ErrorAction SilentlyContinue }
if ($exeAtual -and (Test-Path $exeAtual)) { Fim "Não consegui apagar $exeAtual (antivírus?). Backup em $bk." $true }
Ok 'Removida.'

# ---------- dados ----------
New-Item -ItemType Directory -Force -Path $pasta | Out-Null
if ($manter) {
    if ($bancoAtual -ne $banco) {
        Get-ChildItem -Path (Split-Path -Parent $bancoAtual) -Filter 'inventario.db*' | Copy-Item -Destination $pasta -Force
        Info "Banco copiado para $pasta"
    }
} elseif (Test-Path $banco) {
    Get-ChildItem -Path $pasta -Filter 'inventario.db*' | Remove-Item -Force   # já está no backup
    Info 'Banco anterior retirado (está no backup); o servidor cria um novo.'
}

# ---------- instala ----------
Titulo 'Instalando'
Copy-Item $novo.FullName $exe -Force
Unblock-File $exe -ErrorAction SilentlyContinue
$saida = & $exe -instalar -porta $porta -banco $banco -endereco $url 2>&1
$saida | ForEach-Object { Info "$_" }
$v = Versao $porta 40
if (-not $v) {
    Titulo 'O servidor não respondeu'
    $log = Join-Path $pasta 'servidor.log'
    if (Test-Path $log) { Get-Content $log -Tail 20 | ForEach-Object { Info $_ } }
    if ($bk -and (Test-Path (Join-Path $bk 'inventario-servidor.exe')) -and $exeAtual) {
        Aviso 'Voltando a versão anterior...'
        schtasks /End /TN $tarefa 2>$null | Out-Null
        Get-Process inventario-servidor -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
        Start-Sleep 2
        Copy-Item (Join-Path $bk 'inventario-servidor.exe') $exeAtual -Force
        $pa = if ($portaAtual) { $portaAtual } else { $porta }
        & $exeAtual -instalar -porta $pa -banco $bancoAtual 2>&1 | Out-Null
    }
    Fim 'Instalação falhou. Me mande o arquivo de registro.' $true
}

Titulo 'Pronto'
Ok "Servidor $v no ar: $url"
Info "Inicia sozinho com o Windows (tarefa $tarefa) e a porta $porta foi liberada no firewall."
try { if ((Invoke-WebRequest -UseBasicParsing -TimeoutSec 5 "$url/saude").StatusCode -eq 200) { Ok "Respondendo também pelo IP ($url)." } } catch { Aviso "Pelo IP $url não respondeu daqui — confira o firewall/antivírus." }
$log = Join-Path $pasta 'servidor.log'
if ($manter) { Info 'Dados mantidos: token dos agentes, usuários, chamados e inventário continuam iguais.' }
else {
    $tk = (Select-String -Path $log -Pattern 'Token dos agentes: (\S+)' -ErrorAction SilentlyContinue | Select-Object -Last 1)
    if ($tk) { Aviso "Banco NOVO. Token dos agentes: $($tk.Matches[0].Groups[1].Value)" }
    $sn = Join-Path $pasta 'senha-admin-inicial.txt'
    if (Test-Path $sn) { Aviso "Primeiro acesso: veja usuário e senha em $sn" }
}
Info 'No navegador: Ctrl+F5 no painel.'
Fim "Backup do que havia antes: $(if ($bk) {$bk} else {'(nada)'})" $false
