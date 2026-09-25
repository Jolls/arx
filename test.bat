@echo off
:: Run all tests in the module.
:: Run from repo root: test.bat
pushd "%~dp0"
go test ./...
popd
