# Starts Arx. The exe must be built first (run build.bat in arx_go\).
# Usage: .\start.ps1          (defaults to Test mode)
#        .\start.ps1 -Mode Real
param(
    [ValidateSet('Test', 'Real')]
    [string]$Mode = 'Test'
)

$root      = $PSScriptRoot
$ip        = (Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -notmatch '^(127\.|169\.)' } | Select-Object -First 1).IPAddress
$testMode  = ($Mode -eq 'Test')
$testEnv   = if ($testMode) { 'true' } else { 'false' }
$modeColor = if ($testMode) { 'Yellow' } else { 'Red' }

Write-Host "Starting Arx in $Mode mode..." -ForegroundColor $modeColor
Write-Host "  Parts Master      ->  http://localhost:4568  /  http://${ip}:4568" -ForegroundColor Cyan
Write-Host "  Test Records      ->  http://localhost:4569  /  http://${ip}:4569" -ForegroundColor Cyan
Write-Host ""

$exe = "$root\arx_go\Arx.exe"

if (-not (Test-Path $exe)) {
    Write-Host "ERROR: $exe not found - run build.bat in arx_go\ first." -ForegroundColor Red
    exit 1
}

# local.json always wins over env vars in config loading order, so patch it directly.
$localJsonMap = @{
    "pm" = "$root\arx_go\config\local.pm.json"
    "tr" = "$root\arx_go\config\local.tr.json"
}
foreach ($key in $localJsonMap.Keys) {
    $localJson = $localJsonMap[$key]
    if (Test-Path $localJson) {
        $cfg = Get-Content $localJson -Raw | ConvertFrom-Json
        if ($cfg.PSObject.Properties['test_mode']) {
            $cfg.test_mode = $testMode
        } else {
            $cfg | Add-Member -NotePropertyName 'test_mode' -NotePropertyValue $testMode
        }
        $updated = $cfg | ConvertTo-Json -Depth 10
        [System.IO.File]::WriteAllText($localJson, $updated, (New-Object System.Text.UTF8Encoding $false))
    }
}

$env:TEST_MODE = $testEnv

Start-Process $exe -WorkingDirectory "$root\arx_go"
