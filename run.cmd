@echo off
setlocal
cd /d "%~dp0"

if "%ADDR%"=="" set "ADDR=:8090"
if "%DB_PATH%"=="" set "DB_PATH=data\study.db"

echo.
echo   Listen    : %ADDR%   (LAN enabled)
echo   Local     : http://127.0.0.1:8090
echo   Phone     : http://^<this-PC-IP^>:8090  (same Wi-Fi)
echo   Database  : %DB_PATH%
echo   First run compiles, please wait...
echo   Press Ctrl+C to stop.
echo.

go run .
