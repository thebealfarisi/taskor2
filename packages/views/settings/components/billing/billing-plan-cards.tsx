"use client";

import { CreditCard, Loader2, RefreshCw } from "lucide-react";
import type {
  WorkspaceSubscriptionInterval,
  WorkspaceSubscriptionPrices,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../../i18n";
import {
  SettingsCard,
  SettingsSection,
} from "../settings-layout";
import { formatStripeMinorAmount } from "./billing-currency";

interface BillingPlanCardsProps {
  interval: WorkspaceSubscriptionInterval;
  onIntervalChange: (interval: WorkspaceSubscriptionInterval) => void;
  actualSeats: number;
  prices: WorkspaceSubscriptionPrices | undefined;
  pricesLoading: boolean;
  pricesFetching: boolean;
  onRetryPrices: () => void;
  canManage: boolean;
  isMutating: boolean;
  locale: string;
  onOpenCheckoutConfirm: () => void;
}

export function BillingPlanCards({
  interval,
  onIntervalChange,
  actualSeats,
  prices,
  pricesLoading,
  pricesFetching,
  onRetryPrices,
  canManage,
  isMutating,
  locale,
  onOpenCheckoutConfirm,
}: BillingPlanCardsProps) {
  const { t } = useT("billing");

  const selectedPrice = prices?.[interval] ?? null;
  const formattedUnitPrice = selectedPrice
    ? formatStripeMinorAmount(
        selectedPrice.unitAmount,
        selectedPrice.currency,
        locale,
      )
    : null;
  const formattedEstimatedTotal =
    selectedPrice && actualSeats > 0
      ? formatStripeMinorAmount(
          selectedPrice.unitAmount * actualSeats,
          selectedPrice.currency,
          locale,
        )
      : null;
  const hasDisplayableUnitPrice =
    selectedPrice?.intervalCount === 1 && formattedUnitPrice !== null;
  const hasDisplayableEstimatedTotal =
    hasDisplayableUnitPrice && formattedEstimatedTotal !== null;
  const canRetryPrice = !pricesLoading && selectedPrice === null;

  return (
    <SettingsSection
      title={t(($) => $.workspace.upgrade.title)}
      description={t(($) => $.workspace.upgrade.description)}
    >
      <SettingsCard>
        <div className="space-y-5 p-4 sm:p-5">
          <div
            className="inline-flex w-full rounded-lg border border-surface-border p-1 sm:w-auto"
            role="group"
            aria-label={t(($) => $.workspace.upgrade.interval_label)}
          >
            {(["month", "year"] as const).map((value) => (
              <button
                key={value}
                type="button"
                aria-pressed={interval === value}
                className="min-h-11 flex-1 rounded-md px-4 text-body font-medium text-muted-foreground transition-[color,background-color,box-shadow] hover:text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring aria-pressed:bg-surface-selected aria-pressed:text-surface-selected-foreground aria-pressed:shadow-sm sm:min-w-32"
                onClick={() => onIntervalChange(value)}
              >
                {value === "month"
                  ? t(($) => $.workspace.upgrade.monthly)
                  : t(($) => $.workspace.upgrade.yearly)}
              </button>
            ))}
          </div>
          <div className="space-y-2">
            <p className="text-body font-medium">
              {t(($) => $.workspace.upgrade.pro_for_team, {
                count: actualSeats,
              })}
            </p>
            {pricesLoading ? (
              <div
                className="space-y-2 motion-reduce:[&_[data-slot=skeleton]]:animate-none"
                role="status"
                aria-label={t(($) => $.workspace.upgrade.price_loading)}
              >
                <Skeleton className="h-5 w-48" />
                <Skeleton className="h-4 w-64 max-w-full" />
              </div>
            ) : hasDisplayableUnitPrice ? (
              <div className="space-y-1">
                <p className="text-body font-semibold tabular-nums">
                  {t(($) => $.workspace.upgrade.unit_price, {
                    price: formattedUnitPrice,
                  })}
                </p>
                {hasDisplayableEstimatedTotal ? (
                  <p className="text-caption leading-5 text-muted-foreground tabular-nums">
                    {t(
                      interval === "month"
                        ? ($) => $.workspace.upgrade.estimated_monthly_total
                        : ($) => $.workspace.upgrade.estimated_yearly_total,
                      { price: formattedEstimatedTotal },
                    )}
                  </p>
                ) : null}
              </div>
            ) : null}
            <p className="max-w-[65ch] text-caption leading-5 text-muted-foreground">
              {t(($) => $.workspace.upgrade.price_at_checkout)}
            </p>
            {canRetryPrice ? (
              <>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  aria-busy={pricesFetching}
                  disabled={pricesFetching}
                  onClick={onRetryPrices}
                >
                  {pricesFetching ? (
                    <Loader2 className="animate-spin" />
                  ) : (
                    <RefreshCw />
                  )}
                  {t(($) => $.workspace.actions.retry)}
                </Button>
                {pricesFetching ? (
                  <span
                    className="sr-only"
                    role="status"
                    aria-label={t(($) => $.workspace.upgrade.price_loading)}
                  >
                    {t(($) => $.workspace.upgrade.price_loading)}
                  </span>
                ) : null}
              </>
            ) : null}
          </div>
          {canManage ? (
            <Button
              className="h-11 w-full sm:w-auto"
              disabled={isMutating}
              onClick={onOpenCheckoutConfirm}
            >
              <CreditCard />
              {t(($) => $.workspace.actions.upgrade)}
            </Button>
          ) : null}
        </div>
      </SettingsCard>
    </SettingsSection>
  );
}
