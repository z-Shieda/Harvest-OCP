$ErrorActionPreference = 'SilentlyContinue'
$ProgressPreference = 'SilentlyContinue'
# Corrige serialização de arrays no Windows PowerShell 5.1 (evita {"value":[...],"Count":n})
Remove-TypeData System.Array -ErrorAction SilentlyContinue
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false

function D($d) {
    if ($d) { try { return ([datetime]$d).ToUniversalTime().ToString('o') } catch { } }
    return ''
}
function S($v) { if ($null -eq $v) { return '' } return ([string]$v).Trim() }
function U($v) { if ($null -eq $v) { return [uint64]0 } try { return [uint64]$v } catch { return [uint64]0 } }

$cs   = Get-CimInstance Win32_ComputerSystem
$os   = Get-CimInstance Win32_OperatingSystem
$bios = Get-CimInstance Win32_BIOS
$csp  = Get-CimInstance Win32_ComputerSystemProduct
$cpus = @(Get-CimInstance Win32_Processor)

# ---------- Memória ----------
$mem = @(Get-CimInstance Win32_PhysicalMemory | ForEach-Object {
    @{ capacidade = (U $_.Capacity); velocidade = [int]$_.Speed; fabricante = (S $_.Manufacturer);
       part_number = (S $_.PartNumber); slot = (S $_.DeviceLocator) }
})

# ---------- Discos físicos ----------
$discos = @()
$pd = @(Get-CimInstance -Namespace root\Microsoft\Windows\Storage -ClassName MSFT_PhysicalDisk)
if ($pd.Count -gt 0) {
    $discos = @($pd | ForEach-Object {
        $tipo = switch ([int]$_.MediaType) { 3 { 'HDD' } 4 { 'SSD' } 5 { 'SCM' } default { '' } }
        $bus  = switch ([int]$_.BusType) { 7 { 'USB' } 8 { 'RAID' } 10 { 'SAS' } 11 { 'SATA' } 12 { 'SD' } 17 { 'NVMe' } default { '' } }
        if ($bus -eq 'NVMe' -and $tipo -eq '') { $tipo = 'SSD' }
        @{ modelo = (S $_.FriendlyName); serial = (S $_.SerialNumber); tamanho = (U $_.Size); tipo = $tipo; barramento = $bus }
    })
} else {
    $discos = @(Get-CimInstance Win32_DiskDrive | ForEach-Object {
        @{ modelo = (S $_.Model); serial = (S $_.SerialNumber); tamanho = (U $_.Size); tipo = (S $_.MediaType); barramento = (S $_.InterfaceType) }
    })
}

# ---------- Volumes ----------
$vols = @(Get-CimInstance Win32_LogicalDisk -Filter 'DriveType=3' | ForEach-Object {
    @{ letra = (S $_.DeviceID); rotulo = (S $_.VolumeName); sistema = (S $_.FileSystem); tamanho = (U $_.Size); livre = (U $_.FreeSpace) }
})

# ---------- Vídeo ----------
$gpus = @(Get-CimInstance Win32_VideoController | Where-Object { $_.Name } | ForEach-Object { S $_.Name })

# ---------- Rede ----------
$cfgs = @{}
Get-CimInstance Win32_NetworkAdapterConfiguration | ForEach-Object { $cfgs[[int]$_.Index] = $_ }
$redes = @(Get-CimInstance Win32_NetworkAdapter -Filter 'MACAddress IS NOT NULL' | ForEach-Object {
    $a = $_
    $pnp = S $a.PNPDeviceID
    $fis = ($a.PhysicalAdapter -eq $true) -and -not ($pnp -like 'ROOT\*') -and -not ($pnp -like 'SWD\*')
    $ips = @()
    $c = $cfgs[[int]$a.Index]
    if ($c -and $c.IPAddress) { $ips = @($c.IPAddress | Where-Object { $_ -match '^\d+\.\d+\.\d+\.\d+$' }) }
    $nome = S $a.Name
    $con = S $a.NetConnectionID
    if ($con) { $nome = $con + ' - ' + $nome }
    @{ nome = $nome; mac = (S $a.MACAddress); ipv4 = $ips; conectada = ([int]$a.NetConnectionStatus -eq 2); fisica = $fis }
})

