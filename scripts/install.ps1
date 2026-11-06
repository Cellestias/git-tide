# Install gtide (and the optional tide alias) on Windows.
#
#   irm https://github.com/Cellestias/git-tide/releases/latest/download/install.ps1 | iex
#
# Environment:
#   GTIDE_VERSION      release to install, e.g. v1.1.0 (default: the latest)
#   GTIDE_INSTALL_DIR  where to put the binaries (default: %LOCALAPPDATA%\Programs\gtide)
#   GTIDE_NO_ALIAS     set to 1 to skip the tide alias
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repo = "https://github.com/Cellestias/git-tide"
$installDir = if ($env:GTIDE_INSTALL_DIR) { $env:GTIDE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\gtide" }

$arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
    "X64"   { "amd64" }
    "Arm64" { "arm64" }
    default { throw "unsupported CPU: $_" }
}

$version = $env:GTIDE_VERSION
if (-not $version) {
    $version = (Invoke-RestMethod "https://api.github.com/repos/Cellestias/git-tide/releases/latest").tag_name
}
if ($version -notmatch '^v\d') { throw "could not work out the version (got '$version'); set GTIDE_VERSION=vX.Y.Z" }

$name = "gtide_${version}_windows_${arch}"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("gtide-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    Write-Host "Downloading $name"
    Invoke-WebRequest "$repo/releases/download/$version/$name.zip" -OutFile "$tmp\$name.zip"
    Invoke-WebRequest "$repo/releases/download/$version/checksums.txt" -OutFile "$tmp\checksums.txt"

    $line = Select-String -Path "$tmp\checksums.txt" -Pattern " $([regex]::Escape($name)).zip$" | Select-Object -First 1
    if (-not $line) { throw "$name.zip is not listed in checksums.txt" }
    $expected = ($line.Line -split "\s+")[0]
    if ((Get-FileHash "$tmp\$name.zip" -Algorithm SHA256).Hash -ne $expected) {
        throw "checksum mismatch for $name.zip; nothing was installed"
    }
    Write-Host "Checksum OK"

    Expand-Archive "$tmp\$name.zip" -DestinationPath $tmp
    New-Item -ItemType Directory -Force $installDir | Out-Null
    Copy-Item "$tmp\$name\gtide.exe" $installDir -Force
    Write-Host "Installed $installDir\gtide.exe"

    if ($env:GTIDE_NO_ALIAS -ne "1") {
        # Never overwrite a different program called tide.
        $existing = Join-Path $installDir "tide.exe"
        $ours = $true
        if (Test-Path $existing) {
            $ours = (& $existing --version 2>$null) -match '^gtide version'
        }
        if ($ours) {
            Copy-Item "$tmp\$name\tide.exe" $installDir -Force
            Write-Host "Installed $installDir\tide.exe (alias)"
        } else {
            Write-Host "Skipped the tide alias: $existing is a different program. Use gtide."
        }
    }

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (($userPath -split ";") -notcontains $installDir) {
        [Environment]::SetEnvironmentVariable("Path", "$userPath;$installDir", "User")
        Write-Host "Added $installDir to your PATH. Open a new terminal to use gtide."
    }

    & "$installDir\gtide.exe" --version
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
