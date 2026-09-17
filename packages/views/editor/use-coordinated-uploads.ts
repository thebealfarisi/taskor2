"use client";

/**
 * The coordinated-upload engine shared by every composer surface (MUL-5181, L2).
 *
 * Ownership inversion: an upload is owned by the module-level upload
 * coordinator (`@multica/core/drafts`), not by the React component that
 * started it. On file pick the engine writes a persisted placeholder into the
 * surface's draft IMMEDIATELY (through the {@link UploadDraftBinding}), then
 * hands the file to the coordinator. Closing or scrolling the composer away no
 * longer aborts the upload; logout aborts every tracked request; a placeholder
 * still `uploading` at load time is DROPPED by the store — the bytes were never
 * persisted, so it can neither resume nor be retried.
 *
 * `onSettled` is generation-guarded: it re-reads the draft and only writes if
 * the placeholder is still tracked (the draft may have been submitted or
 * cleared while the request was in flight). The coordinator never calls
 * `onSettled` on abort, so logout — abort first, then clear drafts — cannot
 * resurrect a placeholder into a wiped draft.
 *
 * The SOURCE OF TRUTH for what a submit binds is the draft BODY
 * (reference-filtered): an upload that settles after its mount died gets its
 * markdown link delivered back into the body — into the reopened composer's
 * live editor when one exists (confirmed, with retry while the Tiptap instance
 * is still warming up), else appended to the persisted draft — so the file is
 * visible, deletable, and deleting it really unbinds it.
 *
 * A surface plugs in with a {@link UploadDraftBinding}: imperative, store-backed
 * accessors that must remain callable after the component unmounts. Bindings
 * MUST be referentially stable per target (memoize on the draft key).
 */

import {
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type RefObject,
} from "react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import {
  startUpload,
  abortUpload,
  hasUploadingDraft,
  attachmentToDraftUpload,
  type DraftUpload,
} from "@multica/core/drafts";
import { createSafeId } from "@multica/core/utils";
import type { Attachment } from "@multica/core/types";
import {
  toUploadResult,
  type UploadContext,
  type UploadResult,
} from "@multica/core/hooks/use-file-upload";
import { MAX_FILE_SIZE } from "@multica/core/constants/upload";
import { useT } from "../i18n";
import type { UploadGate } from "./use-upload-gate";
import type { ContentEditorRef } from "./content-editor";
import { pastedTextSource } from "./extensions/file-upload";
import {
  liveEditors,
  deliverFinishedUpload,
  deliverPastedTextBack,
  type UploadDraftBinding,
} from "./upload-delivery";
import { useUploadPlaceholderSync } from "./use-upload-placeholder-sync";

export {
  __liveEditorRegistryKeysForTest,
  attachmentMarkdown,
  type UploadDraftBinding,
} from "./upload-delivery";

const EMPTY_ATTACHMENTS: Attachment[] = [];

export interface CoordinatedUploads {
  /** Every upload for this composer, placeholders included. */
  uploads: DraftUpload[];
  /** Completed attachment rows — the editor preview set; submit binds the
   *  subset whose link the body still references. */
  attachments: Attachment[];
  /**
   * Wire to `<ContentEditor onUploadFile={...} />`.
   *
   * The editor mints `uploadId` when it draws the placeholder node and hands it
   * in here, so the document node and the draft record share ONE id — that is
   * what lets a settle reaching a mount which did not start the upload find the
   * node again. Keep this second parameter in the type: a mock or a hand-rolled
   * caller that drops it silently mints a second id and breaks that link.
   * Optional only for a caller with no editor placeholder to match.
   */
  handleUpload: (file: File, uploadId?: string) => Promise<UploadResult | null>;
  /** Drop a placeholder (dismiss a failure / interrupted). */
  removeUpload: (clientUploadId: string) => void;
  /**
   * The submit gate for this composer. Combines the editor gate (this mount's
   * in-document uploads) with the coordinator-owned placeholders in the draft:
   * a composer reopened while a previous mount's upload is still in flight has
   * a clean editor document, so the editor gate alone would let a send clear
   * the draft out from under the settling upload — silently dropping the file
   * whose "uploading" chip is on screen.
   */
  gate: UploadGate;
}

