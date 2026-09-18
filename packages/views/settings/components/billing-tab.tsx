"use client";

import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertCircle, RefreshCw } from "lucide-react";
import { ApiError } from "@multica/core/api";
import { autopilotQuotaUsageOptions } from "@multica/core/autopilots";
import {
  useCreateWorkspaceSubscriptionCheckout,
  useCreateWorkspaceSubscriptionPortal,
  useReconcileWorkspaceSubscriptionSeats,
  workspaceSubscriptionEntitlementsOptions,
  workspaceSubscriptionPricesOptions,
  workspaceSubscriptionSummaryOptions,
} from "@multica/core/billing";
import { useFeatureEnabled } from "@multica/core/config";
import { BILLING_WORKSPACE_SUBSCRIPTIONS_FLAG } from "@multica/core/feature-flags";
import { useCurrentMember } from "@multica/core/permissions";
import { useCurrentWorkspace } from "@multica/core/paths";
import type { WorkspaceSubscriptionInterval } from "@multica/core/types";
import {
  Alert,
  AlertDescription,
  AlertTitle,
} from "@multica/ui/components/ui/alert";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useLocale, useT } from "../../i18n";
import { useNavigation } from "../../navigation";
import { openExternal } from "../../platform";
import {
  SettingsCard,
  SettingsTab,
} from "./settings-layout";
import {
  canPurchaseWorkspaceSubscription,
  hasManagedWorkspaceSubscription,
  resolveAutopilotUsage,
} from "./billing-state";
import {
  CHECKOUT_SYNC_TIMEOUT_MS,
  createIdempotencyKey,
  formatDate,
  formatDateTime,
  parseReturnResult,
  type WorkspaceBillingReturnResult,
} from "./billing/billing-currency";
import { BillingAlerts } from "./billing/billing-alerts";
import { BillingCurrentPlan } from "./billing/billing-current-plan";
import { BillingPlanCards } from "./billing/billing-plan-cards";
import { BillingUsageMeters } from "./billing/billing-usage-meters";
import { BillingSeatsSection } from "./billing/billing-seats-section";
import { BillingCheckoutDialog } from "./billing/billing-checkout-dialog";

export { formatStripeMinorAmount } from "./billing/billing-currency";

/**
 * A second gate inside the tab keeps direct/test mounts fail-closed. The
 * Settings shell also omits this component and its navigation entry while the
 * flag is absent, so no subscription request is issued in either path.
 */
export function BillingTab() {
  const enabled = useFeatureEnabled(
    BILLING_WORKSPACE_SUBSCRIPTIONS_FLAG,
    false,
  );
  return enabled ? <BillingTabContent /> : null;
}

