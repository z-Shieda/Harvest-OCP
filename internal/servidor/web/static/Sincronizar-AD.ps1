# Harvest - Sincronizar-AD.ps1
# Envia ao Harvest a lista de computadores e usuarios do Active Directory (com o ULTIMO LOGON
# real, consultado em cada controlador de dominio), para o comparativo "AD x maquinas com o
# agente" e a planilha de usuarios ativos.
#
# Rode no controlador de dominio (ou numa maquina com RSAT), PowerShell como administrador:
#   powershell -ExecutionPolicy Bypass -File .\Sincronizar-AD.ps1            envia agora
#   powershell -ExecutionPolicy Bypass -File .\Sincronizar-AD.ps1 -Agendar   envia e agenda todo dia 7h
#   powershell -ExecutionPolicy Bypass -File .\Sincronizar-AD.ps1 -Arquivo C:\Temp\ad-harvest.json   so gera o arquivo
#
# So LE o AD. Nao altera nada no dominio. Nao envia senhas nem hashes.
param(
    [string]$Servidor = '',
    [string]$Token = '',
    [string]$Arquivo = '',
    [switch]$Agendar
)
$ErrorActionPreference = 'Stop'
$log = Join-Path $env:ProgramData 'Harvest\sincronizar-ad.log'
New-Item -ItemType Directory -Force -Path (Split-Path $log) | Out-Null
function Log($t) { $l = "$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') $t"; Write-Host $l; Add-Content -Path $log -Value $l -Encoding UTF8 }

try { Import-Module ActiveDirectory } catch { Log 'ERRO: modulo ActiveDirectory ausente (rode no DC ou instale o RSAT).'; exit 1 }

function DataAD($ft) {
    if ($null -eq $ft -or [int64]$ft -le 0 -or [int64]$ft -eq [int64]::MaxValue) { return '' }
    return [DateTime]::FromFileTimeUtc([int64]$ft).ToString('yyyy-MM-ddTHH:mm:ssZ')
}
function OU($dn) { if (-not $dn) { return '' }; $p = $dn -split '(?<!\\),', 2; if ($p.Count -gt 1) { return $p[1] } else { return '' } }

$dominio = (Get-ADDomain).DNSRoot
$dcs = @(Get-ADDomainController -Filter * | Select-Object -ExpandProperty HostName)
Log "Lendo o AD de $dominio ($($dcs.Count) controlador(es): $($dcs -join ', '))"

# lastLogon NAO replica entre DCs: pega o maior valor de cada DC (e o lastLogonTimestamp)
$ultPC = @{}; $ultUs = @{}
foreach ($dc in $dcs) {
    try {
        Get-ADComputer -Server $dc -Filter * -Properties lastLogon, lastLogonTimestamp | ForEach-Object {
            $v = [Math]::Max([int64]($_.lastLogon), [int64]($_.lastLogonTimestamp))
            if (-not $ultPC.ContainsKey($_.SamAccountName) -or $v -gt $ultPC[$_.SamAccountName]) { $ultPC[$_.SamAccountName] = $v }
        }
        Get-ADUser -Server $dc -Filter * -Properties lastLogon, lastLogonTimestamp | ForEach-Object {
            $v = [Math]::Max([int64]($_.lastLogon), [int64]($_.lastLogonTimestamp))
            if (-not $ultUs.ContainsKey($_.SamAccountName) -or $v -gt $ultUs[$_.SamAccountName]) { $ultUs[$_.SamAccountName] = $v }
        }
    } catch { Log "aviso: nao consegui ler $dc ($($_.Exception.Message)) - usando os outros" }
}

$pcs = @(Get-ADComputer -Filter * -Properties OperatingSystem, DNSHostName, Enabled, whenCreated, DistinguishedName | ForEach-Object {
    [ordered]@{
        nome = $_.Name; dns = [string]$_.DNSHostName; so = [string]$_.OperatingSystem; ou = OU $_.DistinguishedName
        ativo = [bool]$_.Enabled; ultimo_logon = DataAD $ultPC[$_.SamAccountName]
        criado = if ($_.whenCreated) { $_.whenCreated.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ') } else { '' }
    }
})
$us = @(Get-ADUser -Filter * -Properties DisplayName, mail, Department, Title, Enabled, DistinguishedName | ForEach-Object {
    [ordered]@{
        login = $_.SamAccountName; nome = [string]$_.DisplayName; email = [string]$_.mail; departamento = [string]$_.Department
        cargo = [string]$_.Title; ou = OU $_.DistinguishedName; ativo = [bool]$_.Enabled; ultimo_logon = DataAD $ultUs[$_.SamAccountName]
    }
})
Log "$($pcs.Count) computadores e $($us.Count) usuarios lidos"

$pacote = [ordered]@{ dominio = $dominio; origem = $env:COMPUTERNAME; gerado_em = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ'); computadores = $pcs; usuarios = $us }
$json = $pacote | ConvertTo-Json -Depth 4 -Compress

if ($Arquivo) {
    [IO.File]::WriteAllText($Arquivo, $json, (New-Object Text.UTF8Encoding $false))
    Log "Arquivo gerado: $Arquivo (envie em Inventario > Active Directory)"
    exit 0
}
if (-not $Servidor -or -not $Token) { Log 'ERRO: informe -Servidor e -Token (ou baixe o script pelo painel, que ja vem preenchido).'; exit 1 }

try {
    $r = Invoke-RestMethod -Method Post -Uri ($Servidor.TrimEnd('/') + '/api/v1/ad') -Headers @{ 'X-AD-Token' = $Token } `
        -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($json)) -TimeoutSec 120
    Log "Enviado: $($r.estacoes_ativas) estacoes ativas, $($r.com_agente) com o agente ($($r.cobertura_pct)%)"
} catch { Log "ERRO ao enviar para ${Servidor}: $($_.Exception.Message)"; exit 1 }

if ($Agendar) {
    $destino = Join-Path $env:ProgramData 'Harvest\Sincronizar-AD.ps1'
    if ($PSCommandPath -ne $destino) { Copy-Item $PSCommandPath $destino -Force }
    # so administradores (e SYSTEM) leem a copia, que tem o token
    icacls (Split-Path $destino) /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" | Out-Null
    $acao = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$destino`""
    $quando = New-ScheduledTaskTrigger -Daily -At 7am
    $conta = New-ScheduledTaskPrincipal -UserId 'S-1-5-18' -LogonType ServiceAccount -RunLevel Highest
    Register-ScheduledTask -TaskPath '\Harvest\' -TaskName 'Sincronizar-AD' -Action $acao -Trigger $quando -Principal $conta `
        -Description 'Envia computadores e usuarios do AD ao Harvest (somente leitura)' -Force | Out-Null
    Log "Tarefa agendada: \Harvest\Sincronizar-AD (todo dia 7h, como SYSTEM). Log: $log"
}
