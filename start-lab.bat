@echo off
cd /d "%~dp0"
echo Starting strategy lab (cmd/lab). Close window or press Ctrl+C to stop.
go run ./cmd/lab
if errorlevel 1 pause
