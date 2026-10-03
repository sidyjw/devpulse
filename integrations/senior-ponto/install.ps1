# Registra o host local da extensão "DevPulse - Ponto Senior X" no Edge.
#
# O que faz (só no seu usuário, sem precisar de administrador):
#   1. Grava o manifesto do host em %LOCALAPPDATA%\devpulse\senior-ponto\.
#   2. Cria a chave HKCU\Software\Microsoft\Edge\NativeMessagingHosts\com.devpulse.senior_ponto
#      apontando para esse manifesto (com -Chrome, também a do Google Chrome).
#   3. Cria a pasta onde as batidas são gravadas (~/.devpulse/ponto).
#
# Para desfazer: .\uninstall.ps1

param(
    [switch]$Chrome
)

$ErrorActionPreference = 'Stop'

$HostName = 'com.devpulse.senior_ponto'
# Fixo pela "key" do manifest.json da extensão.
$ExtensionId = 'oopkcnbpkledhmkpomeflaomigfnlgdm'

$hostCmd = Join-Path $PSScriptRoot 'host\host.cmd'
$extensionDir = Join-Path $PSScriptRoot 'extension'
foreach ($p in @($hostCmd, (Join-Path $PSScriptRoot 'host\host.ps1'), (Join-Path $extensionDir 'manifest.json'))) {
    if (-not (Test-Path -LiteralPath $p)) { throw "arquivo não encontrado: $p" }
}

$manifestDir = Join-Path $env:LOCALAPPDATA 'devpulse\senior-ponto'
$manifestPath = Join-Path $manifestDir ($HostName + '.json')
[void](New-Item -ItemType Directory -Path $manifestDir -Force)
$manifest = [ordered]@{
    name            = $HostName
    description     = 'DevPulse - grava as marcações de ponto do Senior X em ~/.devpulse/ponto'
    path            = $hostCmd
    type            = 'stdio'
    allowed_origins = @("chrome-extension://$ExtensionId/")
}
[IO.File]::WriteAllText($manifestPath, ($manifest | ConvertTo-Json -Depth 3), (New-Object Text.UTF8Encoding $false))
Write-Host "Manifesto do host: $manifestPath"

$keys = @("HKCU:\Software\Microsoft\Edge\NativeMessagingHosts\$HostName")
if ($Chrome) { $keys += "HKCU:\Software\Google\Chrome\NativeMessagingHosts\$HostName" }
foreach ($k in $keys) {
    [void](New-Item -Path $k -Force)
    Set-Item -Path $k -Value $manifestPath
    Write-Host "Registro: $k"
}

$dataDir = $env:DEVPULSE_PONTO_DIR
if ([string]::IsNullOrWhiteSpace($dataDir)) { $dataDir = Join-Path $env:USERPROFILE '.devpulse\ponto' }
[void](New-Item -ItemType Directory -Path $dataDir -Force)
Write-Host "Batidas serão gravadas em: $dataDir"

Write-Host ''
Write-Host 'Falta carregar a extensão no Edge:'
Write-Host '  1. Abra edge://extensions'
Write-Host '  2. Ligue "Modo de desenvolvedor"'
Write-Host '  3. Clique em "Carregar sem pacote" e escolha a pasta:'
Write-Host "     $extensionDir"
Write-Host "  4. Confira se o ID mostrado é $ExtensionId"
Write-Host '  5. Clique no ícone da extensão para a primeira sincronização (com o Senior X logado no Edge).'
