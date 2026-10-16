# Kestrel Windows Installer
# Downloads the latest release from GitHub, verifies SHA-256, and installs to LocalAppData
[CmdletBinding()]
param(
    [string]$Version = $env:KESTREL_VERSION,
    [string]$InstallDir = $(if ($env:KESTREL_INSTALL_DIR) { $env:KESTREL_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\kestrel" })
)

$ErrorActionPreference = "Stop"
$Repo = "Aareevs/Kestrel-CLI"

function Write-Info($msg) { Write-Host "kestrel-install: $msg" -ForegroundColor Cyan }
function Write-Err($msg) { Write-Host "error: $msg" -ForegroundColor Red; exit 1 }

# Check architecture
$arch = if ([System.Environment]::Is64BitOperatingSystem) { "amd64" } else { Write-Err "Kestrel requires 64-bit Windows" }

# Resolve version
if (-not $Version) {
    try {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "kestrel-installer" }
        $Version = $release.tag_name
    } catch {
        Write-Err "Could not retrieve the latest release tag. Specify -Version vX.Y.Z manually."
    }
}

if (-not $Version.StartsWith("v")) {
    $Version = "v$Version"
}
$plainVersion = $Version.TrimStart("v")
$archive = "kestrel_${plainVersion}_windows_${arch}.zip"
$downloadUrl = "https://github.com/$Repo/releases/download/$Version/$archive"
$checksumUrl = "https://github.com/$Repo/releases/download/$Version/checksums.txt"

$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmpDir | Out-Null

try {
    Write-Info "Downloading $archive ($Version)..."
    $zipPath = Join-Path $tmpDir $archive
    $checksumPath = Join-Path $tmpDir "checksums.txt"

    Invoke-WebRequest -Uri $downloadUrl -OutFile $zipPath
    Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumPath

    Write-Info "Verifying SHA-256 checksum..."
    $actualHash = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLower()

    $expectedLine = Get-Content $checksumPath | Where-Object { $_ -match [regex]::Escape($archive) }
    if (-not $expectedLine) {
        Write-Err "Archive $archive not found in checksums.txt"
    }
    $expectedHash = ($expectedLine -split '\s+')[0].ToLower()

    if ($actualHash -ne $expectedHash) {
        Write-Err "Checksum mismatch! Expected: $expectedHash, got: $actualHash. Aborting installation."
    }
    Write-Info "Checksum verified."

    Write-Info "Extracting to $InstallDir..."
    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    Expand-Archive -Path $zipPath -DestinationPath $tmpDir -Force
    $sourceExe = Join-Path $tmpDir "kestrel.exe"
    $targetExe = Join-Path $InstallDir "kestrel.exe"

    Copy-Item -Path $sourceExe -Destination $targetExe -Force
    Write-Info "Installed $targetExe successfully."

    # Add to User PATH if not already present
    $userPath = [Environment]::GetEnvironmentVariable("Path", [EnvironmentVariableTarget]::User)
    $paths = $userPath -split ';'
    if ($paths -notcontains $InstallDir) {
        Write-Info "Adding $InstallDir to user PATH..."
        $newUserPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable("Path", $newUserPath, [EnvironmentVariableTarget]::User)
        $env:Path = "$env:Path;$InstallDir"
        Write-Info "PATH updated. Restart your terminal for changes to take full effect."
    }

    Write-Host "`n✓ Kestrel $Version installed successfully! Run 'kestrel --help' to get started.`n" -ForegroundColor Green
} finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
