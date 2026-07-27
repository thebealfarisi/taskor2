"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuthStore } from "@multica/core/auth";
import { resolvePostAuthDestination, useHasOnboarded } from "@multica/core/paths";
import { api } from "@multica/core/api";
import type { Workspace } from "@multica/core/types";

export default function LandingPage() {
  const router = useRouter();
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const hasOnboarded = useHasOnboarded();
  const [resolved, setResolved] = useState(false);

  useEffect(() => {
    if (isLoading || resolved) return;

    if (user) {
      // Already authenticated — resolve the correct workspace destination
      api.listWorkspaces()
        .then((workspaces: Workspace[]) => {
          const dest = resolvePostAuthDestination(workspaces, hasOnboarded);
          router.replace(dest);
        })
        .catch(() => {
          // Fallback to workspaces list page if API fails
          router.replace("/workspaces");
        })
        .finally(() => setResolved(true));
    } else {
      // Not authenticated — redirect to Keycloak SSO
      window.location.href = "/auth/keycloak/login";
      setResolved(true);
    }
  }, [isLoading, user, hasOnboarded, router, resolved]);

  return null;
}
