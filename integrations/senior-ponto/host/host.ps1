# Host de native messaging da extensão "DevPulse - Ponto Senior X".
#
# O Edge inicia este script a cada sincronização, entrega uma mensagem
# (4 bytes de tamanho little-endian + JSON em UTF-8), lê a resposta no mesmo
# formato e encerra o processo. Nada fica rodando e nenhuma porta é aberta.
#
# Grava um arquivo por dia em ~/.devpulse/ponto/AAAA-MM-DD.json
# (ou em $env:DEVPULSE_PONTO_DIR). Só aceita datas, horários e fuso; qualquer
# outro dado da mensagem é descartado.
#
# Compatível com o Windows PowerShell 5.1, sem módulos externos. Nada pode
# ser escrito na saída padrão além da resposta: ela é o canal com o Edge.

$ErrorActionPreference = 'Stop'

$MaxMessage = 1MB
$MaxDays = 62
$MaxPunches = 24

$stdin = [Console]::OpenStandardInput()
$stdout = [Console]::OpenStandardOutput()
$utf8 = New-Object System.Text.UTF8Encoding $false

function Read-Exact([int]$count) {
    $buf = New-Object byte[] $count
    $off = 0
    while ($off -lt $count) {
        $n = $stdin.Read($buf, $off, $count - $off)
        if ($n -le 0) { throw 'a entrada terminou antes da mensagem completa' }
        $off += $n
    }
    return ,$buf
}

function Send-Reply($obj) {
    $bytes = $utf8.GetBytes(($obj | ConvertTo-Json -Compress -Depth 4))
    $stdout.Write([BitConverter]::GetBytes([int32]$bytes.Length), 0, 4)
    $stdout.Write($bytes, 0, $bytes.Length)
    $stdout.Flush()
}

function Get-Day($d) {
    if ($d.date -isnot [string] -or $d.date -notmatch '^\d{4}-\d{2}-\d{2}$') {
        throw "data inválida: $($d.date)"
    }
    [void][datetime]::ParseExact($d.date, 'yyyy-MM-dd', [Globalization.CultureInfo]::InvariantCulture)
    $tz = $null
    if ($null -ne $d.timeZone) {
        if ($d.timeZone -isnot [string] -or $d.timeZone -notmatch '^[+-]\d{2}:\d{2}$') {
            throw "fuso inválido em $($d.date): $($d.timeZone)"
        }
        $tz = $d.timeZone
    }
    $punches = @($d.punches | Where-Object { $null -ne $_ })
    if ($punches.Count -gt $MaxPunches) { throw "batidas demais em $($d.date)" }
    foreach ($p in $punches) {
        if ($p -isnot [string] -or $p -notmatch '^([01]\d|2[0-3]):[0-5]\d:[0-5]\d$') {
            throw "horário inválido em $($d.date): $p"
        }
    }
    $sorted = @($punches | Sort-Object)
    return [ordered]@{
        date     = $d.date
        timeZone = $tz
        punches  = [string[]]$sorted
    }
}

function Write-Day($dir, $day, $source, $syncedAt) {
    $doc = [ordered]@{
        date     = $day.date
        timeZone = $day.timeZone
        punches  = $day.punches
        source   = $source
        syncedAt = $syncedAt
    }
    $json = $doc | ConvertTo-Json -Depth 3
    $file = Join-Path $dir ($day.date + '.json')
    $tmp = $file + '.tmp'
    [IO.File]::WriteAllText($tmp, $json, $utf8)
    Move-Item -LiteralPath $tmp -Destination $file -Force
}

try {
    $len = [BitConverter]::ToInt32((Read-Exact 4), 0)
    if ($len -le 0 -or $len -gt $MaxMessage) { throw "tamanho de mensagem inválido: $len" }
    $msg = $utf8.GetString((Read-Exact $len)) | ConvertFrom-Json

    if ($msg.type -ne 'sync') { throw "tipo de mensagem desconhecido: $($msg.type)" }
    $source = 'senior-x'
    if ($msg.source -is [string] -and $msg.source -match '^[a-z0-9-]{1,32}$') { $source = $msg.source }
    $syncedAt = [DateTimeOffset]::Now.ToString('yyyy-MM-ddTHH:mm:sszzz')

    $days = @($msg.days)
    if ($days.Count -eq 0 -or $days.Count -gt $MaxDays) { throw "quantidade de dias inválida: $($days.Count)" }
    # Valida tudo antes de gravar o primeiro arquivo.
    $valid = @($days | ForEach-Object { Get-Day $_ })

    $dir = $env:DEVPULSE_PONTO_DIR
    if ([string]::IsNullOrWhiteSpace($dir)) { $dir = Join-Path $env:USERPROFILE '.devpulse\ponto' }
    [void](New-Item -ItemType Directory -Path $dir -Force)

    foreach ($day in $valid) { Write-Day $dir $day $source $syncedAt }
    Send-Reply ([ordered]@{ ok = $true; written = $valid.Count; dir = $dir })
} catch {
    Send-Reply ([ordered]@{ ok = $false; error = $_.Exception.Message })
    exit 1
}
