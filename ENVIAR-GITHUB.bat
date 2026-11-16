@echo off
rem Envia o codigo do Harvest DIRETO para o branch "main" do GitHub.
rem ATENCAO: o Railway publica a partir do main, entao o envio vai ao ar.
rem Usa o token de .segredos\github-token.txt e a mensagem de .segredos\mensagem-commit.txt.
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
if not exist "cmd\servidor\main.go" (echo ERRO: falta cmd\servidor\main.go - nada foi enviado. & pause & exit /b 1)
git config core.filemode false
git config user.name "Daniel Costa"
git config user.email "z-Shieda@users.noreply.github.com"

echo == Preparando o main (sem mexer nos arquivos da pasta) ==
git fetch -q origin || (echo Nao consegui falar com o GitHub. & pause & exit /b 1)
rem aponta o branch main para o main do GitHub e deixa a pasta como esta
git update-ref refs/heads/main origin/main
git symbolic-ref HEAD refs/heads/main
git reset -q
git add -A
rem script local com caminhos da rede interna: fica fora do repositorio publico
git reset -q -- Verificar-Seguranca-Harvest.ps1 2>nul

git diff --cached --quiet && (echo Nada novo para enviar. & pause & exit /b 0)
if exist ".segredos\mensagem-commit.txt" (
  git commit -q -F ".segredos\mensagem-commit.txt"
) else (
  git commit -q -m "Harvest - atualizacao"
)

echo == Enviando para o main (o Railway publica em seguida) ==
git push "https://x-access-token:%TOKEN%@github.com/z-Shieda/Harvest-OCP.git" main
if errorlevel 1 (echo. & echo FALHOU o envio. Veja a mensagem acima. & pause & exit /b 1)
git branch --set-upstream-to=origin/main main >nul 2>&1

echo.
echo Pronto: enviado para o main. Acompanhe o deploy no Railway.
pause
