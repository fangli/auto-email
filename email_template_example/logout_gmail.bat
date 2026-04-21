@echo off
setlocal

REM If checks pass, run the command
.\gws.exe auth logout

echo.
echo -----------------------------
echo You have been logged out successfully.
echo If you want to sign-in again with different Gmail account, run login_gmail.bat
echo -----------------------------
echo.
pause