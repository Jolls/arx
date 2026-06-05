@echo off
:: Run all tests across the workspace (arx_go, arxlib).
:: Run from repo root: test.bat
pushd "%~dp0"
go test ./arx_go/... ./arxlib/...
popd
