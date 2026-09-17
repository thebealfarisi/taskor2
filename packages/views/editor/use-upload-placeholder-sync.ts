import { useEffect, type MutableRefObject, type RefObject } from "react";
import type { DraftUpload } from "@multica/core/drafts";
import type { ContentEditorRef } from "./content-editor";
import { DELIVER_MAX_TRIES, DELIVER_RETRY_MS } from "./upload-delivery";

export function useUploadPlaceholderSync(
  uploads: DraftUpload[],
  editorRef: RefObject<ContentEditorRef | null>,
  editorHoldsThisTarget: boolean,
  rebuiltUploadIdsRef: MutableRefObject<Set<string>>,
) {
  useEffect(() => {
    if (!editorHoldsThisTarget) return;
    const pending = uploads.filter(
      (u) =>
        u.status === "uploading" &&
        !rebuiltUploadIdsRef.current.has(u.clientUploadId),
    );
    if (pending.length === 0) return;
    let cancelled = false;
    let tries = 0;
    const attempt = () => {
      if (cancelled) return;
      const missing = pending.filter((u) => {
        const landed = editorRef.current?.insertUploadPlaceholder({
          uploadId: u.clientUploadId,
          filename: u.filename,
          size: u.size,
        });
        if (landed === true) rebuiltUploadIdsRef.current.add(u.clientUploadId);
        return landed !== true;
      });
      if (missing.length === 0) return;
      if (++tries >= DELIVER_MAX_TRIES) return;
      setTimeout(attempt, DELIVER_RETRY_MS);
    };
    attempt();
    return () => {
      cancelled = true;
    };
  }, [uploads, editorRef, editorHoldsThisTarget, rebuiltUploadIdsRef]);
}
