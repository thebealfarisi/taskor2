"use client";

import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { clearClientSessionData } from "@multica/core/platform";
import { configStore } from "@multica/core/config";
import { paths } from "@multica/core/paths";
import { useNavigation } from "../navigation";

/**
 * Performs a complete logout: clears per-workspace client storage, legacy
 * cookies, the desktop tab state, the entire React Query cache, the
 * in-memory auth store, and finally navigates to /login. Wraps what was
 * previously duplicated in app-sidebar's logout handler so NoAccessPage's
 * "Sign in as a different user" and any future entry point can use the
 * same flow.
 *
 * Without a unified logout, callers that only do `navigate('/login')`
 * leave the auth cookie + React Query cache + local storage intact —
 * AuthInitializer then silently re-authenticates the user on the login
 * page and redirects them back where they came from.
 *
 * When Keycloak SSO is enabled (configStore.ssoEnabled), the final
 * navigation is a full-page redirect to /auth/keycloak/logout instead of
 * a client-side push to /login. The backend clears the Multica cookies and
 * redirects to the Keycloak end_session_endpoint, which destroys the
 * Keycloak session (Single Logout) and bounces back to /login. This
 * prevents the user from being silently re-authenticated by an active
 * Keycloak session on the next /auth/keycloak/login.
 */
export function useLogout() {
  const queryClient = useQueryClient();
  const authLogout = useAuthStore((s) => s.logout);
  const { push } = useNavigation();

  return useCallback(() => {
    // Shared with the session-expiry path, which has to erase exactly the
    // same client-side state — see core's platform/session-cleanup for what
    // and why.
    clearClientSessionData(queryClient);
    authLogout();

    // When SSO is enabled, perform Single Logout via the backend endpoint
    // (full-page redirect — the backend 302s to Keycloak end_session_endpoint).
    // Otherwise navigate client-side to /login.
    if (configStore.getState().ssoEnabled) {
      window.location.href = "/auth/keycloak/logout";
      return;
    }
    push(paths.login());
  }, [queryClient, authLogout, push]);
}
