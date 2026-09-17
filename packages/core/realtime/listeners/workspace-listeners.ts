import type { QueryClient } from "@tanstack/react-query";
import type { StoreApi, UseBoundStore } from "zustand";
import type { AuthState } from "../../auth/store";
import type { WSClient } from "../../api/ws-client";
import { createLogger } from "../../logger";
import { clearWorkspaceStorage } from "../../platform/storage-cleanup";
import { defaultStorage } from "../../platform/storage";
import { getCurrentWsId, getCurrentSlug } from "../../platform/workspace-storage";
import { isWorkspaceDeletePending } from "../../workspace/pending-delete";
import { workspaceKeys } from "../../workspace/queries";
import {
  applyWorkspaceUpdatedToCache,
  relocateAfterWorkspaceLoss,
} from "../workspace-sync";
import type {
  InvitationCreatedPayload,
  MemberAddedPayload,
  MemberRemovedPayload,
  WorkspaceDeletedPayload,
  WorkspaceUpdatedPayload,
} from "../../types";

const logger = createLogger("realtime-sync");

export function registerWorkspaceListeners(
  ws: WSClient,
  qc: QueryClient,
  authStore: UseBoundStore<StoreApi<AuthState>>,
  hasOnboardedRef: React.MutableRefObject<boolean>,
  onToast?: (message: string, type?: "info" | "error") => void,
): () => void {
  const unsubs: (() => void)[] = [];

  unsubs.push(
    ws.on("workspace:updated", (p) => {
      applyWorkspaceUpdatedToCache(qc, p as WorkspaceUpdatedPayload);
    }),
  );

  unsubs.push(
    ws.on("workspace:deleted", (p) => {
      const { workspace_id } = p as WorkspaceDeletedPayload;
      if (isWorkspaceDeletePending(workspace_id)) return;
      const wsList = qc.getQueryData<{ id: string; slug: string }[]>(workspaceKeys.list()) ?? [];
      const deletedSlug = wsList.find((w) => w.id === workspace_id)?.slug;
      if (deletedSlug) clearWorkspaceStorage(defaultStorage, deletedSlug);
      if (getCurrentWsId() === workspace_id) {
        logger.warn("current workspace deleted, switching");
        onToast?.("This workspace was deleted", "info");
        relocateAfterWorkspaceLoss(qc, workspace_id, hasOnboardedRef.current);
      }
    }),
  );

  unsubs.push(
    ws.on("member:removed", (p) => {
      const { user_id } = p as MemberRemovedPayload;
      const myUserId = authStore.getState().user?.id;
      if (user_id === myUserId) {
        const slug = getCurrentSlug();
        const wsId = getCurrentWsId();
        if (slug && wsId) {
          clearWorkspaceStorage(defaultStorage, slug);
          logger.warn("removed from workspace, switching");
          onToast?.("You were removed from this workspace", "info");
          relocateAfterWorkspaceLoss(qc, wsId, hasOnboardedRef.current);
        }
      }
    }),
  );

  unsubs.push(
    ws.on("member:added", (p) => {
      const { member, workspace_name } = p as MemberAddedPayload;
      const myUserId = authStore.getState().user?.id;
      if (member.user_id === myUserId) {
        qc.invalidateQueries({ queryKey: workspaceKeys.list() });
        qc.invalidateQueries({ queryKey: workspaceKeys.myInvitations() });
        onToast?.(
          `You joined ${workspace_name ?? "a workspace"}`,
          "info",
        );
      }
    }),
  );

  unsubs.push(
    ws.on("invitation:created", (p) => {
      const { workspace_name } = p as InvitationCreatedPayload;
      qc.invalidateQueries({ queryKey: workspaceKeys.myInvitations() });
      onToast?.(
        `You were invited to ${workspace_name ?? "a workspace"}`,
        "info",
      );
    }),
  );

  unsubs.push(
    ws.on("invitation:accepted", () => {
      const currentWsId = getCurrentWsId();
      if (currentWsId) {
        qc.invalidateQueries({ queryKey: workspaceKeys.invitations(currentWsId) });
        qc.invalidateQueries({ queryKey: workspaceKeys.members(currentWsId) });
      }
    }),
  );

  unsubs.push(
    ws.on("invitation:declined", () => {
      const currentWsId = getCurrentWsId();
      if (currentWsId) {
        qc.invalidateQueries({ queryKey: workspaceKeys.invitations(currentWsId) });
      }
    }),
  );

  unsubs.push(
    ws.on("invitation:revoked", () => {
      qc.invalidateQueries({ queryKey: workspaceKeys.myInvitations() });
    }),
  );

  return () => {
    for (const unsub of unsubs) unsub();
  };
}
