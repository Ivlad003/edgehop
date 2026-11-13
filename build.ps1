# Builds edgehop for every supported platform into dist\.
#
#   $env:VERSION = 'v1.2.3'; $env:REPO_URL = 'https://github.com/owner/edgehop'; .\build.ps1
#
# Both variables are optional; the release workflow sets them.
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Force dist | Out-Null
$env:CGO_ENABLED = '0'
$version = if ($env:VERSION) { $env:VERSION -replace '^v', '' } else { 'dev' }
$ldflags = "-s -w -X main.version=$version -X main.repoURL=$($env:REPO_URL)"
$targets = 'windows/amd64', 'windows/arm64', 'linux/amd64', 'linux/arm64', 'darwin/arm64', 'darwin/amd64'
try {
    foreach ($target in $targets) {
        $os, $arch = $target.Split('/')
        $ext = ''
        if ($os -eq 'windows') { $ext = '.exe' }
        $out = "dist/edgehop-$os-$arch$ext"
        Write-Host "building $out ($version)"
        $env:GOOS = $os
        $env:GOARCH = $arch
        go build -trimpath -ldflags $ldflags -o $out ./cmd/edgehop
        if ($LASTEXITCODE -ne 0) { throw "build failed for $target" }
    }
}
finally {
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
}
