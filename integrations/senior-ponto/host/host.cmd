@echo off
rem Ponto de entrada do native messaging: o Edge só executa .exe, .cmd e .bat.
powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "%~dp0host.ps1"
