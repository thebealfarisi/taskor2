# Implementation Plan: Redirect Root Domain to Keycloak (Larasati) SSO

## Objective

Change the behavior when users visit `super-presales.lintasarta.co.id` (root `/`) so they are redirected to the **Keycloak SSO login page (Larasati)** instead of the application's native `/login` page.

## Current State

- Visiting `super-presales.lintasarta.co.id` → redirects to `/login` (native email-based login)
- The application already has **Keycloak SSO integration** built-in:
  - Backend endpoints: `/auth/keycloak/login`, `/auth/keycloak/callback`, `/auth/keycloak/logout`
  - OIDC flow with PKCE support
  - The issuer is configured via environment variables

## Proposed Changes

### 1. Frontend: Change Root Redirect

#### [MODIFY] `apps/web/app/(landing)/page.tsx`

Change the redirect from `/login` to `/auth/keycloak/login`.

```tsx
// BEFORE
export default function LandingPage() {
  redirect("/login");
}

// AFTER
export default function LandingPage() {
  redirect("/auth/keycloak/login");
}
```

This ensures anyone visiting the root domain is immediately sent to the Keycloak authorization flow.

### 2. Backend: Ensure SSO Environment Variables Are Configured

The Keycloak SSO feature is **env-gated**. The following environment variables must be set in `/home/multica/multica/.env`:

| Variable | Required | Description | Example |
|----------|----------|-------------|---------|
| `MULTICA_SSO_ENABLED` | ✅ | Must be `true` to enable SSO | `true` |
| `MULTICA_SSO_KEYCLOAK_ISSUER` | ✅ | Keycloak realm URL | `https://larasati.lintasarta.co.id/realms/dev` |
| `MULTICA_SSO_CLIENT_ID` | ✅ | Keycloak client ID | `super-presales` |
| `MULTICA_SSO_CLIENT_SECRET` | ✅ | Keycloak client secret | `<your-secret>` |
| `MULTICA_SSO_REDIRECT_URL` | ✅ | Callback URL after Keycloak auth | `https://super-presales.lintasarta.co.id/auth/keycloak/callback` |
| `MULTICA_SSO_SKIP_TLS_VERIFY` | ❌ | Skip TLS verify (dev only) | `false` |

> [!IMPORTANT]
> If `MULTICA_SSO_ENABLED` is not `true`, the `/auth/keycloak/login` endpoint will return **404 Not Found**.

### 3. Post-Login Redirect Flow

After successful Keycloak authentication:
1. User is redirected to `/auth/keycloak/callback?code=...&state=...`
2. Backend exchanges the code for tokens, creates/logs in the user
3. Backend sets auth cookies
4. Backend redirects user to the `next` URL (or `/` if no `next` was specified)

The default redirect after login is `/` (root), which will then redirect back to `/auth/keycloak/login` — this is fine because the user is now authenticated and the Keycloak flow will detect the existing session.

**Alternative**: If you want users to land on the dashboard after login, you can pass a `next` parameter:
```tsx
redirect("/auth/keycloak/login?next=/workspaces");
```

## Verification Plan

### Manual Verification
1. Open browser in incognito mode
2. Visit `https://super-presales.lintasarta.co.id`
3. Verify you are redirected to `https://larasati.lintasarta.co.id` (Keycloak login page)
4. Log in with Keycloak credentials
5. Verify you are redirected back to the app and logged in successfully

### API Test
```bash
# Test that the Keycloak login endpoint is accessible
curl -I https://super-presales.lintasarta.co.id/auth/keycloak/login
# Expected: 302 redirect to Keycloak authorization URL
```

## Build & Deploy Guide

### Step 1: Update Environment Variables

Edit `/home/multica/multica/.env` and ensure SSO variables are set:

```bash
MULTICA_SSO_ENABLED=true
MULTICA_SSO_KEYCLOAK_ISSUER=https://larasati.lintasarta.co.id/realms/dev
MULTICA_SSO_CLIENT_ID=super-presales
MULTICA_SSO_CLIENT_SECRET=<your-keycloak-client-secret>
MULTICA_SSO_REDIRECT_URL=https://super-presales.lintasarta.co.id/auth/keycloak/callback
```

### Step 2: Rebuild Frontend

```bash
cd /home/multica/multica
pnpm --filter web build
```

### Step 3: Rebuild Backend

```bash
cd /home/multica/multica/server
go build -o bin/server ./cmd/server
```

### Step 4: Restart Services

```bash
sudo systemctl restart multica-frontend
sudo systemctl restart multica-backend
```

### Step 5: Verify

```bash
# Check backend logs for SSO initialization
sudo journalctl -u multica-backend -f

# Test the redirect
curl -I https://super-presales.lintasarta.co.id
```

## Rollback Plan

If issues occur, revert the change in `apps/web/app/(landing)/page.tsx`:

```tsx
export default function LandingPage() {
  redirect("/login");
}
```

Then rebuild frontend and restart:
```bash
pnpm --filter web build
sudo systemctl restart multica-frontend
```

## Security Notes

- The `next` parameter is sanitized to prevent open-redirect attacks
- Keycloak state cookies are signed with the JWT secret
- PKCE (Proof Key for Code Exchange) is used for the OIDC flow
- Ensure `MULTICA_SSO_REDIRECT_URL` exactly matches the callback URL registered in Keycloak
