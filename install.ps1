# edgehop installer for Windows.
#
#   irm https://raw.githubusercontent.com/Ivlad003/edgehop/main/install.ps1 | iex
#
# Downloads the latest release, checks its SHA-256 checksum, puts it into
# %LOCALAPPDATA%\Programs\edgehop and adds that folder to your user PATH
# (no administrator rights needed).
#
# Optional environment variables:
#   EDGEHOP_INSTALL_DIR   where to put the program
#   EDGEHOP_VERSION       a specific version such as v0.1.0 (default: latest)
#   EDGEHOP_NO_PATH       set to 1 to leave PATH alone
#   EDGEHOP_REPO          owner/name of the GitHub repository

& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'   # the progress bar makes downloads very slow

    $repo = if ($env:EDGEHOP_REPO) { $env:EDGEHOP_REPO } else { 'Ivlad003/edgehop' }
    $version = if ($env:EDGEHOP_VERSION) { $env:EDGEHOP_VERSION } else { 'latest' }
    $dir = if ($env:EDGEHOP_INSTALL_DIR) { $env:EDGEHOP_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\edgehop' }

    $arch = 'amd64'
    if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64' -or $env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { $arch = 'arm64' }
    $file = "edgehop-windows-$arch.exe"
    $base = if ($version -eq 'latest') { "https://github.com/$repo/releases/latest/download" } else { "https://github.com/$repo/releases/download/$version" }

    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('edgehop-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "Downloading $file ($version) ..."
        $exe = Join-Path $tmp $file
        $sums = Join-Path $tmp 'SHA256SUMS.txt'
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$base/$file" -OutFile $exe
            Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS.txt" -OutFile $sums
        }
        catch {
            throw "could not download $base/$file - check the internet connection and that version '$version' exists"
        }

        $want = $null
        foreach ($line in Get-Content -LiteralPath $sums) {
            $parts = $line -split '\s+', 2
            if ($parts.Count -eq 2 -and $parts[1].Trim().TrimStart('*') -eq $file) { $want = $parts[0].ToLower() }
        }
        if (-not $want) { throw "no checksum for $file in SHA256SUMS.txt" }
        $got = (Get-FileHash -Algorithm SHA256 -LiteralPath $exe).Hash.ToLower()
        if ($got -ne $want) { throw "checksum mismatch for $file - the download is damaged or was tampered with" }

        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        $target = Join-Path $dir 'edgehop.exe'
        try {
            Move-Item -Force -LiteralPath $exe -Destination $target
        }
        catch {
            throw "could not replace $target - close edgehop if it is running and try again"
        }
    }
    finally {
        Remove-Item -Recurse -Force -LiteralPath $tmp -ErrorAction SilentlyContinue
    }

    $installed = & $target version
    Write-Host "Installed $installed to $target"
    Write-Host ''

    $run = $target
    if ($env:EDGEHOP_NO_PATH -ne '1') {
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $entries = @()
        if ($userPath) { $entries = $userPath -split ';' | Where-Object { $_ } }
        if ($entries -notcontains $dir) {
            [Environment]::SetEnvironmentVariable('Path', (($entries + $dir) -join ';'), 'User')
            Write-Host "Added $dir to your PATH. Open a new terminal to use the 'edgehop' command."
        }
        else {
            $run = 'edgehop'
        }
        if (($env:Path -split ';') -notcontains $dir) { $env:Path = "$env:Path;$dir" }
    }

    Write-Host 'The first time it shares the mouse, Windows asks about the firewall:'
    Write-Host 'allow edgehop on private networks.'
    Write-Host ''
    Write-Host "Start it with:  $run"
}
