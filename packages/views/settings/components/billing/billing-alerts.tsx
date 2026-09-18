"use client";

import { AlertCircle, CheckCircle2, Loader2, ShieldCheck } from "lucide-react";
import {
  Alert,
  AlertDescription,
  AlertTitle,
} from "@multica/ui/components/ui/alert";
import { useT } from "../../../i18n";
import type { WorkspaceBillingReturnResult } from "./billing-currency";
import type { WorkspaceSubscriptionEntitlements } from "@multica/core/types";

interface BillingAlertsProps {
  returnResult: WorkspaceBillingReturnResult | null;
  isCheckoutProConfirmed: boolean;
  isSyncingCheckout: boolean;
  syncTimedOut: boolean;
  cancelAtPeriodEnd?: boolean;
  summaryPeriodEnd: string | null;
  entitlements: WorkspaceSubscriptionEntitlements;
  hasActiveProGrace: boolean;
  graceUntil: string | null;
  canManage: boolean;
  currentMemberLoading: boolean;
  actionError: string | null;
}

export function BillingAlerts({
  returnResult,
  isCheckoutProConfirmed,
  isSyncingCheckout,
  syncTimedOut,
  cancelAtPeriodEnd,
  summaryPeriodEnd,
  entitlements,
  hasActiveProGrace,
  graceUntil,
  canManage,
  currentMemberLoading,
  actionError,
}: BillingAlertsProps) {
  const { t } = useT("billing");

  return (
    <>
      {returnResult === "cancel" ? (
        <Alert>
          <AlertCircle />
          <AlertTitle>{t(($) => $.workspace.return.cancel_title)}</AlertTitle>
          <AlertDescription>
            {t(($) => $.workspace.return.cancel_description)}
          </AlertDescription>
        </Alert>
      ) : null}

      {returnResult === "portal" ? (
        <Alert>
          <CheckCircle2 />
          <AlertTitle>{t(($) => $.workspace.return.portal_title)}</AlertTitle>
          <AlertDescription>
            {t(($) => $.workspace.return.portal_description)}
          </AlertDescription>
        </Alert>
      ) : null}

      {returnResult === "success" ? (
        <Alert>
          {isCheckoutProConfirmed ? (
            <CheckCircle2 />
          ) : (
            <Loader2
              className={
                isSyncingCheckout
                  ? "animate-spin motion-reduce:animate-none"
                  : undefined
              }
            />
          )}
          <AlertTitle>
            {isCheckoutProConfirmed
              ? t(($) => $.workspace.return.active_title)
              : t(($) => $.workspace.return.syncing_title)}
          </AlertTitle>
          <AlertDescription>
            {isCheckoutProConfirmed
              ? t(($) => $.workspace.return.active_description)
              : syncTimedOut
                ? t(($) => $.workspace.return.timeout_description)
                : t(($) => $.workspace.return.syncing_description)}
          </AlertDescription>
        </Alert>
      ) : null}

      {cancelAtPeriodEnd ? (
        <Alert>
          <AlertCircle />
          <AlertTitle>
            {t(($) => $.workspace.subscription_notice.canceling_title)}
          </AlertTitle>
          <AlertDescription>
            {summaryPeriodEnd
              ? t(
                  ($) =>
                    $.workspace.subscription_notice.canceling_description,
                  { date: summaryPeriodEnd },
                )
              : t(
                  ($) =>
                    $.workspace.subscription_notice
                      .canceling_description_without_date,
                )}
          </AlertDescription>
        </Alert>
      ) : null}

      {entitlements.status === "past_due" ? (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertTitle>{t(($) => $.workspace.past_due.title)}</AlertTitle>
          <AlertDescription>
            {hasActiveProGrace && graceUntil
              ? t(($) => $.workspace.past_due.grace_description, {
                  date: graceUntil,
                })
              : t(($) => $.workspace.past_due.description)}
          </AlertDescription>
        </Alert>
      ) : null}

      {entitlements.status === "incomplete" ? (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertTitle>
            {t(($) => $.workspace.subscription_notice.incomplete_title)}
          </AlertTitle>
          <AlertDescription>
            {t(($) => $.workspace.subscription_notice.incomplete_description)}
          </AlertDescription>
        </Alert>
      ) : null}

      {entitlements.status === "incomplete_expired" ? (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertTitle>
            {t(
              ($) => $.workspace.subscription_notice.incomplete_expired_title,
            )}
          </AlertTitle>
          <AlertDescription>
            {t(
              ($) =>
                $.workspace.subscription_notice
                  .incomplete_expired_description,
            )}
          </AlertDescription>
        </Alert>
      ) : null}

      {entitlements.status === "paused" ? (
        <Alert>
          <AlertCircle />
          <AlertTitle>
            {t(($) => $.workspace.subscription_notice.paused_title)}
          </AlertTitle>
          <AlertDescription>
            {t(($) => $.workspace.subscription_notice.paused_description)}
          </AlertDescription>
        </Alert>
      ) : null}

      {entitlements.status === "unpaid" ? (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertTitle>
            {t(($) => $.workspace.subscription_notice.unpaid_title)}
          </AlertTitle>
          <AlertDescription>
            {t(($) => $.workspace.subscription_notice.unpaid_description)}
          </AlertDescription>
        </Alert>
      ) : null}

      {entitlements.status === "canceled" ? (
        <Alert>
          <AlertCircle />
          <AlertTitle>
            {t(($) => $.workspace.subscription_notice.canceled_title)}
          </AlertTitle>
          <AlertDescription>
            {t(($) => $.workspace.subscription_notice.canceled_description)}
          </AlertDescription>
        </Alert>
      ) : null}

      {!canManage && !currentMemberLoading ? (
        <Alert>
          <ShieldCheck />
          <AlertTitle>{t(($) => $.workspace.read_only.title)}</AlertTitle>
          <AlertDescription>
            {t(($) => $.workspace.read_only.description)}
          </AlertDescription>
        </Alert>
      ) : null}

      {actionError ? (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertTitle>{t(($) => $.workspace.errors.action_title)}</AlertTitle>
          <AlertDescription>{actionError}</AlertDescription>
        </Alert>
      ) : null}
    </>
  );
}
