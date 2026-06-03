@echo off
:: Run all tests across the workspace (parts_master_go, test_records_go, arxlib).
:: Run from repo root: test.bat
pushd "%~dp0"
go test ./...
popd