# ---------- Usuários ----------
$sessao = @()
Get-CimInstance Win32_Process -Filter "Name='explorer.exe'" | ForEach-Object {
    $o = Invoke-CimMethod -InputObject $_ -MethodName GetOwner
    if ($o -and $o.User) { $sessao += ((S $o.Domain) + '\' + (S $o.User)) }
}
$sessao = @($sessao | Sort-Object -Unique)

$logonUI = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Authentication\LogonUI'
$ultimo = S $logonUI.LastLoggedOnSAMUser
if (-not $ultimo) { $ultimo = S $logonUI.LastLoggedOnUser }
if ($ultimo -like '.\*') { $ultimo = $env:COMPUTERNAME + $ultimo.Substring(1) }

$perfis = @(Get-CimInstance Win32_UserProfile -Filter 'Special=False' | ForEach-Object {
    $pf = $_
    $nome = ''
    try { $nome = (New-Object System.Security.Principal.SecurityIdentifier($pf.SID)).Translate([System.Security.Principal.NTAccount]).Value } catch { }
    if (-not $nome) { $nome = Split-Path -Leaf (S $pf.LocalPath) }
    $uso = $pf.LastUseTime
    $nt = Join-Path (S $pf.LocalPath) 'NTUSER.DAT'
    $f = Get-Item -LiteralPath $nt -Force
    if ($f -and ((-not $uso) -or ($f.LastWriteTime -gt $uso))) { $uso = $f.LastWriteTime }
    @{ usuario = $nome; sid = (S $pf.SID); caminho = (S $pf.LocalPath); ultimo_uso = (D $uso) }
})

$cpu0 = $cpus | Select-Object -First 1
$nucleos = 0; $threads = 0
foreach ($p in $cpus) { $nucleos += [int]$p.NumberOfCores; $threads += [int]$p.NumberOfLogicalProcessors }

$ver = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion'
$disp = S $ver.DisplayVersion
if (-not $disp) { $disp = S $ver.ReleaseId }
$build = S $os.BuildNumber
if ($ver.UBR) { $build = $build + '.' + [string]$ver.UBR }

$r = @{
    hostname       = (S $env:COMPUTERNAME)
    dominio        = (S $cs.Domain)
    no_dominio     = ($cs.PartOfDomain -eq $true)
    fabricante     = (S $cs.Manufacturer)
    modelo         = (S $cs.Model)
    serial         = (S $bios.SerialNumber)
    hw_uuid        = (S $csp.UUID)
    bios           = ((S $bios.Manufacturer) + ' ' + (S $bios.SMBIOSBIOSVersion)).Trim()
    so_nome        = (S $os.Caption)
    so_versao      = $disp
    so_build       = $build
    so_arquitetura = (S $os.OSArchitecture)
    so_instalacao  = (D $os.InstallDate)
    ultimo_boot    = (D $os.LastBootUpTime)
    cpu            = (S $cpu0.Name)
    cpu_nucleos    = $nucleos
    cpu_threads    = $threads
    ram_bytes      = (U $cs.TotalPhysicalMemory)
    memorias       = $mem
    discos         = $discos
    volumes        = $vols
    gpus           = $gpus
    redes          = $redes
    usuario_atual  = (S $cs.UserName)
    usuarios_sessao = $sessao
    ultimo_logon   = $ultimo
    ultimo_logon_nome = (S $logonUI.LastLoggedOnDisplayName)
    perfis         = $perfis
}
$r | ConvertTo-Json -Depth 6 -Compress
