"use client";

import { Loader2, RefreshCw } from "lucide-react";
import type { WorkspaceSubscriptionEntitlements } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Progress,
  ProgressLabel,
  ProgressValue,
} from "@multica/ui/components/ui/progress";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../../i18n";
import {
  SettingsCard,
  SettingsRow,
  SettingsSection,
} from "../settings-layout";
import type { AutopilotUsageView } from "../billing-state";

interface BillingUsageMetersProps {
  entitlements: WorkspaceSubscriptionEntitlements;
  canUseEntitlementUnlimited: boolean;
  quotaUsage: AutopilotUsageView;
  quotaUsageLoading: boolean;
  quotaUsageFetching: boolean;
  onRetryQuotaUsage: () => void;
  quotaResetAt: string | null;
  locale: string;
}

export function BillingUsageMeters({
  entitlements,
  canUseEntitlementUnlimited,
  quotaUsage,
  quotaUsageLoading,
  quotaUsageFetching,
  onRetryQuotaUsage,
  quotaResetAt,
  locale,
}: BillingUsageMetersProps) {
  const { t } = useT("billing");
  const numberFormatter = new Intl.NumberFormat(locale);

  return (
    <SettingsSection
      title={t(($) => $.workspace.limits.title)}
      description={t(($) => $.workspace.limits.description)}
    >
      <SettingsCard>
        <SettingsRow
          label={t(($) => $.workspace.limits.issues)}
          description={t(($) => $.workspace.limits.issues_description)}
        >
          <span className="tabular-nums">
            {entitlements.issueWindow === null
              ? canUseEntitlementUnlimited
                ? t(($) => $.workspace.limits.unlimited)
                : t(($) => $.workspace.limits.unavailable)
              : numberFormatter.format(entitlements.issueWindow)}
          </span>
        </SettingsRow>
        <SettingsRow
          label={t(($) => $.workspace.limits.autopilots)}
          description={t(($) => $.workspace.limits.autopilots_description)}
        >
          {quotaUsage.kind === "unlimited" ? (
            <span className="tabular-nums">
              {t(($) => $.workspace.limits.unlimited)}
            </span>
          ) : quotaUsageLoading ? (
            <div
              className="w-full max-w-72 space-y-2 motion-reduce:[&_[data-slot=skeleton]]:animate-none"
              role="status"
              aria-label={t(($) => $.workspace.limits.usage_loading)}
            >
              <Skeleton className="h-5 w-full" />
              <Skeleton className="h-4 w-2/3" />
            </div>
          ) : quotaUsage.kind === "metered" ? (
            <div className="w-full max-w-72 space-y-2">
              <Progress
                value={quotaUsage.progress}
                aria-label={t(($) => $.workspace.limits.usage_label)}
              >
                <ProgressLabel>
                  {quotaUsage.reached
                    ? t(($) => $.workspace.limits.reached)
                    : t(($) => $.workspace.limits.current_usage)}
                </ProgressLabel>
                <ProgressValue>
                  {() =>
                    t(($) => $.workspace.limits.usage_total, {
                      total: numberFormatter.format(quotaUsage.total),
                      limit: numberFormatter.format(quotaUsage.limit),
                    })
                  }
                </ProgressValue>
              </Progress>
              <p className="text-caption text-muted-foreground tabular-nums">
                {t(($) => $.workspace.limits.usage_breakdown, {
                  used: numberFormatter.format(quotaUsage.used),
                  reserved: numberFormatter.format(quotaUsage.reserved),
                })}
              </p>
              {quotaResetAt ? (
                <p className="text-caption text-muted-foreground tabular-nums">
                  {t(($) => $.workspace.limits.resets_at, {
                    date: quotaResetAt,
                  })}
                </p>
              ) : null}
            </div>
          ) : (
            <div className="flex flex-col gap-2 sm:items-end">
              {entitlements.autopilotRuns !== null ? (
                <span className="tabular-nums">
                  {t(($) => $.workspace.limits.per_month, {
                    count: entitlements.autopilotRuns,
                  })}
                </span>
              ) : null}
              <span className="text-caption text-muted-foreground">
                {t(($) => $.workspace.limits.usage_unavailable)}
              </span>
              <Button
                type="button"
                variant="outline"
                size="sm"
                aria-label={t(($) => $.workspace.actions.retry_autopilots)}
                aria-busy={quotaUsageFetching}
                disabled={quotaUsageFetching}
                onClick={onRetryQuotaUsage}
              >
                {quotaUsageFetching ? (
                  <Loader2 className="animate-spin motion-reduce:animate-none" />
                ) : (
                  <RefreshCw />
                )}
                {t(($) => $.workspace.actions.retry)}
              </Button>
            </div>
          )}
        </SettingsRow>
      </SettingsCard>
    </SettingsSection>
  );
}
