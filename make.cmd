@echo off
setlocal

set BIN=bin
set PKG=.\cmd\toss

if "%~1"=="" goto run
if "%~1"=="run" goto run
if "%~1"=="build" goto build
if "%~1"=="build-all" goto build-all
if "%~1"=="test" goto test
if "%~1"=="clean" goto clean
if "%~1"=="vendor" goto vendor

echo Usage: make [run^|build^|build-all^|test^|clean^|vendor]
exit /b 1

:run
go run %PKG%
exit /b %errorlevel%

:build
if not exist %BIN% mkdir %BIN%
go build -o %BIN%\toss.exe %PKG%
exit /b %errorlevel%

:build-all
if not exist %BIN% mkdir %BIN%
echo Building for all platforms...

echo   darwin/arm64
set GOOS=darwin& set GOARCH=arm64& go build -o %BIN%\toss-darwin-arm64 %PKG%
if errorlevel 1 goto fail

echo   darwin/amd64
set GOOS=darwin& set GOARCH=amd64& go build -o %BIN%\toss-darwin-amd64 %PKG%
if errorlevel 1 goto fail

echo   windows/amd64
set GOOS=windows& set GOARCH=amd64& go build -o %BIN%\toss-windows-amd64.exe %PKG%
if errorlevel 1 goto fail

echo   linux/amd64
set GOOS=linux& set GOARCH=amd64& go build -o %BIN%\toss-linux-amd64 %PKG%
if errorlevel 1 goto fail

set GOOS=& set GOARCH=
echo.
dir /b %BIN%\toss-*
exit /b 0

:test
go test -v -count=1 %PKG%
exit /b %errorlevel%

:clean
if exist %BIN% rmdir /s /q %BIN%
echo Cleaned.
exit /b 0

:vendor
cd cmd\toss\web\vendor
curl -sfL -o highlight.min.js  "https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/highlight.min.js"
curl -sfL -o github-dark.min.css "https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/styles/github-dark.min.css"
curl -sfL -o marked.min.js     "https://cdnjs.cloudflare.com/ajax/libs/marked/12.0.1/marked.min.js"
curl -sfL -o purify.min.js     "https://cdnjs.cloudflare.com/ajax/libs/dompurify/3.0.9/purify.min.js"
echo JS/CSS updated. Fonts: edit font URLs in fonts.css manually if needed.
exit /b 0

:fail
set GOOS=& set GOARCH=
echo Build failed.
exit /b 1
