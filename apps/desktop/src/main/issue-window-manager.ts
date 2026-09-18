import { BrowserWindow, dialog } from "electron";
import { join } from "path";
import { is } from "@electron-toolkit/utils";
import { openExternalSafely } from "./external-url";
import { installContextMenu } from "./context-menu";
import { createRendererWebPreferences } from "./renderer-web-preferences";
import { getAppVersion } from "./app-version";
import {
  writeFreezeBreadcrumb,
  clearFreezeBreadcrumb,
} from "./freeze-breadcrumb";
import {
  createElectronReloadPrompt,
  installRendererRecoveryHandlers,
  type RendererRecoveryWindow,
} from "./renderer-recovery";
import {
  sanitizeRendererRouteContext,
  type RendererRouteContext,
} from "../shared/renderer-route-context";
import {
  encodeIssueWindowArgument,
  parseIssueWindowRequest,
} from "../shared/issue-window";
import type { AuthSessionCoordinator } from "./auth-session-coordinator";
import type { DevLog } from "./dev-log";

// ---------------------------------------------------------------------------
// Helpers shared with the main window (imported from index.ts via callbacks)
// ---------------------------------------------------------------------------

export interface IssueWindowManagerDeps {
  BUNDLED_ICON_PATH: string;
  devLog: DevLog | undefined;
  freezeBreadcrumbPath: () => string;
  /** Shared renderer-route-context WeakMap — also used by the main window. */
  rendererRouteContexts: WeakMap<Electron.WebContents, RendererRouteContext>;
  authSessionCoordinator: AuthSessionCoordinator<BrowserWindow>;
  /** Installs locale-refresh listener on a window. */
  installLocaleRefresh: (window: BrowserWindow) => void;
  /** Installs the download save-dialog handler on a window session. */
  installDownloadSaveDialogHandler: (window: BrowserWindow) => void;
  /** Installs the shortcut handler on a window. */
  installWindowShortcutHandler: (window: BrowserWindow) => void;
  /** Loads the renderer into a window. */
  loadRenderer: (window: BrowserWindow) => void;
  /** Returns the current system locale string, e.g. "en-US". */
  getSystemLocale: () => string;
}

// ---------------------------------------------------------------------------
// IssueWindowManager
// ---------------------------------------------------------------------------

/**
 * Manages the lifecycle of dedicated issue pop-out windows.
 *
 * Responsibilities:
 * - Create issue windows on request from the main renderer.
 * - Register/unregister them with `AuthSessionCoordinator` so cross-window
 *   auth changes close stale windows.
 * - Expose `hasIssueWindow` so IPC handlers can validate the sender without
 *   importing the raw `Set` from `index.ts`.
 */
export class IssueWindowManager {
  private readonly issueWindows = new Set<BrowserWindow>();
  private readonly deps: IssueWindowManagerDeps;

  constructor(deps: IssueWindowManagerDeps) {
    this.deps = deps;
  }

  hasIssueWindow(window: BrowserWindow): boolean {
    return this.issueWindows.has(window);
  }

  /**
   * Parse `request` and open a new issue window.
   * Returns `true` on success, `false` if the request payload is invalid.
   */
  openIssueWindow(request: unknown): boolean {
    const context = parseIssueWindowRequest(request);
    if (!context) return false;
    this.createIssueWindow(context);
    return true;
  }

  private createIssueWindow(
    context: ReturnType<typeof parseIssueWindowRequest> & object,
  ): void {
    const {
      BUNDLED_ICON_PATH,
      devLog,
      freezeBreadcrumbPath,
      rendererRouteContexts,
      authSessionCoordinator,
      installLocaleRefresh,
      installDownloadSaveDialogHandler,
      installWindowShortcutHandler,
      loadRenderer,
      getSystemLocale,
    } = this.deps;

    const systemLocale = getSystemLocale();

    const window = new BrowserWindow({
      width: 960,
      height: 760,
      minWidth: 720,
      minHeight: 520,
      title: context.title,
      titleBarStyle: "hiddenInset",
      trafficLightPosition: { x: 16, y: 17 },
      show: false,
      autoHideMenuBar: true,
      ...(is.dev || process.platform === "linux"
        ? { icon: BUNDLED_ICON_PATH }
        : {}),
      webPreferences: createRendererWebPreferences(
        join(__dirname, "../preload/index.js"),
        systemLocale,
        [encodeIssueWindowArgument(context)],
      ),
    });

    this.issueWindows.add(window);
    authSessionCoordinator.registerIssueWindow(window);

    window.on("closed", () => {
      this.issueWindows.delete(window);
      authSessionCoordinator.unregisterIssueWindow(window);
    });

    window.on("ready-to-show", () => window.show());
    installLocaleRefresh(window);
    installDownloadSaveDialogHandler(window);

    window.webContents.setWindowOpenHandler((details) => {
      openExternalSafely(details.url);
      return { action: "deny" };
    });
    installWindowShortcutHandler(window);

    const initialRouteContext = sanitizeRendererRouteContext({
      surface: "tab",
      path: context.path,
      workspaceSlug: context.workspaceSlug,
    });
    if (initialRouteContext) {
      rendererRouteContexts.set(window.webContents, initialRouteContext);
    }

    installRendererRecoveryHandlers(
      window as unknown as RendererRecoveryWindow,
      {
        isDev: is.dev,
        showReloadPrompt: createElectronReloadPrompt((options) =>
          dialog.showMessageBox(window, options),
        ),
        getDiagnosticContext: () => {
          // No `windowUrl`: it is an absolute install path and the bucketed
          // route below already says which page the window was on.
          const routeContext = rendererRouteContexts.get(window.webContents);
          return routeContext ? { desktopRoute: routeContext } : {};
        },
        persistBreadcrumb: is.dev
          ? undefined
          : (payload) =>
              writeFreezeBreadcrumb(freezeBreadcrumbPath(), {
                ownerId: `issue:${window.id}`,
                kind: payload.kind,
                context: payload.context,
                ts: Date.now(),
                version: getAppVersion(),
              }),
        clearBreadcrumb: is.dev
          ? undefined
          : () =>
              clearFreezeBreadcrumb(freezeBreadcrumbPath(), `issue:${window.id}`),
        log: devLog,
      },
    );

    installContextMenu(window.webContents);
    loadRenderer(window);
  }
}
