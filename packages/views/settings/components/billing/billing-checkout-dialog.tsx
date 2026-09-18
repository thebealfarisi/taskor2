"use client";

import { ExternalLink, Loader2 } from "lucide-react";
import type { WorkspaceSubscriptionInterval } from "@multica/core/types";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { useT } from "../../../i18n";

interface BillingCheckoutDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  interval: WorkspaceSubscriptionInterval;
  actualSeats: number;
  isPending: boolean;
  onConfirm: () => void;
}

export function BillingCheckoutDialog({
  open,
  onOpenChange,
  interval,
  actualSeats,
  isPending,
  onConfirm,
}: BillingCheckoutDialogProps) {
  const { t } = useT("billing");

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t(($) => $.workspace.confirm.title)}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.workspace.confirm.description, {
              interval:
                interval === "month"
                  ? t(($) => $.workspace.upgrade.monthly)
                  : t(($) => $.workspace.upgrade.yearly),
              count: actualSeats,
            })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel className="h-11" disabled={isPending}>
            {t(($) => $.workspace.actions.cancel)}
          </AlertDialogCancel>
          <AlertDialogAction
            className="h-11"
            disabled={isPending}
            onClick={onConfirm}
          >
            {isPending ? (
              <Loader2 className="animate-spin motion-reduce:animate-none" />
            ) : (
              <ExternalLink />
            )}
            {t(($) => $.workspace.actions.continue_to_stripe)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
