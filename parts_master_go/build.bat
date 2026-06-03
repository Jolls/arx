@echo off
:: Build ArxPartsMaster.exe
:: Run from parts_master_go\: build.bat
:: Output: ArxPartsMaster.exe in the same folder.
::
:: -H windowsgui suppresses the console window (required for systray apps).
:: AppVersion is parsed from the top ## [x.y.z] entry in CHANGELOG.md — no need to update config.go.

cd /d "%~dp0"

:: Extract version from CHANGELOG.md
for /f "usebackq delims=" %%v in (`powershell -NoProfile -Command "(Select-String -Path '..\CHANGELOG.md' -Pattern '## \[(\d+\.\d+[\d.]*)\]' | Select-Object -First 1).Matches.Groups[1].Value"`) do set VERSION=%%v

if "%VERSION%"=="" (
    echo Could not parse version from CHANGELOG.md — aborting.
    exit /b 1
)
echo Version: %VERSION%

echo Running tests...
pushd "%~dp0\.."
go test ./parts_master_go/...
popd
if %errorlevel% neq 0 (
    echo Tests failed — aborting build.
    exit /b %errorlevel%
)

go build -ldflags "-H windowsgui -X arx/parts_master_go/config.AppVersion=%VERSION%" -o ArxPartsMaster.exe .
if %errorlevel% neq 0 (
    echo Build failed.
    exit /b %errorlevel%
)
echo Built ArxPartsMaster.exe v%VERSION%
