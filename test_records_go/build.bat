@echo off
:: Build ArxTestRecords.exe
:: Run from test_records_go\: build.bat
:: Output: ArxTestRecords.exe in the same folder.
::
:: -H windowsgui suppresses the console window (required for systray apps).
:: AppVersion is parsed from the top ## [x.y.z] entry in CHANGELOG.md.

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
go test ./test_records_go/... ./arxlib/...
popd
if %errorlevel% neq 0 (
    echo Tests failed — aborting build.
    exit /b %errorlevel%
)

go build -ldflags "-H windowsgui -X arx/test_records_go/config.AppVersion=%VERSION%" -o ArxTestRecords.exe .
if %errorlevel% neq 0 (
    echo Build failed.
    exit /b %errorlevel%
)
echo Built ArxTestRecords.exe v%VERSION%
