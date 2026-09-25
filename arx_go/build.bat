@echo off
:: Build Arx.exe — single binary (Parts Master + Test Records in one package)
:: Run from arx_go\: build.bat
:: Output: Arx.exe in arx_go\
::
:: -H windowsgui suppresses the console window (required for systray).
:: AppVersion is injected from the top ## [x.y.z] in CHANGELOG.md.

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
go test ./...
popd
if %errorlevel% neq 0 (
    echo Tests failed — aborting build.
    exit /b %errorlevel%
)

go build -trimpath -ldflags "-H windowsgui -X main.AppVersion=%VERSION%" -o Arx.exe .
if %errorlevel% neq 0 (
    echo Build failed.
    exit /b %errorlevel%
)
echo Built Arx.exe v%VERSION%
