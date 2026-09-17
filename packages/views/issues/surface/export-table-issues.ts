import { api } from "@multica/core/api";
import type { Issue, IssueTableQuerySpec } from "@multica/core/types";
import { IssueTableExportIntegrityError } from "../components/table-view-model";

export async function exportTableIssues(
  tableQuerySpec: IssueTableQuerySpec,
): Promise<Issue[]> {
  const issues: Issue[] = [];
  const seenIssueIds = new Set<string>();
  const seenCursors = new Set<string>();
  let fingerprint: string | null = null;
  let expectedTotal: number | null = null;
  let cursor: string | null = null;
  do {
    if (cursor !== null) {
      if (seenCursors.has(cursor)) throw new IssueTableExportIntegrityError();
      seenCursors.add(cursor);
    }
    const page = await api.listIssueTableRows({
      query: tableQuerySpec,
      group: { kind: "none" },
      group_key: null,
      hierarchy: { enabled: false },
      parent_id: null,
      page: { limit: 100, cursor },
    });
    // parseWithFallback deliberately protects interactive views from schema
    // drift with an empty response. Export must fail closed instead: an empty
    // fingerprint is the fallback sentinel and must never create a truncated
    // CSV that looks successful.
    if (!page.query_fingerprint) throw new IssueTableExportIntegrityError();
    fingerprint ??= page.query_fingerprint;
    if (cursor === null) expectedTotal = page.total;
    if (
      page.query_fingerprint !== fingerprint ||
      page.group_key !== null ||
      page.parent_id !== null
    ) {
      throw new IssueTableExportIntegrityError();
    }
    for (const row of page.rows) {
      if (seenIssueIds.has(row.issue.id)) {
        throw new IssueTableExportIntegrityError();
      }
      seenIssueIds.add(row.issue.id);
      issues.push(row.issue);
    }
    cursor = page.next_cursor;
  } while (cursor);
  if (issues.length !== (expectedTotal ?? 0)) {
    throw new IssueTableExportIntegrityError();
  }
  return issues;
}
