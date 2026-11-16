@echo off
rem Envia o codigo do Harvest para o GitHub (branch "desenvolvimento").
rem Usa o token de C:\Harvest-OCP\.segredos\github-token.txt e a mensagem de
rem .segredos\mensagem-commit.txt (o Claude deixa a mensagem pronta a cada entrega).
rem Nada vai para o "main": para publicar no Railway, faca o merge no GitHub.
setlocal
cd /d "%~dp0"

where git >nul 2>&1 || (echo O Git nao esta instalado nesta maquina. & pause & exit /b 1)
if not exist ".segredos\github-token.txt" (echo Falta .segredos\github-token.txt & pause & exit /b 1)
set /p TOKEN=<".segredos\github-token.txt"

echo == Limpando travas e temporarios ==
if exist ".git\index.lock" del /f ".git\index.lock"
rem So os temporarios "arquivo.go.<numeros>" (o curinga *.go.* do cmd apagaria tambem os .go!)
powershell -NoProfile -Command "Get-ChildItem -Path internal -Recurse -File | Where-Object { $_.Name -match '\.go\.\d+$' } | Remove-Item -Force"
rem trava: se faltar codigo, nao envia nada
if not exist "internal\servidor\servidor.go" (echo ERRO: faltam os .go em internal\servidor - nada foi enviado. & pause & exit /b 1)
if not exist "internal\pdf\pdf.go" (echo ERRO: faltam os .go em internal\pdf - nada foi enviado. & pause & exit /b 1)
git config core.filemode false
git config user.name "Daniel Costa"
git config user.email "z-Shieda@users.noreply.github.com"

echo == Preparando o branch desenvolvimento ==
git fetch -q origin
git checkout -B desenvolvimento
git add -A
rem script local com caminhos da rede interna: fica fora do repositorio publico
git reset -q -- Verificar-Seguranca-Harvest.ps1 2>nul

if exist ".segredos\mensagem-commit.txt" (
  git commit -q -F ".segredos\mensagem-commit.txt"
) else (
  git commit -q -m "Harvest - atualizacao"
)

echo == Enviando ==
git push -u "https://x-access-token:%TOKEN%@github.com/z-Shieda/Harvest-OCP.git" desenvolvimento
if errorlevel 1 (echo. & echo FALHOU o envio. Veja a mensagem acima. & pause & exit /b 1)

echo.
echo Pronto. Para publicar no Railway: GitHub ^> Harvest-OCP ^> "Compare ^& pull request" ^> Merge para o main.
pause
