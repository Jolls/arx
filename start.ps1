# Starts both Arx apps. Both exes must be built first (run build.bat in each app dir).
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

Write-Host "Starting Arx apps in $Mode mode..." -ForegroundColor $modeColor
Write-Host "  Parts Master      ->  http://localhost:4568  /  http://${ip}:4568" -ForegroundColor Cyan
Write-Host "  Test Records      ->  http://localhost:4569  /  http://${ip}:4569" -ForegroundColor Cyan
Write-Host ""

$pmExe = "$root\parts_master_go\ArxPartsMaster.exe"
$trExe = "$root\test_records_go\ArxTestRecords.exe"

foreach ($exe in @($pmExe, $trExe)) {
    if (-not (Test-Path $exe)) {
        Write-Host "ERROR: $exe not found - run build.bat in the app directory first." -ForegroundColor Red
        exit 1
    }
}

# local.pm.json always wins over env vars in config loading order, so patch it directly.
foreach ($appDir in @("$root\parts_master_go", "$root\test_records_go")) {
    $localJson = "$appDir\config\local.pm.json"
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

Start-Process $pmExe -WorkingDirectory "$root\parts_master_go"
Start-Process $trExe -WorkingDirectory "$root\test_records_go"
