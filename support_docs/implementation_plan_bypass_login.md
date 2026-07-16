# Rebranding and Login Bypass Implementation Plan

This document outlines the steps to fulfill your request: rebranding "Multica" to "Task-Or" and implementing a complete login bypass to avoid the verification code step entirely.

## User Review Required

> [!WARNING]  
> Bypassing login authentication poses significant security risks in a production environment. I will implement a bypass that allows **any** email to instantly log in without identity verification. Please be aware this completely disables standard identity verification for all users.

## Proposed Changes

### 1. Rebranding to "Task-Or"

To execute the rebranding from "Multica" to "Task-Or", we will need to update user-facing text strings primarily found in localization files and metadata.

#### [MODIFY] [packages/views/locales/en/common.json](file:///d:/Kerjaan/Project/taskor2/packages/views/locales/en/common.json)
Update global branding text from "Multica" to "Task-Or".

#### [MODIFY] [apps/web/app/(landing)/page.tsx](file:///d:/Kerjaan/Project/taskor2/apps/web/app/(landing)/page.tsx)
Update metadata title and descriptions from "Multica" to "Task-Or".

#### [MODIFY] [packages/views/locales/en/auth.json](file:///d:/Kerjaan/Project/taskor2/packages/views/locales/en/auth.json)
Update login page branding from "Multica" to "Task-Or" (signin title, CLI description, desktop handoff text).

#### [MODIFY] [packages/views/locales/zh-Hans/auth.json](file:///d:/Kerjaan/Project/taskor2/packages/views/locales/zh-Hans/auth.json)
Same rebranding for Chinese locale.

#### [MODIFY] [packages/views/locales/ko/auth.json](file:///d:/Kerjaan/Project/taskor2/packages/views/locales/ko/auth.json)
Same rebranding for Korean locale.

#### [MODIFY] [packages/views/locales/ja/auth.json](file:///d:/Kerjaan/Project/taskor2/packages/views/locales/ja/auth.json)
Same rebranding for Japanese locale.

### 2. Login Bypass Implementation (Auto-Login)

To completely bypass the verification step in the user interface as requested, we will update both the backend and frontend so that submitting the email immediately logs the user in.

#### [MODIFY] [server/internal/handler/auth.go](file:///d:/Kerjaan/Project/taskor2/server/internal/handler/auth.go)
- Modify the `VerifyCode` function to remove the strict code validation check (so it accepts any code, e.g., `000000`).

#### [MODIFY] [packages/views/auth/login-page.tsx](file:///d:/Kerjaan/Project/taskor2/packages/views/auth/login-page.tsx)
- Rearrange the component so `handleVerify` is defined before `handleSendCode`.
- Modify `handleSendCode` so that immediately after successfully requesting a code, it automatically calls `handleVerify("000000")`. This entirely skips the OTP UI step and seamlessly logs the user in.

## Verification Plan

### Manual Verification
1. Open the web app.
2. Observe the branding is now "Task-Or" on the page title.
3. Go to the login page.
4. Enter any email address and click "Continue".
5. Verify that you are **immediately logged in** and redirected to the workspace, completely skipping the verification code input step.

## Setup & Testing Guide

Because this application relies on a compiled Go backend and a bundled Next.js frontend, any code changes require rebuilding the application before they take effect. If you are deploying this to a server (or testing locally), follow these steps:

### 1. Rebuild and Restart Frontend (Next.js)
The frontend changes (such as the bypass and translation updates) require a fresh production build.
1. Open your terminal and navigate to the project root (`d:\Kerjaan\Project\taskor2`).
2. Run the build command for the web app:
   ```bash
   pnpm --filter web build
   ```
3. Restart your frontend service (e.g., if you are using systemctl):
   ```bash
   sudo systemctl restart <your-frontend-service-name>
   ```

### 2. Rebuild and Restart Backend (Go)
The backend change in `auth.go` requires the Go binary to be recompiled.

> [!IMPORTANT]
> The `go build` output **must match the path** configured in your systemd service's `ExecStart`. If the service runs `/home/multica/multica/server/bin/server`, you must build to that exact path — otherwise the service will keep running the old binary.

1. Check your systemd service to find the correct binary path:
   ```bash
   systemctl cat <your-backend-service-name>
   # Look for ExecStart= — e.g. ExecStart=/home/multica/multica/server/bin/server
   ```
2. Navigate to the server folder:
   ```bash
   cd server
   ```
3. Rebuild the backend binary to the **exact path** from step 1:
   ```bash
   # Linux/macOS (match the ExecStart path from systemd):
   go build -o bin/server ./cmd/server
   # Windows:
   go build -o bin/server.exe ./cmd/server
   ```
4. Restart your backend service:
   ```bash
   sudo systemctl restart <your-backend-service-name>
   ```
5. Verify the bypass is active by calling the API directly:
   ```bash
   curl -X POST http://localhost:<PORT>/auth/verify-code \
     -H "Content-Type: application/json" \
     -d '{"email":"test@example.com","code":"000000"}'
   ```
   If this returns a token, the backend bypass is working. If it returns "invalid or expired code", the service is still running the old binary — double-check the build output path and restart again.

*(Note: If you are just testing in a local development environment instead of production, you can simply stop your current processes and run `make dev` or `make start` to automatically rebuild and run the changes).*
