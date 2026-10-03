# Desfaz o install.ps1: remove as chaves de registro (Edge e Chrome) e o
# manifesto do host. As batidas já gravadas ficam, a menos que use -RemoveData.
# A extensão é removida em edge://extensions.

param(
    [switch]$RemoveData
)

$ErrorActionPreference = 'Stop'

$HostName = 'com.devpulse.senior_ponto'

foreach ($k in @(
        "HKCU:\Software\Microsoft\Edge\NativeMessagingHosts\$HostName",
        "HKCU:\Software\Google\Chrome\NativeMessagingHosts\$HostName")) {
    if (Test-Path -LiteralPath $k) {
        Remove-Item -LiteralPath $k -Recurse
        Write-Host "Removido: $k"
    }
}

$manifestDir = Join-Path $env:LOCALAPPDATA 'devpulse\senior-ponto'
if (Test-Path -LiteralPath $manifestDir) {
    Remove-Item -LiteralPath $manifestDir -Recurse
    Write-Host "Removido: $manifestDir"
}

if ($RemoveData) {
    $dataDir = $env:DEVPULSE_PONTO_DIR
    if ([string]::IsNullOrWhiteSpace($dataDir)) { $dataDir = Join-Path $env:USERPROFILE '.devpulse\ponto' }
    if (Test-Path -LiteralPath $dataDir) {
        Remove-Item -LiteralPath $dataDir -Recurse
        Write-Host "Removido: $dataDir"
    }
}

Write-Host 'Remova também a extensão em edge://extensions.'
