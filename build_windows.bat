@echo off
setlocal
cd /d %~dp0
if not exist dist mkdir dist
set GOOS=windows
set GOARCH=amd64
go build -ldflags "-H=windowsgui -s -w" -o "dist\微损压缩JPG.exe" .
echo.
echo Build finished: dist\微损压缩JPG.exe
pause
