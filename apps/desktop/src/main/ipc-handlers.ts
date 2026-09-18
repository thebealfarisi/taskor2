import {
  app,
  BrowserWindow,
  ipcMain,
  Notification,
} from "electron";
import { openExternalSafely, downloadURLSafely } from "./external-url";
import { getAppVersion } from "./app-version";
import {
  readFreezeBreadcrumb,
  ackFreezeBreadcrumb,
} from "./freeze-breadcrumb";
import {
  RENDERER_ROUTE_CONTEXT_CHANNEL,
  sanitizeRendererRouteContext,
  type RendererRouteContext,
} from "../shared/renderer-route-context";
import {
  AUTH_SESSION_STATE_CHANNEL,
  parseAuthSessionUserId,
} from "../shared/auth-session";
import {
  MAIN_RENDERER_CHANNEL_STATE_CHANNEL,
  parseMainRendererChannelState,
  type MainRendererMessageChannel,
} from "../shared/main-renderer-messages";
import { parseNativeNotificationPayload } from "./notification-gate";
import type { RuntimeConfigResult } from "../shared/runtime-config";
import type { AuthSessionCoordinator } from "./auth-session-coordinator";
import type { NotificationGate } from "./notification-gate";
import type { MainRendererMessageQueue } from "../shared/main-renderer-messages";
import type { IssueWindowManager } from "./issue-window-manager";

// ---------------------------------------------------------------------------
// Runtime config result — shared mutable state between index.ts (writer) and
// the "runtime-config:get" IPC handler (reader). Kept here so ipc-handlers.ts
// is self-contained and the handler closure can read without a dep cycle.
// ---------------------------------------------------------------------------

let _runtimeConfigResult: RuntimeConfigResult = {
  ok: false,
  error: { message: "Runtime config has not loaded yet" },
};

export function setRuntimeConfigResult(result: RuntimeConfigResult): void {
  _runtimeConfigResult = result;
}

function getRuntimeConfigResult(): RuntimeConfigResult {
  return _runtimeConfigResult;
}

// ---------------------------------------------------------------------------
// Dependency surface
// ---------------------------------------------------------------------------

