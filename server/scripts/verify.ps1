param([string]$Go = "go", [string]$Python = "python")
$ErrorActionPreference = "Stop"
$savedEnvironment = @{}
foreach ($name in @("CGO_ENABLED", "GOCACHE", "GOMODCACHE", "GOPATH", "GOOS", "GOARCH")) {
    $savedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}
Push-Location (Split-Path -Parent $PSScriptRoot)
try {
    $env:CGO_ENABLED = "0"
    if (-not $env:GOCACHE) { $env:GOCACHE = Join-Path (Get-Location) ".local/gocache" }
    if (-not $env:GOMODCACHE) { $env:GOMODCACHE = Join-Path (Get-Location) ".local/gomodcache" }
    if (-not $env:GOPATH) { $env:GOPATH = Join-Path (Get-Location) ".local/gopath" }
    function Invoke-Go([string[]]$GoArguments) {
        & $Go @GoArguments
        if ($LASTEXITCODE -ne 0) { throw "Go failed: $GoArguments" }
    }
    $goDirectory = Split-Path -Parent (Get-Command $Go).Source
    $formatting = & (Join-Path $goDirectory "gofmt.exe") -l cmd internal
    if ($LASTEXITCODE -ne 0 -or $formatting) { throw "Go formatting required: $formatting" }
    Invoke-Go @("mod", "verify")
    Invoke-Go @("test", "./...")
    Invoke-Go @("vet", "./...")
    Invoke-Go @("build", "-trimpath", "-o", ".local/quakerelay.exe", "./cmd/quakerelay")
    Invoke-Go @("build", "-trimpath", "-o", ".local/apns-send.exe", "./cmd/apns-send")
    & $Python scripts/smoke.py --binary .local/quakerelay.exe
    if ($LASTEXITCODE -ne 0) { throw "Smoke test failed" }
    $env:GOOS = "linux"
    foreach ($arch in @("amd64", "arm64")) {
        $env:GOARCH = $arch
        Invoke-Go @("build", "-trimpath", "-o", ".local/quakerelay-linux-$arch", "./cmd/quakerelay")
        Invoke-Go @("build", "-trimpath", "-o", ".local/apns-send-linux-$arch", "./cmd/apns-send")
    }
} finally {
    foreach ($name in $savedEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $savedEnvironment[$name], "Process")
    }
    Pop-Location
}