/**
 * @param binding       Store-backed accessors for the persisted target. When
 *                      absent (a composer with no persistence context) uploads
 *                      fall back to component-local state and die with the
 *                      mount, matching pre-L2 behavior.
 * @param boundUploads  The binding's uploads as a REACTIVE value (the caller's
 *                      store subscription). Ignored when `binding` is absent.
 */
export function useCoordinatedUploads(
  binding: UploadDraftBinding | undefined,
  boundUploads: DraftUpload[],
  ctx: UploadContext,
  editorGate: UploadGate,
  editorRef: RefObject<ContentEditorRef | null>,
  opts?: {
    /**
     * Resolve the binding a NEW upload should target, snapshotted at pick
     * time. For composers whose single editor instance can hold a DIFFERENT
     * draft than the selected one (chat pins the document while an upload is
     * in flight): the file lands in the document the editor is HOLDING, so
     * its placeholder/settle/write-back must follow that draft, not whatever
     * is selected by the time the request finishes. Defaults to `binding`.
     */
    resolveUploadTarget?: () => UploadDraftBinding;
    /**
     * Registry key to register this mount's editor under, when it differs
     * from `binding.registryKey`. Same divergence as above: a write-back must
     * insert into the editor only if its DOCUMENT belongs to the settling
     * draft, so composers with a pinnable editor register the LOADED key.
     */
    liveRegistryKey?: string;
  },
): CoordinatedUploads {
  const { t } = useT("editor");
  const [localUploads, setLocalUploads] = useState<DraftUpload[]>([]);
  // Latest-value ref: handleUpload snapshots the target at invocation time.
  const resolveUploadTargetRef = useRef(opts?.resolveUploadTarget);
  resolveUploadTargetRef.current = opts?.resolveUploadTarget;

  // Liveness of THIS mount, read by settle closures it created: while true,
  // the editor that started the upload will do the inline swap itself; once
  // false, the write-back is the only path that lands the link. Layout effect
  // on purpose: React nulls the child editor's ref during the unmount commit,
  // but a passive cleanup flips this a task later — a settle landing in that
  // gap would see "mounted" with no editor left to swap.
  const mountedRef = useRef(true);
  useLayoutEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Registration only exists for a persisted target; a liveRegistryKey with
  // no binding would register an editor nothing can ever look up.
  const registryKey = binding ? (opts?.liveRegistryKey ?? binding.registryKey) : undefined;
  // Layout effect for the same reason as mountedRef: chat's adopt swaps the
  // editor's document (and its loaded key) synchronously during commit, and a
  // passive re-registration one task later leaves a settle window where the
  // old key still maps to an editor now holding another draft's document.
  useLayoutEffect(() => {
    if (!registryKey) return;
    liveEditors.set(registryKey, editorRef);
    return () => {
      if (liveEditors.get(registryKey) === editorRef) liveEditors.delete(registryKey);
    };
  }, [registryKey, editorRef]);

  const uploads = binding ? boundUploads : localUploads;
  const attachments = useMemo(() => {
    const done: Attachment[] = [];
    for (const u of uploads) {
      if (u.status === "uploaded") done.push(u.attachment);
    }
    return done.length === 0 ? EMPTY_ATTACHMENTS : done;
  }, [uploads]);

  const rebuiltUploadIdsRef = useRef<Set<string>>(new Set());
  const editorHoldsThisTarget = !binding || registryKey === binding.registryKey;

  useUploadPlaceholderSync(
    uploads,
    editorRef,
    editorHoldsThisTarget,
    rebuiltUploadIdsRef,
  );

  const issueId = ctx.issueId;
  const commentId = ctx.commentId;
  const chatSessionId = ctx.chatSessionId;

  const handleUpload = useCallback(
    (file: File, uploadId?: string): Promise<UploadResult | null> => {
      // Adopt the editor's id rather than minting a second one: the document
      // node and this draft record are the same upload, and a settle that
      // reaches a mount which did not start it can only find the node by id.
      // The fallback covers a caller with no editor placeholder to match.
      const clientUploadId = uploadId ?? createSafeId();
      if (uploadId) rebuiltUploadIdsRef.current.add(uploadId);
      // Snapshot the target NOW: settle handlers must keep addressing the
      // draft the file landed in, no matter what is selected when they fire.
      const target = binding ? (resolveUploadTargetRef.current?.() ?? binding) : undefined;
      const placeholder: DraftUpload = {
        clientUploadId,
        status: "uploading",
        filename: file.name,
        size: file.size,
        contentType: file.type || undefined,
      };

      const pastedText = pastedTextSource(file);

      if (file.size > MAX_FILE_SIZE) {
        // Never enters the coordinator, and never enters the draft either —
        // see the settle handler below for why a failure leaves no placeholder.
        const reason = "File exceeds 100 MB limit";
        if (pastedText !== undefined) {
          // A paste has no on-disk copy to re-attach from, so the text goes
          // back into the composer instead of being lost with the upload.
          deliverPastedTextBack(target, editorRef, pastedText);
        }
        toast.error(t(($) => $.upload.failed, { filename: file.name, reason }));
        return Promise.resolve(null);
      }

      if (target) {
        target.addUpload(placeholder);
      } else {
        setLocalUploads((prev) => [...prev, placeholder]);
      }

      return new Promise<UploadResult | null>((resolve) => {
        startUpload({
          clientUploadId,
          file,
          api,
          ctx: { issueId, commentId, chatSessionId },
          onSettled: (outcome) => {
            if (outcome.status === "uploaded") {
              if (target) {
                // Generation guard: only write if the draft still tracks it.
                if (target.getUploads().some((u) => u.clientUploadId === clientUploadId)) {
                  target.settleUpload(clientUploadId, outcome.attachment);
                  // Write-back (MUL-5181): the mount that started this upload
                  // is gone, so no editor swap will put the finished link into
                  // the document — deliver it into the BODY instead (that is
                  // what submit binds, reference-filtered). Skipped while this
                  // mount is alive: resolving the promise below drives the
                  // normal inline blob→URL swap, and a placeholder the user
                  // deleted mid-upload must stay deleted.
                  if (!mountedRef.current) {
                    deliverFinishedUpload(target, clientUploadId, outcome.attachment);
                  }
                }
              } else {
                setLocalUploads((prev) =>
                  prev.map((u) =>
                    u.clientUploadId === clientUploadId
                      ? { ...attachmentToDraftUpload(outcome.attachment), clientUploadId }
                      : u,
                  ),
                );
              }
              resolve(toUploadResult(outcome.attachment));
            } else {
              const reason = outcome.error.message;
              if (target) {
                if (target.getUploads().some((u) => u.clientUploadId === clientUploadId)) {
                  target.removeUpload(clientUploadId);
                  // Paste-as-file has no on-disk copy to re-attach from, so
                  // its text goes back into the composer.
                  if (pastedText !== undefined) {
                    deliverPastedTextBack(target, editorRef, pastedText);
                  }
                }
              } else {
                setLocalUploads((prev) =>
                  prev.filter((u) => u.clientUploadId !== clientUploadId),
                );
                if (pastedText !== undefined) {
                  deliverPastedTextBack(undefined, editorRef, pastedText);
                }
              }
              toast.error(t(($) => $.upload.failed, { filename: file.name, reason }));
              resolve(null);
            }
          },
        });
      });
    },
    [binding, editorRef, issueId, commentId, chatSessionId, t],
  );

  const removeUpload = useCallback(
    (clientUploadId: string) => {
      // Defensive cancel for a placeholder removed while still in flight: its
      // request has no destination left, so don't let it run to completion.
      const tracked = binding ? binding.getUploads() : localUploadsRef.current;
      if (tracked.some((u) => u.clientUploadId === clientUploadId && u.status === "uploading")) {
        abortUpload(clientUploadId);
      }
      if (binding) binding.removeUpload(clientUploadId);
      else setLocalUploads((prev) => prev.filter((u) => u.clientUploadId !== clientUploadId));
    },
    [binding],
  );

  // Submit-time truth for the local (non-persisted) path: a deliberate
  // latest-value ref written during render, because `isBlocked` must read the
  // value at invocation time, not at the render the callback captured. The
  // persisted path reads the store through the binding.
  const localUploadsRef = useRef(localUploads);
  localUploadsRef.current = localUploads;

  // Rebuilt every render on purpose (editorGate itself is a fresh object per
  // render): consumers read it through refs/props, never by identity.
  const gate: UploadGate = {
    uploading: editorGate.uploading || hasUploadingDraft(uploads),
    onUploadingChange: editorGate.onUploadingChange,
    isBlocked: () =>
      editorGate.isBlocked() ||
      hasUploadingDraft(binding ? binding.getUploads() : localUploadsRef.current),
  };

  return { uploads, attachments, handleUpload, removeUpload, gate };
}
