@echo off
setlocal

REM Check if .env exists
if not exist ".env" (
    echo Error: .env file not found in current directory.
    pause
    exit /b 1
)

REM Search for placeholder values
findstr /C:"YOUR_CLIENT_ID" ".env" >nul
if %errorlevel%==0 goto invalid_env

findstr /C:"YOUR_CLIENT_SECRET" ".env" >nul
if %errorlevel%==0 goto invalid_env

REM If checks pass, run the command
.\gws.exe auth login -s gmail
goto end

:invalid_env
echo.
echo -----------------------------
echo You haven't filled out the .env file yet.
echo Please create a Googleworkspace Project with Gmail API enabled at https://console.cloud.google.com/, then add a Desktop App OAuth Client, and put the client_id and client_secret in .env file
echo Once completed, run this script again to login.
echo -----------------------------
echo.
pause
exit /b 1

:end
endlocal