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
1. Navigate to the server folder:
   ```bash
   cd server
   ```
2. Rebuild the backend binary (adjust the output name if necessary):
   ```bash
   go build -o multica.exe ./cmd/server
   ```
3. Restart your backend service:
   ```bash
   sudo systemctl restart <your-backend-service-name>
   ```

*(Note: If you are just testing in a local development environment instead of production, you can simply stop your current processes and run `make dev` or `make start` to automatically rebuild and run the changes).*
