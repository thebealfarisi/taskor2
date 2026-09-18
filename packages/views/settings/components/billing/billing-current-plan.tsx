"use client";

import { ExternalLink, Loader2 } from "lucide-react";
import type {
  WorkspaceSubscriptionEntitlements,
  WorkspaceSubscriptionSummary,
} from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../../i18n";
import {
  SettingsCard,
  SettingsRow,
  SettingsSection,
} from "../settings-layout";
import { planBadgeVariant, statusBadgeVariant } from "./billing-currency";

interface BillingCurrentPlanProps {
  entitlements: WorkspaceSubscriptionEntitlements;
  summary: WorkspaceSubscriptionSummary | null | undefined;
  actualSeats: number;
  summaryPeriodEnd: string | null;
  hasManagedSubscription: boolean;
  canManage: boolean;
  portalUnavailable: boolean;
  portalPending: boolean;
  isMutating: boolean;
  onPortal: () => void;
}

export function BillingCurrentPlan({
  entitlements,
  summary,
  actualSeats,
  summaryPeriodEnd,
  hasManagedSubscription,
  canManage,
  portalUnavailable,
  portalPending,
  isMutating,
  onPortal,
}: BillingCurrentPlanProps) {
  const { t } = useT("billing");

  const planLabel = (plan: string) => {
    switch (plan) {
      case "free":
        return t(($) => $.workspace.plan.free);
      case "pro":
        return t(($) => $.workspace.plan.pro);
      default:
        return t(($) => $.workspace.plan.unknown);
    }
  };

  const statusLabel = (status: string) => {
    switch (status) {
      case "inactive":
        return t(($) => $.workspace.status.inactive);
      case "active":
        return t(($) => $.workspace.status.active);
      case "trialing":
        return t(($) => $.workspace.status.trialing);
      case "past_due":
        return t(($) => $.workspace.status.past_due);
      case "canceled":
        return t(($) => $.workspace.status.canceled);
      case "incomplete":
        return t(($) => $.workspace.status.incomplete);
      case "incomplete_expired":
        return t(($) => $.workspace.status.incomplete_expired);
      case "paused":
        return t(($) => $.workspace.status.paused);
      case "unpaid":
        return t(($) => $.workspace.status.unpaid);
      default:
        return t(($) => $.workspace.status.unknown);
    }
  };

  return (
    <>
      <SettingsSection title={t(($) => $.workspace.current.title)}>
        <SettingsCard>
          <SettingsRow
            label={t(($) => $.workspace.current.plan)}
            description={t(($) => $.workspace.current.plan_description)}
          >
            <div className="flex flex-wrap items-center gap-2 sm:justify-end">
              <Badge variant={planBadgeVariant(entitlements.plan)}>
                {planLabel(entitlements.plan)}
              </Badge>
              <Badge variant={statusBadgeVariant(entitlements.status)}>
                {statusLabel(entitlements.status)}
              </Badge>
            </div>
          </SettingsRow>
          <SettingsRow
            label={t(($) => $.workspace.current.members)}
            description={t(($) => $.workspace.current.members_description)}
          >
            <span className="tabular-nums">
              {t(($) => $.workspace.current.member_count, {
                count: actualSeats,
              })}
            </span>
          </SettingsRow>
          {summary?.billingInterval ? (
            <SettingsRow
              label={t(($) => $.workspace.current.billing_interval)}
              description={t(
                ($) => $.workspace.current.billing_interval_description,
              )}
            >
              <span>
                {summary.billingInterval === "month"
                  ? t(($) => $.workspace.upgrade.monthly)
                  : t(($) => $.workspace.upgrade.yearly)}
              </span>
            </SettingsRow>
          ) : null}
          {summaryPeriodEnd ? (
            <SettingsRow
              label={t(($) => $.workspace.current.period_end)}
              description={t(($) => $.workspace.current.period_end_description)}
            >
              <span className="tabular-nums">{summaryPeriodEnd}</span>
            </SettingsRow>
          ) : null}
        </SettingsCard>
      </SettingsSection>

      {hasManagedSubscription && canManage ? (
        <SettingsSection
          title={t(($) => $.workspace.management.title)}
          description={t(($) => $.workspace.management.description)}
        >
          <SettingsCard>
            <SettingsRow
              label={t(($) => $.workspace.management.portal)}
              description={
                portalUnavailable
                  ? t(($) => $.workspace.management.portal_unavailable)
                  : t(($) => $.workspace.management.portal_description)
              }
            >
              {!portalUnavailable ? (
                <Button
                  className="h-11 w-full sm:w-auto"
                  disabled={isMutating}
                  onClick={onPortal}
                >
                  {portalPending ? (
                    <Loader2 className="animate-spin motion-reduce:animate-none" />
                  ) : (
                    <ExternalLink />
                  )}
                  {t(($) => $.workspace.actions.manage)}
                </Button>
              ) : null}
            </SettingsRow>
          </SettingsCard>
        </SettingsSection>
      ) : null}
    </>
  );
}
