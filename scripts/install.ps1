param(
    [string]$Version = 'latest',
    [string]$InstallDir = $(if ($env:DDB_INSTALL_DIR) { $env:DDB_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'ddb\bin' })
)
$ErrorActionPreference = 'Stop'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw 'Use install.sh on Linux/macOS' }
$arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
    'X64' { 'amd64' }
    'Arm64' { 'arm64' }
    default { throw 'Unsupported architecture' }
}
$base = if ($env:DDB_RELEASE_BASE_URL) { $env:DDB_RELEASE_BASE_URL } else { 'https://github.com/paveforge/go-ddb-cli/releases/download' }
$latest = if ($env:DDB_LATEST_API) { $env:DDB_LATEST_API } else { 'https://api.github.com/repos/paveforge/go-ddb-cli/releases/latest' }
if ($Version -eq 'latest') { $Version = (Invoke-RestMethod -Uri $latest).tag_name }
if (-not $Version.StartsWith('v')) { $Version = "v$Version" }
if ($Version -notmatch '^v\d+\.\d+\.\d+(-[A-Za-z0-9.-]+)?$') { throw 'Invalid version (expected vX.Y.Z)' }
$asset = "ddb_$($Version.Substring(1))_windows_$arch.zip"
$workDir = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid().ToString())
$staged = $null
try {
    New-Item -ItemType Directory -Path $workDir | Out-Null
    $archive = Join-Path $workDir $asset
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$Version/$asset" -OutFile $archive
    Invoke-WebRequest -UseBasicParsing -Uri "$base/$Version/checksums.txt" -OutFile (Join-Path $workDir 'checksums.txt')
    $checksumLines = @(Get-Content (Join-Path $workDir 'checksums.txt') | Where-Object { ($_ -split '\s+')[1] -eq $asset })
    if ($checksumLines.Count -ne 1) { throw 'Missing or ambiguous checksum' }
    $expected = ($checksumLines[0] -split '\s+')[0]
    if ($expected -notmatch '^[a-fA-F0-9]{64}$') { throw 'Invalid checksum' }
    if ((Get-FileHash -Algorithm SHA256 $archive).Hash -ne $expected) { throw 'Checksum mismatch' }
    Expand-Archive -Path $archive -DestinationPath (Join-Path $workDir 'unpacked')
    $binary = Join-Path $workDir 'unpacked\ddb.exe'
    if (-not (Test-Path -LiteralPath $binary -PathType Leaf)) { throw 'Archive has no ddb.exe' }
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $staged = Join-Path $InstallDir ('.ddb-' + [Guid]::NewGuid().ToString() + '.exe')
    Copy-Item -LiteralPath $binary -Destination $staged
    Move-Item -LiteralPath $staged -Destination (Join-Path $InstallDir 'ddb.exe') -Force
    $staged = $null
    Write-Host "Installed ddb $Version to $InstallDir\ddb.exe"
    if (($env:PATH -split ';') -notcontains $InstallDir) { Write-Host "Add $InstallDir to your user PATH, then reopen your terminal." }
} finally {
    if ($staged -and (Test-Path -LiteralPath $staged)) { Remove-Item -LiteralPath $staged -Force }
    Remove-Item -LiteralPath $workDir -Recurse -Force -ErrorAction SilentlyContinue
}