function BillingTabContent() {
  const { t } = useT("billing");
  const locale = useLocale();
  const navigation = useNavigation();
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id ?? "";
  const currentMember = useCurrentMember(wsId);
  const canManage =
    currentMember.role === "owner" || currentMember.role === "admin";
  const returnResultParam = parseReturnResult(
    navigation.searchParams.get("result"),
  );
  const returnSessionId = navigation.searchParams.get("session_id");
  const callbackKey =
    navigation.searchParams.has("result") ||
    navigation.searchParams.has("session_id")
      ? `${navigation.searchParams.get("result") ?? ""}:${returnSessionId ?? ""}`
      : null;
  const [returnState, setReturnState] = useState<{
    workspaceId: string | null;
    result: WorkspaceBillingReturnResult | null;
    observedAt: number | null;
  }>(() => ({
    workspaceId: wsId || null,
    result: returnResultParam,
    observedAt: returnResultParam === "success" ? Date.now() : null,
  }));
  const returnStateMatchesWorkspace =
    returnState.workspaceId === null || returnState.workspaceId === wsId;
  const returnResult = returnStateMatchesWorkspace ? returnState.result : null;
  const returnObservedAt = returnStateMatchesWorkspace
    ? returnState.observedAt
    : null;
  const [interval, setInterval] =
    useState<WorkspaceSubscriptionInterval>("month");
  const [checkoutConfirmOpen, setCheckoutConfirmOpen] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [portalUnavailable, setPortalUnavailable] = useState(false);
  const [reconcileMessage, setReconcileMessage] = useState<string | null>(null);
  const [isSyncingCheckout, setIsSyncingCheckout] = useState(
    returnResult === "success",
  );
  const [syncTimedOut, setSyncTimedOut] = useState(false);
  const checkoutIntentRef = useRef<{
    wsId: string;
    interval: WorkspaceSubscriptionInterval;
    key: string;
  } | null>(null);
  const portalIntentKeyRef = useRef<string | null>(null);
  const consumedCallbackKeyRef = useRef<string | null>(null);

  useEffect(() => {
    checkoutIntentRef.current = null;
    portalIntentKeyRef.current = null;
    setPortalUnavailable(false);
    setActionError(null);
    setReconcileMessage(null);
  }, [wsId]);

  // Consume Stripe callback params once, then remove them with replace so a
  // refresh, copied URL, or settings-tab round trip cannot replay a banner or
  // restart subscription polling. Keep unrelated params, especially `tab`.
  useEffect(() => {
    if (!callbackKey || consumedCallbackKeyRef.current === callbackKey) return;
    consumedCallbackKeyRef.current = callbackKey;
    setReturnState({
      workspaceId: wsId || null,
      result: returnResultParam,
      observedAt: returnResultParam === "success" ? Date.now() : null,
    });
    if (returnResultParam === "cancel") checkoutIntentRef.current = null;

    const params = new URLSearchParams(navigation.searchParams);
    params.delete("result");
    params.delete("session_id");
    const query = params.toString();
    navigation.replace(
      query ? `${navigation.pathname}?${query}` : navigation.pathname,
    );
  }, [callbackKey, navigation, returnResultParam, wsId]);

  useEffect(() => {
    if (returnResult !== "success") {
      setIsSyncingCheckout(false);
      setSyncTimedOut(false);
      return;
    }
    setIsSyncingCheckout(true);
    setSyncTimedOut(false);
    const timeout = window.setTimeout(() => {
      setIsSyncingCheckout(false);
      setSyncTimedOut(true);
    }, CHECKOUT_SYNC_TIMEOUT_MS);
    return () => window.clearTimeout(timeout);
  }, [returnResult, wsId]);

  const entitlementQuery = useQuery({
    ...workspaceSubscriptionEntitlementsOptions(wsId),
    refetchInterval: isSyncingCheckout ? 2_000 : false,
  });
  const entitlements = entitlementQuery.data;
  const isCheckoutProConfirmed =
    returnResult === "success" &&
    returnObservedAt !== null &&
    entitlements?.plan === "pro" &&
    entitlementQuery.isFetchedAfterMount &&
    entitlementQuery.dataUpdatedAt >= returnObservedAt;
  const summaryQuery = useQuery({
    ...workspaceSubscriptionSummaryOptions(wsId),
    refetchInterval: isSyncingCheckout ? 2_000 : false,
  });
  const summaryUnavailable =
    summaryQuery.isError ||
    (!summaryQuery.isPending && summaryQuery.data == null);
  const quotaUsageQuery = useQuery(autopilotQuotaUsageOptions(wsId));
  const hasManagedSubscription = entitlements
    ? hasManagedWorkspaceSubscription(entitlements, summaryQuery.data)
    : false;
  const canUpgrade = entitlements
    ? canPurchaseWorkspaceSubscription(entitlements)
    : false;
  const pricesQuery = useQuery({
    ...workspaceSubscriptionPricesOptions(wsId),
    enabled: wsId.length > 0 && canUpgrade,
  });
  const checkoutMutation = useCreateWorkspaceSubscriptionCheckout(wsId);
  const portalMutation = useCreateWorkspaceSubscriptionPortal(wsId);
  const reconcileMutation = useReconcileWorkspaceSubscriptionSeats(wsId);
  const refetchEntitlements = entitlementQuery.refetch;
  const refetchSummary = summaryQuery.refetch;

  useEffect(() => {
    if (isSyncingCheckout && isCheckoutProConfirmed) {
      setIsSyncingCheckout(false);
      setSyncTimedOut(false);
      checkoutIntentRef.current = null;
    }
  }, [isCheckoutProConfirmed, isSyncingCheckout]);

  useEffect(() => {
    const graceUntil = summaryQuery.data?.graceUntil;
    if (!graceUntil) return;
    const graceUntilMs = new Date(graceUntil).getTime();
    if (Number.isNaN(graceUntilMs)) return;
    const delay = Math.max(0, graceUntilMs - Date.now()) + 100;
    const timeout = window.setTimeout(() => {
      refetchEntitlements();
      refetchSummary();
    }, Math.min(delay, 2_147_000_000));
    return () => window.clearTimeout(timeout);
  }, [refetchEntitlements, refetchSummary, summaryQuery.data?.graceUntil]);

  const reportActionError = (error: unknown, fallback: string) => {
    if (error instanceof ApiError && error.status === 503) {
      setActionError(t(($) => $.workspace.errors.temporarily_unavailable));
      return;
    }
    if (error instanceof ApiError && error.status === 403) {
      setActionError(t(($) => $.workspace.errors.permission_changed));
      return;
    }
    setActionError(fallback);
  };

  const handleCheckout = async () => {
    setActionError(null);
    const existing = checkoutIntentRef.current;
    const intent =
      existing?.wsId === wsId && existing.interval === interval
        ? existing
        : {
            wsId,
            interval,
            key: createIdempotencyKey("workspace-checkout", wsId),
          };
    checkoutIntentRef.current = intent;
    try {
      const response = await checkoutMutation.mutateAsync({
        interval,
        idempotencyKey: intent.key,
      });
      if (!response?.url) {
        setCheckoutConfirmOpen(false);
        setActionError(t(($) => $.workspace.errors.checkout_response));
        return;
      }
      setCheckoutConfirmOpen(false);
      openExternal(response.url, { webTarget: "same-tab" });
    } catch (error) {
      setCheckoutConfirmOpen(false);
      if (error instanceof ApiError && error.status === 409) {
        checkoutIntentRef.current = null;
        setActionError(t(($) => $.workspace.errors.already_subscribed));
        await Promise.all([entitlementQuery.refetch(), summaryQuery.refetch()]);
        return;
      }
      reportActionError(error, t(($) => $.workspace.errors.checkout_failed));
    }
  };

  const handleCheckoutConfirmOpenChange = (open: boolean) => {
    setCheckoutConfirmOpen(open);
    if (!open) checkoutIntentRef.current = null;
  };

  const handlePortal = async () => {
    setActionError(null);
    const key =
      portalIntentKeyRef.current ??
      createIdempotencyKey("workspace-portal", wsId);
    portalIntentKeyRef.current = key;
    try {
      const response = await portalMutation.mutateAsync(key);
      if (!response?.url) {
        setActionError(t(($) => $.workspace.errors.portal_response));
        return;
      }
      openExternal(response.url, { webTarget: "same-tab" });
      portalIntentKeyRef.current = null;
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) {
        portalIntentKeyRef.current = null;
        setPortalUnavailable(true);
        setActionError(t(($) => $.workspace.errors.portal_unavailable));
        await Promise.all([entitlementQuery.refetch(), summaryQuery.refetch()]);
        return;
      }
      reportActionError(error, t(($) => $.workspace.errors.portal_failed));
    }
  };

  const handleReconcile = async () => {
    setActionError(null);
    setReconcileMessage(null);
    try {
      const response = await reconcileMutation.mutateAsync();
      if (!response) {
        setActionError(t(($) => $.workspace.errors.reconcile_response));
        return;
      }
      setReconcileMessage(
        t(($) => $.workspace.seats.reconciled, {
          actual: response.actualSeats,
          billed: response.billedSeats,
        }),
      );
      await Promise.all([entitlementQuery.refetch(), summaryQuery.refetch()]);
    } catch (error) {
      reportActionError(error, t(($) => $.workspace.errors.reconcile_failed));
    }
  };

  if (entitlementQuery.isPending) {
    return (
      <SettingsTab
        title={t(($) => $.workspace.title)}
        description={t(($) => $.workspace.description)}
      >
        <SettingsCard>
          <div
            className="space-y-4 p-4 motion-reduce:[&_[data-slot=skeleton]]:animate-none"
            aria-label={t(($) => $.workspace.loading)}
          >
            <Skeleton className="h-5 w-40" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-2/3" />
          </div>
        </SettingsCard>
      </SettingsTab>
    );
  }

  if (entitlementQuery.isError || !entitlements) {
    return (
      <SettingsTab
        title={t(($) => $.workspace.title)}
        description={t(($) => $.workspace.description)}
      >
        <Alert variant="destructive">
          <AlertCircle />
          <AlertTitle>{t(($) => $.workspace.load_failed.title)}</AlertTitle>
          <AlertDescription>
            <p>{t(($) => $.workspace.load_failed.description)}</p>
            <Button
              className="mt-3 h-11"
              variant="outline"
              onClick={() => entitlementQuery.refetch()}
            >
              <RefreshCw />
              {t(($) => $.workspace.actions.retry)}
            </Button>
          </AlertDescription>
        </Alert>
      </SettingsTab>
    );
  }

  const summaryPeriodEnd = formatDate(
    summaryQuery.data?.entitlement.currentPeriodEnd ?? null,
    locale,
  );
  const graceUntilValue = summaryQuery.data?.graceUntil ?? null;
  const graceUntil = formatDate(graceUntilValue, locale);
  const graceUntilMs = graceUntilValue
    ? new Date(graceUntilValue).getTime()
    : Number.NaN;
  const hasActiveProGrace =
    entitlements.plan === "pro" &&
    entitlements.status === "past_due" &&
    Number.isFinite(graceUntilMs) &&
    graceUntilMs > Date.now();
  const canUseEntitlementUnlimited =
    entitlements.plan === "pro" &&
    (entitlements.status !== "past_due" || hasActiveProGrace) &&
    (returnResult !== "success" || isCheckoutProConfirmed);
  const actualSeats = summaryQuery.data?.actualSeats ?? entitlements.seats;
  const billedSeats = summaryQuery.data?.billedSeats;
  const pendingSeatQuantity = summaryQuery.data?.pendingSeatQuantity;
  const quotaUsage = resolveAutopilotUsage(
    entitlements,
    quotaUsageQuery.data,
    quotaUsageQuery.isError,
    canUseEntitlementUnlimited,
  );
  const quotaResetAt =
    quotaUsage.kind === "metered"
      ? formatDateTime(quotaUsage.resetAt, locale)
      : null;
  const isMutating =
    checkoutMutation.isPending ||
    portalMutation.isPending ||
    reconcileMutation.isPending;

  return (
    <SettingsTab
      title={t(($) => $.workspace.title)}
      description={t(($) => $.workspace.description)}
    >
      <BillingAlerts
        returnResult={returnResult}
        isCheckoutProConfirmed={isCheckoutProConfirmed}
        isSyncingCheckout={isSyncingCheckout}
        syncTimedOut={syncTimedOut}
        cancelAtPeriodEnd={summaryQuery.data?.cancelAtPeriodEnd}
        summaryPeriodEnd={summaryPeriodEnd}
        entitlements={entitlements}
        hasActiveProGrace={hasActiveProGrace}
        graceUntil={graceUntil}
        canManage={canManage}
        currentMemberLoading={currentMember.isLoading}
        actionError={actionError}
      />

      <BillingCurrentPlan
        entitlements={entitlements}
        summary={summaryQuery.data}
        actualSeats={actualSeats}
        summaryPeriodEnd={summaryPeriodEnd}
        hasManagedSubscription={hasManagedSubscription}
        canManage={canManage}
        portalUnavailable={portalUnavailable}
        portalPending={portalMutation.isPending}
        isMutating={isMutating}
        onPortal={() => void handlePortal()}
      />

      {canUpgrade ? (
        <BillingPlanCards
          interval={interval}
          onIntervalChange={(value) => {
            setInterval(value);
            checkoutIntentRef.current = null;
          }}
          actualSeats={actualSeats}
          prices={pricesQuery.data ?? undefined}
          pricesLoading={pricesQuery.isLoading}
          pricesFetching={pricesQuery.isFetching}
          onRetryPrices={() => pricesQuery.refetch()}
          canManage={canManage}
          isMutating={isMutating}
          locale={locale}
          onOpenCheckoutConfirm={() => setCheckoutConfirmOpen(true)}
        />
      ) : null}

      <BillingUsageMeters
        entitlements={entitlements}
        canUseEntitlementUnlimited={canUseEntitlementUnlimited}
        quotaUsage={quotaUsage}
        quotaUsageLoading={quotaUsageQuery.isPending}
        quotaUsageFetching={quotaUsageQuery.isFetching}
        onRetryQuotaUsage={() => quotaUsageQuery.refetch()}
        quotaResetAt={quotaResetAt}
        locale={locale}
      />

      <BillingSeatsSection
        summaryUnavailable={summaryUnavailable}
        summaryLoading={summaryQuery.isPending}
        summaryFetching={summaryQuery.isFetching}
        onRetrySummary={() => summaryQuery.refetch()}
        reconcileMessage={reconcileMessage}
        actualSeats={actualSeats}
        billedSeats={billedSeats}
        pendingSeatQuantity={pendingSeatQuantity}
        summaryPeriodEnd={summaryPeriodEnd}
        canManage={canManage}
        hasManagedSubscription={hasManagedSubscription}
        isMutating={isMutating}
        reconcilePending={reconcileMutation.isPending}
        onReconcile={() => void handleReconcile()}
      />

      <BillingCheckoutDialog
        open={checkoutConfirmOpen}
        onOpenChange={handleCheckoutConfirmOpenChange}
        interval={interval}
        actualSeats={actualSeats}
        isPending={checkoutMutation.isPending}
        onConfirm={() => void handleCheckout()}
      />
    </SettingsTab>
  );
}
