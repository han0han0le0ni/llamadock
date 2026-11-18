# LlamaDock build script (ASCII-only on purpose: Windows PowerShell 5.1 reads
# .ps1 files as ANSI, so non-ASCII text here would break parsing).
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File build.ps1
#   powershell -ExecutionPolicy Bypass -File build.ps1 -SkipFrontend
#
# Outputs:
#   build/llamadock.exe          headless (browser admin UI)
#   build/llamadock-desktop.exe  Wails desktop window + system tray

param(
    [switch]$SkipFrontend,
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
Set-Location $root

Write-Host "==> frontend build (Vue3 + Vite -> internal/webui/dist)" -ForegroundColor Cyan
if (-not $SkipFrontend) {
    Push-Location frontend
    if (-not (Test-Path node_modules)) { npm install --no-audit --no-fund }
    npm run build
    Pop-Location
} else {
    Write-Host "    skipped"
}

if (-not $SkipTests) {
    Write-Host "==> unit tests" -ForegroundColor Cyan
    go test ./internal/...
}

New-Item -ItemType Directory -Force -Path build | Out-Null

Write-Host "==> build headless" -ForegroundColor Cyan
go build -trimpath -ldflags "-s -w" -o build/llamadock.exe ./cmd/ai-runner

Write-Host "==> build desktop window (-tags desktop)" -ForegroundColor Cyan
go build -trimpath -tags desktop -ldflags "-s -w" -o build/llamadock-desktop.exe .

Write-Host "==> done" -ForegroundColor Green
Get-ChildItem build/*.exe | ForEach-Object {
    "{0,-28} {1,8:N1} MB" -f $_.Name, ($_.Length / 1MB)
}
Write-Host ""
Write-Host "run: build\llamadock-desktop.exe  (window + tray)"
Write-Host "     build\llamadock.exe -no-browser  (headless, admin UI on 127.0.0.1:8081)"
