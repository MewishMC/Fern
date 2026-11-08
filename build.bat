@echo off
windres -o .\Fern\Fern.syso .\Fern\Fern.rc
if errorlevel 1 goto :error

go generate .\Fern\
if errorlevel 1 goto :error

go build -ldflags "-H windowsgui" .\Fern\
if errorlevel 1 goto :error

go build -ldflags "-H windowsgui" .\update\
if errorlevel 1 goto :error

echo Build complete.
exit /b 0

:error
echo Build failed with error code %errorlevel%.
exit /b %errorlevel%
