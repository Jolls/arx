# Starts both Arx apps in separate PowerShell windows.
# Usage: .\start.ps1          (defaults to Test mode)
#        .\start.ps1 -Mode Real
param(
    [ValidateSet('Test', 'Real')]
    [string]$Mode = 'Test'
)

$root    = $PSScriptRoot
$ip      = (Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -notmatch '^(127\.|169\.)' } | Select-Object -First 1).IPAddress
$testEnv = if ($Mode -eq 'Test') { 'true' } else { 'false' }
$modeColor = if ($Mode -eq 'Test') { 'Yellow' } else { 'Red' }

Write-Host "Starting Arx apps in $Mode mode..." -ForegroundColor $modeColor
Write-Host "  PN Viewer         ->  http://localhost:4568  /  http://${ip}:4568" -ForegroundColor Cyan
Write-Host "  Arx: Test Records ->  http://localhost:9292  /  http://${ip}:9292" -ForegroundColor Cyan
Write-Host ""

Start-Process powershell -WorkingDirectory "$root\parts_master_web" `
    -ArgumentList "-NoExit", "-Command", `
    "`$env:TEST_MODE='$testEnv'; Write-Host 'PN Viewer [$Mode mode]' -ForegroundColor Green; bundle exec puma -p 4568 -b tcp://0.0.0.0"

Start-Process powershell -WorkingDirectory "$root\test_records" `
    -ArgumentList "-NoExit", "-Command", `
    "Write-Host 'Arx: Test Records' -ForegroundColor Green; bundle exec puma -p 9292 -b tcp://localhost"
