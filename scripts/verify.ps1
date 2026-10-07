param([string]$Go = "go", [string]$Python = "python")
$ErrorActionPreference = "Stop"
& $Python (Join-Path $PSScriptRoot "verify-repository.py")
if ($LASTEXITCODE -ne 0) { throw "Repository validation failed" }
& (Join-Path $PSScriptRoot "../server/scripts/verify.ps1") -Go $Go -Python $Python
