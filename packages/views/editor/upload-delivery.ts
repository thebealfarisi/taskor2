import type { RefObject } from "react";
import { contentReferencesAttachment, type Attachment } from "@multica/core/types";
import { toUploadResult } from "@multica/core/hooks/use-file-upload";
import type { DraftUpload } from "@multica/core/drafts";
import type { ContentEditorRef } from "./content-editor";

/**
 * Store-backed accessors for one composer target's uploads and body. Every
 * method must go through the store's `getState()` (never captured React
 * state): settle handlers call them after the owning component is gone.
 */
export interface UploadDraftBinding {
  /**
   * Identity for the live-editor registry — unique per composer target
   * (e.g. `comment:new:{issueId}`, `issue-create:manual`). A reopened
   * composer for the same target registers its editor under the same key,
   * which is how a settle handler finds it.
   */
  registryKey: string;
  getUploads: () => DraftUpload[];
  addUpload: (upload: DraftUpload) => void;
  settleUpload: (clientUploadId: string, attachment: Attachment) => void;
  failUpload: (clientUploadId: string, error?: string) => void;
  removeUpload: (clientUploadId: string) => void;
  /** The draft body reference-filtering binds against at submit time. */
  getBody: () => string;
  /** Append a markdown fragment to the persisted body. */
  appendToBody: (markdown: string) => void;
}

// The editor currently showing each registry key. Lets a settle handler whose
// own mount is gone (the upload outlived the composer) hand the finished link
// to the editor a REOPENED composer mounted for the same target.
export const liveEditors = new Map<string, RefObject<ContentEditorRef | null>>();

/** Test-only: registry keys currently registered. Lets timing tests assert
 *  registration is part of the COMMIT (layout), not a passive task later. */
export function __liveEditorRegistryKeysForTest(): string[] {
  return [...liveEditors.keys()];
}

/** Markdown for a finished upload. Mirrors the shape the in-editor swap
 *  produces (`extensions/file-upload.ts`: image node for images, fileCard link
 *  for everything else) — keep the two in sync. */
export function attachmentMarkdown(att: Attachment): string {
  const link = toUploadResult(att).markdownLink;
  return (att.content_type ?? "").startsWith("image/")
    ? `![${att.filename}](${link})`
    : `[${att.filename}](${link})`;
}

export const DELIVER_RETRY_MS = 50;
export const DELIVER_MAX_TRIES = 100; // ~5s — editor init is a passive effect away

/**
 * Land a finished upload's markdown link in the draft BODY after the mount
 * that owned the upload died. Delivery must be CONFIRMED, not assumed:
 *
 *  - live editor for the key, insert landed → also persist the same body via
 *    `appendToBody` as insurance — the editor's debounced emit is dropped on a
 *    quick unmount, and it converges to identical content anyway.
 *  - no composer mounted for the key → append to the persisted draft; the next
 *    mount reads it as `defaultValue`.
 *  - composer mounted but its Tiptap instance not created yet (the handle
 *    exists from first commit; the instance arrives in a passive effect) →
 *    RETRY. Appending to the store here would be erased by the mounted
 *    editor's first emit, which snapshots a body without the link.
 *
 * Every attempt re-checks the generation guard (draft may be cleared or
 * submitted while waiting) and the body (the link may have landed some other
 * way) before writing.
 */
export function deliverFinishedUpload(
  binding: UploadDraftBinding,
  clientUploadId: string,
  attachment: Attachment,
  tries = 0,
): void {
  if (!binding.getUploads().some((u) => u.clientUploadId === clientUploadId)) return;
  if (contentReferencesAttachment(binding.getBody(), attachment)) return;

  const md = attachmentMarkdown(attachment);
  const live = liveEditors.get(binding.registryKey);
  // A composer showing this target rebuilt the placeholder on mount, so the
  // finished attachment REPLACES it where the user last saw it instead of
  // being appended a second time at the end.
  if (live?.current?.settleUploadPlaceholder(clientUploadId, toUploadResult(attachment)) === true) {
    binding.appendToBody(md);
    return;
  }
  if (live?.current?.insertMarkdownAtEnd(md) === true) {
    binding.appendToBody(md);
    return;
  }
  if (!live) {
    binding.appendToBody(md);
    return;
  }
  if (tries >= DELIVER_MAX_TRIES) {
    // Editor never initialized — persist to the store as the least-bad option.
    binding.appendToBody(md);
    return;
  }
  setTimeout(
    () => deliverFinishedUpload(binding, clientUploadId, attachment, tries + 1),
    DELIVER_RETRY_MS,
  );
}

/**
 * Put a failed paste-as-file's source text back where the user can see it.
 *
 * The mirror image of {@link deliverFinishedUpload}, and it exists for the
 * same reason: the upload outlives the mount, so the composer that swallowed
 * the paste may be gone by the time the failure lands. Unlike a dropped file,
 * this content has no other copy — it was never written into the document and
 * the tab it came from may be closed — so "the editor is gone, drop it" would
 * be silent data loss.
 *
 * Restored as markdown, not literal text: had the paste never been converted,
 * `markdown-paste` is exactly what would have handled it, so this reproduces
 * what the user would have gotten. Live editor first (it lands at the end of
 * the document, never mid-sentence at a caret the user has since moved), the
 * persisted body otherwise.
 */
export function deliverPastedTextBack(
  binding: UploadDraftBinding | undefined,
  editorRef: RefObject<ContentEditorRef | null>,
  text: string,
): void {
  if (!binding) {
    // No persistence context (a reply composer opened without a draft key):
    // the live editor is the only place left to put it.
    editorRef.current?.insertMarkdownAtEnd(text);
    return;
  }
  // Same insurance as deliverFinishedUpload: a landed editor insert can still
  // lose its debounced emit to a quick unmount, and both writes converge on
  // identical content.
  liveEditors.get(binding.registryKey)?.current?.insertMarkdownAtEnd(text);
  binding.appendToBody(text);
}
