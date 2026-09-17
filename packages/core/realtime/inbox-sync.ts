import type { QueryClient } from "@tanstack/react-query";
import { onInboxNew, onInboxSummaryInvalidate } from "../inbox/ws-updaters";
import {
  notificationPreferenceKeys,
  notificationPreferenceOptions,
} from "../notification-preferences/queries";
import {
  showWebNotification,
  type SystemNotificationPayload,
} from "../platform/system-notification";
import type { InboxItem, NotificationPreferenceResponse } from "../types";
import { workspaceListOptions } from "../workspace/queries";

/**
 * Resolves the slug of the workspace an inbox item originated from, via the
 * cached workspace list (fetched once when the cache is cold).
 *
 * Desktop notification routing must pin to the *source* workspace of the
 * inbox item, not the currently active one: the user can be on workspace B
 * when an `inbox:new` for workspace A arrives, and macOS Notification Center
 * holds banners across workspace switches. Returns null when the workspace
 * cannot be resolved — callers must NOT fall back to the current slug (that
 * recreates the wrong-workspace routing this exists to prevent, #3766) and
 * should show the notification without a deep link instead.
 */
export async function resolveInboxSourceSlug(
  qc: QueryClient,
  workspaceId: string,
): Promise<string | null> {
  if (!workspaceId) return null;
  try {
    const workspaces = await qc.ensureQueryData(workspaceListOptions());
    return workspaces?.find((w) => w.id === workspaceId)?.slug ?? null;
  } catch {
    // Workspace list unavailable (e.g. network hiccup): degrade to a
    // link-less notification rather than guessing a slug.
    return null;
  }
}

/**
 * Handles an `inbox:new` event end-to-end: inbox cache invalidation, the
 * focus / mute checks, and the native OS banner. Exported so the handler
 * behavior (not just slug resolution) is testable.
 *
 * Every workspace-scoped read here keys on the ITEM's workspace
 * (`item.workspace_id`), never the currently active one (#3766): the cache
 * invalidation must refresh the source workspace's inbox list / unread
 * count / dock badge, the mute check must honor the source workspace's
 * preference, and the deep link must carry the source workspace's slug.
 */
export async function handleInboxNew(
  qc: QueryClient,
  item: InboxItem,
): Promise<void> {
  const sourceWsId = item.workspace_id;
  if (sourceWsId) onInboxNew(qc, sourceWsId, item);
  // A new item in ANY workspace can light the workspace-switcher dot, so
  // refresh the cross-workspace summary regardless of the active workspace.
  onInboxSummaryInvalidate(qc);
  // Fire a native OS notification only when the app isn't focused. When
  // the user is already looking at Multica, the inbox sidebar's unread
  // styling is enough — no need to interrupt with a banner. `desktopAPI`
  // is injected by the preload script; its absence (web app) skips silently.
  if (typeof document !== "undefined" && document.hasFocus()) return;
  // Resolve the source workspace's slug once: it pins BOTH the mute check
  // and the deep link to the workspace the inbox item BELONGS to, never the
  // currently active one. Reading `getCurrentSlug()` here was the source of
  // wrong-workspace routing (#3766): an `inbox:new` from workspace A arriving
  // while workspace B is active emitted a notification carrying B's slug and
  // A's issue id, deep-linking to an issue B doesn't have.
  const slug = await resolveInboxSourceSlug(qc, sourceWsId);
  // Respect the SOURCE workspace's system-notification preference. Keying the
  // query on `sourceWsId` is not enough: the request resolves its workspace
  // from the `X-Workspace-Slug` header, which follows the ACTIVE workspace —
  // so a cold-cache lookup while viewing B would read B's mute setting and
  // cache it under A's key. Passing the source slug scopes the fetch to A.
  // When the slug can't be resolved we read only an already-warm cache
  // (populated earlier with the correct workspace context) rather than fetch
  // with the wrong one; on network failure we fall through to the default
  // ("all") rather than swallow the banner.
  if (sourceWsId) {
    try {
      const prefData = slug
        ? await qc.ensureQueryData(
            notificationPreferenceOptions(sourceWsId, slug),
          )
        : qc.getQueryData<NotificationPreferenceResponse>(
            notificationPreferenceKeys.all(sourceWsId),
          );
      if (prefData?.preferences?.system_notifications === "muted") return;
    } catch {
      // Fall through with default behavior.
    }
  }
  // `issueKey` matches the inbox page's URL selector (issue id when the
  // item is attached to an issue, otherwise the inbox item id). `itemId`
  // is the inbox row's own id, needed to fire markInboxRead on click.
  // A null slug (workspace list unavailable / item from a workspace this
  // client can't see) still shows the banner — the user should learn about
  // the inbox item — but with an empty slug so the click is a no-op
  // (the inbox bridge ignores empty slugs) instead of routing wrong.
  const payload: SystemNotificationPayload = {
    slug: slug ?? "",
    itemId: item.id,
    issueKey: item.issue_id ?? item.id,
    title: item.title,
    body: item.body ?? "",
  };
  const desktopAPI = (
    globalThis as unknown as {
      desktopAPI?: {
        showNotification?: (payload: SystemNotificationPayload) => void;
      };
    }
  ).desktopAPI;
  if (desktopAPI?.showNotification) {
    // Desktop: native OS banner rendered by the Electron main process.
    desktopAPI.showNotification(payload);
    return;
  }
  // Web: the browser Notification API. No-op without granted permission or on
  // SSR — the in-app inbox + unread badge still reflect the new item.
  showWebNotification(payload);
}
