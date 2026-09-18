"use client";

import { AlertCircle, CheckCircle2, Loader2, RefreshCw } from "lucide-react";
import {
  Alert,
  AlertDescription,
  AlertTitle,
} from "@multica/ui/components/ui/alert";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../../i18n";
import {
  SettingsCard,
  SettingsRow,
  SettingsSection,
} from "../settings-layout";

interface BillingSeatsSectionProps {
  summaryUnavailable: boolean;
  summaryLoading: boolean;
  summaryFetching: boolean;
  onRetrySummary: () => void;
  reconcileMessage: string | null;
  actualSeats: number;
  billedSeats: number | null | undefined;
  pendingSeatQuantity: number | null | undefined;
  summaryPeriodEnd: string | null;
  canManage: boolean;
  hasManagedSubscription: boolean;
  isMutating: boolean;
  reconcilePending: boolean;
  onReconcile: () => void;
}

export function BillingSeatsSection({
  summaryUnavailable,
  summaryLoading,
  summaryFetching,
  onRetrySummary,
  reconcileMessage,
  actualSeats,
  billedSeats,
  pendingSeatQuantity,
  summaryPeriodEnd,
  canManage,
  hasManagedSubscription,
  isMutating,
  reconcilePending,
  onReconcile,
}: BillingSeatsSectionProps) {
  const { t } = useT("billing");

  return (
    <SettingsSection
      title={t(($) => $.workspace.seats.title)}
      description={t(($) => $.workspace.seats.description)}
    >
      {summaryUnavailable ? (
        <Alert className="mb-3">
          <AlertCircle />
          <AlertTitle>
            {t(($) => $.workspace.seats.summary_unavailable_title)}
          </AlertTitle>
          <AlertDescription>
            <p>
              {t(($) => $.workspace.seats.summary_unavailable_description)}
            </p>
            <Button
              className="mt-3"
              type="button"
              variant="outline"
              size="sm"
              aria-busy={summaryFetching}
              disabled={summaryFetching}
              onClick={onRetrySummary}
            >
              {summaryFetching ? (
                <Loader2 className="animate-spin motion-reduce:animate-none" />
              ) : (
                <RefreshCw />
              )}
              {t(($) => $.workspace.actions.retry)}
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}
      {reconcileMessage ? (
        <Alert className="mb-3">
          <CheckCircle2 />
          <AlertTitle>{t(($) => $.workspace.seats.updated)}</AlertTitle>
          <AlertDescription>{reconcileMessage}</AlertDescription>
        </Alert>
      ) : null}
      <SettingsCard>
        <SettingsRow
          label={t(($) => $.workspace.seats.human_members)}
          description={t(($) => $.workspace.seats.human_members_description)}
        >
          <div className="flex flex-col gap-2 sm:items-end">
            <span className="tabular-nums">
              {t(($) => $.workspace.current.member_count, {
                count: actualSeats,
              })}
            </span>
            {canManage && hasManagedSubscription ? (
              <Button
                className="h-11 w-full sm:w-auto"
                variant="outline"
                disabled={isMutating}
                onClick={onReconcile}
              >
                {reconcilePending ? (
                  <Loader2 className="animate-spin motion-reduce:animate-none" />
                ) : (
                  <RefreshCw />
                )}
                {t(($) => $.workspace.actions.refresh_seats)}
              </Button>
            ) : null}
          </div>
        </SettingsRow>
        <SettingsRow
          label={t(($) => $.workspace.seats.billed)}
          description={t(($) => $.workspace.seats.billed_description)}
        >
          {summaryLoading ? (
            <Skeleton
              className="h-5 w-20 motion-reduce:animate-none"
              aria-label={t(($) => $.workspace.seats.summary_loading)}
            />
          ) : summaryUnavailable ? (
            <span className="text-muted-foreground">
              {t(($) => $.workspace.seats.unavailable)}
            </span>
          ) : billedSeats === null || billedSeats === undefined ? (
            <span className="text-muted-foreground">
              {t(($) => $.workspace.seats.not_subscribed)}
            </span>
          ) : (
            <span className="tabular-nums">
              {t(($) => $.workspace.seats.seat_count, {
                count: billedSeats,
              })}
            </span>
          )}
        </SettingsRow>
        <SettingsRow
          label={t(($) => $.workspace.seats.pending)}
          description={t(($) => $.workspace.seats.pending_description)}
        >
          {summaryLoading ? (
            <Skeleton
              className="h-5 w-28 motion-reduce:animate-none"
              aria-label={t(($) => $.workspace.seats.summary_loading)}
            />
          ) : summaryUnavailable ? (
            <span className="text-muted-foreground">
              {t(($) => $.workspace.seats.unavailable)}
            </span>
          ) : pendingSeatQuantity === null ||
            pendingSeatQuantity === undefined ? (
            <span className="text-muted-foreground">
              {t(($) => $.workspace.seats.none_pending)}
            </span>
          ) : summaryPeriodEnd ? (
            <span className="tabular-nums">
              {t(($) => $.workspace.seats.pending_with_date, {
                count: pendingSeatQuantity,
                date: summaryPeriodEnd,
              })}
            </span>
          ) : (
            <span className="tabular-nums">
              {t(($) => $.workspace.seats.seat_count, {
                count: pendingSeatQuantity,
              })}
            </span>
          )}
        </SettingsRow>
      </SettingsCard>
    </SettingsSection>
  );
}
