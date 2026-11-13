# Builds edgehop for every supported platform into dist\.
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Force dist | Out-Null
$env:CGO_ENABLED = '0'
$targets = 'windows/amd64', 'windows/arm64', 'linux/amd64', 'linux/arm64', 'darwin/arm64', 'darwin/amd64'
try {
    foreach ($target in $targets) {
        $os, $arch = $target.Split('/')
        $ext = ''
        if ($os -eq 'windows') { $ext = '.exe' }
        $out = "dist/edgehop-$os-$arch$ext"
        Write-Host "building $out"
        $env:GOOS = $os
        $env:GOARCH = $arch
        go build -trimpath -ldflags '-s -w' -o $out ./cmd/edgehop
        if ($LASTEXITCODE -ne 0) { throw "build failed for $target" }
    }
}
finally {
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
}
