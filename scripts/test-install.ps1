$ErrorActionPreference = 'Stop'
$root = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
try {
    New-Item -ItemType Directory -Path $root | Out-Null
    $fixture = Join-Path $root 'fixture'
    New-Item -ItemType Directory -Path $fixture | Out-Null
    $arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -eq 'Arm64') { 'arm64' } else { 'amd64' }
    $asset = "ddb_0.1.0_windows_$arch.zip"
    Set-Content -LiteralPath (Join-Path $fixture 'ddb.exe') -Value 'fixture-binary' -NoNewline
    Compress-Archive -Path (Join-Path $fixture 'ddb.exe') -DestinationPath (Join-Path $fixture $asset)
    $hash = (Get-FileHash -Algorithm SHA256 (Join-Path $fixture $asset)).Hash
    Set-Content -LiteralPath (Join-Path $fixture 'checksums.txt') -Value "$hash  $asset"
    function Invoke-RestMethod { param($Uri) return @{ tag_name = 'v0.1.0' } }
    function Invoke-WebRequest { param([switch]$UseBasicParsing, $Uri, $OutFile) Copy-Item -LiteralPath (Join-Path $fixture ($Uri.Split('/')[-1])) -Destination $OutFile }
    $dest = Join-Path $root 'install with spaces'
    & "$PSScriptRoot/install.ps1" -InstallDir $dest
    if ((Get-Content -Raw (Join-Path $dest 'ddb.exe')) -ne 'fixture-binary') { throw 'Incorrect installed binary' }
    Set-Content -LiteralPath (Join-Path $fixture 'checksums.txt') -Value "$('0' * 64)  $asset"
    $rejected = $false
    try { & "$PSScriptRoot/install.ps1" -Version 'v0.1.0' -InstallDir $dest } catch { $rejected = $true }
    if (-not $rejected) { throw 'Accepted bad checksum' }
    if ((Get-Content -Raw (Join-Path $dest 'ddb.exe')) -ne 'fixture-binary') { throw 'Damaged existing installation' }
    Write-Host 'PowerShell installer fixture tests passed'
} finally {
    Remove-Item -LiteralPath $root -Force -Recurse
}
