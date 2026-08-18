"use client";

import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { clearWorkspaceStorage, defaultStorage } from "@multica/core/platform";
import { configStore } from "@multica/core/config";
import { resetAllRegisteredDrafts } from "@multica/core/drafts/cleanup-registry";
import { paths } from "@multica/core/paths";
import type { Workspace } from "@multica/core/types";
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
    // Reset draft stores' in-memory state FIRST, before removing persisted
    // keys. Each reset is a Zustand setState, and persist middleware writes
    // the new (empty) state straight back to storage under the still-active
    // workspace slug — so resetting after removal would resurrect the very
    // keys just deleted, with whatever the reset state still carries. Memory
    // must be wiped regardless of the workspace list below: a client-side
    // navigation to /login does not reload the page, so the singletons would
    // otherwise surface the previous user's draft after the next login.
    resetAllRegisteredDrafts();

    // Then clear workspace-scoped storage for every workspace this user has
    // access to, BEFORE clearing the React Query cache (which holds the
    // workspace list). Otherwise per-workspace drafts/chat/etc would leak
    // to the next user on this device.
    const cachedWorkspaces =
      queryClient.getQueryData<Workspace[]>(workspaceKeys.list()) ?? [];
    for (const ws of cachedWorkspaces) {
      clearWorkspaceStorage(defaultStorage, ws.slug);
    }

    // Clear the last-workspace-slug cookie. Otherwise on a shared device
    // the next user gets redirected by the proxy to the previous user's
    // last workspace, then bounced to NoAccessPage — confusing.
    if (typeof document !== "undefined") {
      document.cookie =
        "last_workspace_slug=; path=/; max-age=0; SameSite=Lax";
    }

    // Clear desktop tab state. Tab paths can contain workspace slugs and
    // issue UUIDs that must not survive across user sessions on a shared
    // machine. No-op on web (web doesn't write this key).
    defaultStorage.removeItem("multica_tabs");

    queryClient.clear();
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