export interface IpcHandlerDeps {
  /** Returns the current main BrowserWindow, or null if not created yet. */
  getMainWindow: () => BrowserWindow | null;
  /** Resolves the freeze breadcrumb path. */
  freezeBreadcrumbPath: () => string;
  /** Stable ref to the renderer route context map. */
  rendererRouteContexts: WeakMap<Electron.WebContents, RendererRouteContext>;
  authSessionCoordinator: AuthSessionCoordinator<BrowserWindow>;
  notificationGate: NotificationGate;
  mainRendererMessages: MainRendererMessageQueue;
  issueWindowManager: IssueWindowManager;
  /** Returns the current auth session generation counter. */
  getAuthSessionGeneration: () => number;
  /** Atomically bumps the auth session generation counter. */
  incrementAuthSessionGeneration: () => void;
  /** Dispatch a message through the main-renderer message queue. */
  dispatchToMainRenderer: (
    channel: MainRendererMessageChannel,
    payload: unknown,
  ) => void;
  sendMainRendererMessage: (
    channel: MainRendererMessageChannel,
    payload: unknown,
  ) => void;
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

/**
 * Register all ipcMain handlers for the desktop main process.
 * Must be called inside `app.whenReady()` after the main window has been
 * set up, so all dependent modules are fully initialised.
 */
export function registerIpcHandlers(deps: IpcHandlerDeps): void {
  const {
    getMainWindow,
    freezeBreadcrumbPath,
    rendererRouteContexts,
    authSessionCoordinator,
    notificationGate,
    mainRendererMessages,
    issueWindowManager,
    getAuthSessionGeneration,
    incrementAuthSessionGeneration,
    dispatchToMainRenderer,
    sendMainRendererMessage,
  } = deps;

  // IPC: open URL in default browser (used by renderer for Google login).
  // All scheme-allowlist enforcement lives in openExternalSafely — this
  // is the single audit point for renderer-controlled URLs reaching the
  // OS shell under the app's intentional webSecurity: false configuration
  // (the renderer itself runs sandboxed).
  ipcMain.handle("shell:openExternal", (_event, url: string) => {
    return openExternalSafely(url);
  });

  // Renderer requests its own window close (e.g. Cmd+W on the last main
  // tab, or Cmd+W anywhere in a dedicated issue window).
  ipcMain.on("window:close", (event) => {
    BrowserWindow.fromWebContents(event.sender)?.close();
  });

  ipcMain.handle("window:open-issue", (event, request: unknown) => {
    if (!BrowserWindow.fromWebContents(event.sender)) {
      return { ok: false, reason: "invalid_request" } as const;
    }
    const ok = issueWindowManager.openIssueWindow(request);
    return ok
      ? ({ ok: true } as const)
      : ({ ok: false, reason: "invalid_request" } as const);
  });

  ipcMain.handle("file:download-url", (event, url: string) => {
    const sourceWindow = BrowserWindow.fromWebContents(event.sender);
    if (!sourceWindow) {
      console.warn(
        "[download] ignored file:download-url — source window torn down",
      );
      return;
    }
    downloadURLSafely(sourceWindow, url);
  });

  // Sync IPC: app version + normalized OS for preload. Sync (not invoke) so
  // preload can attach the values to `desktopAPI.appInfo` before any renderer
  // code reads them, ensuring the very first HTTP request from the renderer
  // already carries X-Client-Version and X-Client-OS.
  ipcMain.on("app:get-info", (event) => {
    const p = process.platform;
    const os =
      p === "darwin"
        ? "macos"
        : p === "win32"
          ? "windows"
          : p === "linux"
            ? "linux"
            : "unknown";
    event.returnValue = { version: getAppVersion(), os };
  });

  // Sync IPC: read + clear any freeze/crash breadcrumb left by a previous
  // session. The renderer flushes it to telemetry on boot (it couldn't be
  // reported when it happened — the renderer was hung or gone). Read-and-
  // clear so a failure reports exactly once.
  ipcMain.on("freeze:get-last", (event) => {
    event.returnValue = readFreezeBreadcrumb(freezeBreadcrumbPath());
  });

  // The renderer got its breadcrumb event to posthog — retire that exact
  // payload. A newer failure recorded since the read keeps its own ts and
  // survives to be reported on the next boot.
  ipcMain.on("freeze:ack", (event, ts: unknown) => {
    if (!BrowserWindow.fromWebContents(event.sender)) return;
    if (typeof ts !== "number" || !Number.isFinite(ts)) return;
    ackFreezeBreadcrumb(freezeBreadcrumbPath(), ts);
  });

  // Sync IPC: preload exposes the validated runtime config before renderer
  // boot. If desktop.json exists but is invalid, renderer receives the
  // blocking error and must not silently fall back to the cloud defaults.
  ipcMain.on("runtime-config:get", (event) => {
    event.returnValue = getRuntimeConfigResult();
  });

  ipcMain.on(RENDERER_ROUTE_CONTEXT_CHANNEL, (event, context: unknown) => {
    if (!BrowserWindow.fromWebContents(event.sender)) return;
    const sanitized = sanitizeRendererRouteContext(context);
    if (!sanitized) return;
    rendererRouteContexts.set(event.sender, sanitized);
  });

  // Preload announces each listener only after it has been installed by the
  // main renderer. Ignore issue-window senders so they can never drain a
  // payload intended for the tabbed application window.
  ipcMain.on(
    MAIN_RENDERER_CHANNEL_STATE_CHANNEL,
    (event, state: unknown) => {
      const mainWindow = getMainWindow();
      if (!mainWindow || event.sender !== mainWindow.webContents) return;
      const parsed = parseMainRendererChannelState(state);
      if (!parsed) return;
      mainRendererMessages.setReady(
        parsed.channel,
        parsed.ready,
        sendMainRendererMessage,
      );
    },
  );

  // Account identity is the only cross-renderer auth signal. Main remains
  // authoritative and closes issue windows instead of copying credentials.
  ipcMain.on(AUTH_SESSION_STATE_CHANNEL, (event, value: unknown) => {
    const sourceWindow = BrowserWindow.fromWebContents(event.sender);
    const userId = parseAuthSessionUserId(value);
    if (!sourceWindow || userId === undefined) return;

    const mainWindow = getMainWindow();
    if (sourceWindow === mainWindow) {
      const accountInvalidated = authSessionCoordinator.reportMain(userId);
      if (accountInvalidated) {
        incrementAuthSessionGeneration();
        mainRendererMessages.clear("inbox:open");
      }
      return;
    }
    if (issueWindowManager.hasIssueWindow(sourceWindow)) {
      authSessionCoordinator.reportIssue(sourceWindow, userId);
    }
  });

  // IPC: toggle immersive mode — hides the macOS traffic lights so full-screen
  // modals (e.g. create-workspace) can place UI in the top-left corner
  // without fighting the native window controls' hit-test.
  ipcMain.handle("window:setImmersive", (event, immersive: boolean) => {
    if (process.platform !== "darwin") return;
    BrowserWindow.fromWebContents(event.sender)?.setWindowButtonVisibility(
      !immersive,
    );
  });

  // Main owns foreground detection and item-level dedupe. Every renderer
  // has its own WebSocket and `document.hasFocus()` only describes that one
  // window, so renderer-only gating can emit N duplicate system banners.
  ipcMain.on("notification:show", (event, value: unknown) => {
    const sourceWindow = BrowserWindow.fromWebContents(event.sender);
    if (!sourceWindow) return;

    const mainWindow = getMainWindow();
    if (sourceWindow === mainWindow) {
      if (!authSessionCoordinator.hasActiveMainSession()) return;
    } else if (
      !issueWindowManager.hasIssueWindow(sourceWindow) ||
      !authSessionCoordinator.isCurrentIssueSession(sourceWindow)
    ) {
      return;
    }

    const payload = parseNativeNotificationPayload(value);
    if (!payload || !Notification.isSupported()) return;

    const anyWindowFocused = BrowserWindow.getAllWindows().some(
      (window) => !window.isDestroyed() && window.isFocused(),
    );
    if (!notificationGate.shouldShow(payload.itemId, anyWindowFocused)) return;

    const notification = new Notification({
      title: payload.title,
      body: payload.body,
    });
    const notificationSessionGeneration = getAuthSessionGeneration();
    notification.on("click", () => {
      // A banner emitted for user A must not navigate after the main window
      // logs out or switches to user B.
      if (notificationSessionGeneration !== getAuthSessionGeneration()) return;
      // Recreate the main window when an issue-only window outlived it, then
      // wait for the inbox listener before delivering the navigation.
      dispatchToMainRenderer("inbox:open", {
        slug: payload.slug,
        itemId: payload.itemId,
        issueKey: payload.issueKey,
      });
    });
    notification.show();
  });

  // IPC: update the dock / taskbar unread badge. Values above 99 render as
  // "99+". macOS is the primary target (user-visible dock badge); Linux
  // Unity launchers also respect `setBadgeCount`. Windows' taskbar overlay
  // needs a pre-rendered PNG and is deferred — the OS notification + the
  // in-app inbox sidebar cover the core UX there for now.
  ipcMain.on("badge:set", (_event, rawCount: number) => {
    const count = Math.max(0, Math.floor(rawCount));
    if (process.platform === "darwin") {
      const label = count === 0 ? "" : count > 99 ? "99+" : String(count);
      app.dock?.setBadge(label);
    } else {
      app.setBadgeCount(count);
    }
  });
}
