# Instala o DevPulse no Windows:
#
#   irm https://raw.githubusercontent.com/sidyjw/devpulse/main/install.ps1 | iex
#
# Baixa a release do seu sistema, confere o SHA256 com o SHA256SUMS.txt da
# release e roda `devpulse install`. Para passar argumentos ao instalador:
#
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/sidyjw/devpulse/main/install.ps1))) --yes --harness claude --app code
#
# $env:DEVPULSE_VERSION = '0.2.0' fixa a versão (padrão: a mais recente).
# Funciona no Windows PowerShell 5.1 e no PowerShell 7.

function Install-DevPulse {
    param([string[]]$InstallerArgs)

    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # a barra de progresso deixa o download muito lento no 5.1
    $Repo = 'sidyjw/devpulse'
    $Name = 'devpulse'

    if ($InstallerArgs -contains '--no-copy' -or $InstallerArgs -contains '-no-copy') {
        throw '--no-copy não funciona aqui: o executável baixado é apagado no fim'
    }
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $cpu = $env:PROCESSOR_ARCHITEW6432 # PowerShell de 32 bits num Windows de 64 bits
    if (-not $cpu) { $cpu = $env:PROCESSOR_ARCHITECTURE }
    switch ($cpu) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "arquitetura não suportada: $cpu" }
    }

    $version = $env:DEVPULSE_VERSION
    if (-not $version) {
        # releases/latest redireciona para .../releases/tag/vX.Y.Z: sem API, sem limite de requisições
        $req = [Net.HttpWebRequest]::Create("https://github.com/$Repo/releases/latest")
        $req.Method = 'HEAD'
        $req.AllowAutoRedirect = $false
        $resp = $req.GetResponse()
        try { $location = $resp.Headers['Location'] } finally { $resp.Close() }
        if ($location -notmatch '/tag/([^/]+)$') { throw "não consegui descobrir a última versão ($location)" }
        $version = $Matches[1]
    }
    $version = $version -replace '^v', ''
    if ($version -notmatch '^[0-9A-Za-z.-]+$') { throw "versão inválida: $version" }

    $pkg = "${Name}_${version}_windows_${arch}"
    $archive = "$pkg.zip"
    $base = "https://github.com/$Repo/releases/download/v$version"
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("devpulse-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "Baixando $Name $version (windows/$arch)..."
        Invoke-WebRequest -UseBasicParsing -Uri "$base/$archive" -OutFile (Join-Path $tmp $archive)
        Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS.txt" -OutFile (Join-Path $tmp 'SHA256SUMS.txt')

        $want = $null
        foreach ($line in Get-Content (Join-Path $tmp 'SHA256SUMS.txt')) {
            $f = $line -split '\s+', 2
            if ($f.Count -eq 2 -and $f[1].TrimStart('*') -eq $archive) { $want = $f[0].ToLower() }
        }
        if (-not $want) { throw "$archive não está no SHA256SUMS.txt" }
        $got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $archive)).Hash.ToLower()
        if ($got -ne $want) { throw "o SHA256 de $archive não confere (esperado $want, obtido $got)" }
        Write-Host ([char]0x2713 + ' SHA256 conferido') -ForegroundColor Green
        if (Get-Command gh -ErrorAction SilentlyContinue) {
            Write-Host "  (opcional) para conferir a proveniência: gh attestation verify <arquivo> --repo $Repo" -ForegroundColor DarkGray
        }

        Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath $tmp -Force
        $exe = Join-Path (Join-Path $tmp $pkg) "$Name.exe"
        if (-not (Test-Path $exe)) { throw "executável não encontrado em $archive" }

        & $exe install @InstallerArgs
        if ($LASTEXITCODE -ne 0) { Write-Host "O instalador terminou com código $LASTEXITCODE." -ForegroundColor Yellow }
    }
    finally {
        Remove-Item -Recurse -Force -Path $tmp -ErrorAction SilentlyContinue
    }
}

Install-DevPulse -InstallerArgs $args
