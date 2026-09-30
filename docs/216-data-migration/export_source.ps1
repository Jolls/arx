<#
.SYNOPSIS
  Exports every data table of the Azure SQL ArxProd database to CSV, plus manifest.csv, for
  arx_go/cmd/migrate_data (#216). Read-only: it only runs SELECTs. Run it yourself; see runbook.md.

.EXAMPLE
  # Entra (needs the Azure CLI: winget install Microsoft.AzureCLI, then az login):
  .\export_source.ps1 -Server myserver.database.windows.net -Database ArxProd -OutDir C:\arx-export -Entra
  # SQL login (prompts for the password; never put it on the command line):
  .\export_source.ps1 -Server myserver.database.windows.net -Database ArxProd -OutDir C:\arx-export -User someone
#>
param(
    [Parameter(Mandatory)][string]$Server,
    [Parameter(Mandatory)][string]$Database,
    [Parameter(Mandatory)][string]$OutDir,
    [string]$User,    # SQL login name (you are prompted for its password)
    [switch]$Entra    # instead: use the Azure CLI's Entra sign-in (needs 'az login' first)
)
$ErrorActionPreference = 'Stop'
$inv = [System.Globalization.CultureInfo]::InvariantCulture
$skip = @('logs', 'release_notes', 'schema_migrations')   # dead tables (#31) and the migration ledger

$Server = $Server -replace ':(\d+)$', ',$1'   # SqlClient wants host,port, not host:port
$cs = "Server=$Server;Database=$Database;Encrypt=True;"
$token = $null
if ($Entra) {
    # Windows PowerShell's built-in SqlClient cannot sign in to Entra itself, so borrow a token from the Azure CLI.
    if (-not (Get-Command az -ErrorAction SilentlyContinue)) { throw "-Entra needs the Azure CLI: run 'winget install Microsoft.AzureCLI', open a new terminal, then 'az login'" }
    $token = (& az account get-access-token --resource https://database.windows.net/ --query accessToken -o tsv)
    if (-not $token) { throw "az gave no token: run 'az login' first (and 'az account show' to check the tenant)" }
} else {
    if (-not $User) { throw "-User is required for a SQL login (or use -Entra)" }
    $pw = Read-Host -AsSecureString "Password for $User"
    $plain = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($pw))
    $cs += "User ID=$User;Password=$plain;"
}
$conn = New-Object System.Data.SqlClient.SqlConnection $cs
if ($token) { $conn.AccessToken = $token }
$conn.Open()

function Invoke-Rows([string]$sql) {
    $cmd = $conn.CreateCommand(); $cmd.CommandText = $sql; $cmd.CommandTimeout = 300
    $r = $cmd.ExecuteReader(); $rows = @()
    while ($r.Read()) { $rows += , @(0..($r.FieldCount - 1) | ForEach-Object { $r.GetValue($_) }) }
    $r.Close(); return , $rows
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$utf8 = New-Object System.Text.UTF8Encoding $false
$manifest = New-Object System.Collections.Generic.List[string]
$manifest.Add('table,metric,value')

$tables = (Invoke-Rows "SELECT name FROM sys.tables ORDER BY name") | ForEach-Object { $_[0] } | Where-Object { $skip -notcontains $_ }
foreach ($t in $tables) {
    $cmd = $conn.CreateCommand(); $cmd.CommandText = "SELECT * FROM [$t]"; $cmd.CommandTimeout = 600
    $r = $cmd.ExecuteReader()
    $w = New-Object System.IO.StreamWriter (Join-Path $OutDir "$t.csv"), $false, $utf8
    $names = 0..($r.FieldCount - 1) | ForEach-Object { '"' + $r.GetName($_) + '"' }
    $w.Write(($names -join ',') + "`r`n")
    $n = 0
    while ($r.Read()) {
        $f = New-Object string[] $r.FieldCount
        for ($i = 0; $i -lt $r.FieldCount; $i++) {
            if ($r.IsDBNull($i)) { $f[$i] = '\N'; continue }
            $v = $r.GetValue($i)
            if     ($v -is [bool])     { $s = if ($v) { '1' } else { '0' } }
            elseif ($v -is [datetime]) { $s = if ($r.GetDataTypeName($i) -eq 'date') { $v.ToString('yyyy-MM-dd', $inv) } else { $v.ToString('yyyy-MM-dd HH:mm:ss.fff', $inv) } }
            elseif ($v -is [decimal])  { $s = $v.ToString($inv) }
            else                       { $s = [string]$v }
            if ($s -ceq '\N') { throw "$t.$($r.GetName($i)) holds the literal text \N, which the CSV uses for NULL; fix that value first" }
            $f[$i] = '"' + $s.Replace('"', '""') + '"'
        }
        $w.Write(($f -join ',') + "`r`n")
        $n++
    }
    $r.Close(); $w.Close()

    # Counts and sums are computed by the server, separately from the export, so the loader can
    # tell a truncated or mangled file from good data.
    $rows = (Invoke-Rows "SELECT COUNT_BIG(*) FROM [$t]")[0][0]
    $manifest.Add("$t,rows,$rows")
    $dec = Invoke-Rows "SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_NAME = '$t' AND DATA_TYPE = 'decimal'"
    foreach ($d in $dec) {
        $c = $d[0]
        $sum = (Invoke-Rows "SELECT COALESCE(SUM([$c]), 0) FROM [$t]")[0][0]
        $manifest.Add("$t,sum:$c,$($sum.ToString($inv))")
    }
    if ($n -ne $rows) { Write-Warning "$t : exported $n rows but the server counts $rows (rows changed during export? freeze writes)" }
    Write-Host ("{0,-26} {1,7} rows" -f $t, $n)
}
[System.IO.File]::WriteAllLines((Join-Path $OutDir 'manifest.csv'), $manifest, $utf8)
$conn.Close()
Write-Host "Done: $OutDir"
