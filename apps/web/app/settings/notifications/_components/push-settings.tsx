"use client";

import { useCallback, useEffect, useState } from "react";
import { Button } from "../../../../components/ui/button";
import {
  disablePush,
  enablePush,
  getVapidPublicKey,
  readPushState,
  type PushState,
} from "./push";

const stateLabels: Record<PushState, string> = {
  checking: "Checking browser support…",
  unsupported: "Push notifications are unavailable in this browser or context.",
  "not-requested": "Notifications are off. Permission hasn’t been requested.",
  "default-subscribed":
    "Permission hasn’t been granted. A push subscription remains in this browser.",
  denied: "Notifications are blocked in this browser.",
  "denied-subscribed":
    "Permission is blocked, but a push subscription remains in this browser.",
  granted: "Permission is allowed, but this browser isn’t subscribed.",
  subscribed: "This browser is subscribed to push notifications.",
};

export function PushSettings() {
  const [state, setState] = useState<PushState>("checking");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const publicKey = getVapidPublicKey();

  const refresh = useCallback(async () => {
    try {
      setState(await readPushState());
    } catch {
      setMessage("Couldn’t check this browser’s notification settings.");
    }
  }, []);

  useEffect(() => {
    let active = true;
    void readPushState()
      .then((nextState) => {
        if (active) setState(nextState);
      })
      .catch(() => {
        if (active)
          setMessage("Couldn’t check this browser’s notification settings.");
      });
    function onVisibilityChange() {
      if (document.visibilityState === "visible") void refresh();
    }
    document.addEventListener("visibilitychange", onVisibilityChange);
    let permissionStatus: PermissionStatus | undefined;
    if ("permissions" in navigator) {
      void navigator.permissions
        .query({ name: "notifications" as PermissionName })
        .then((status) => {
          if (!active) return;
          permissionStatus = status;
          status.addEventListener("change", refresh);
        })
        .catch(() => undefined);
    }
    return () => {
      active = false;
      permissionStatus?.removeEventListener("change", refresh);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, [refresh]);

  async function handleEnable() {
    setBusy(true);
    setMessage("");
    try {
      if (!publicKey) {
        setMessage("Push isn’t configured yet. No permission was requested.");
        return;
      }
      const nextState = await enablePush(publicKey);
      setState(nextState);
      if (nextState === "denied") {
        setMessage(
          "Permission is blocked. Change it in your browser settings.",
        );
      } else if (nextState === "subscribed") {
        setMessage(
          "This browser is subscribed. Server delivery is not connected yet.",
        );
      } else if (nextState === "not-requested") {
        setMessage("Permission wasn’t granted. No subscription was created.");
      }
    } catch {
      setMessage(
        "Couldn’t enable push. Check the browser and public key settings.",
      );
      await refresh();
    } finally {
      setBusy(false);
    }
  }

  async function handleDisable() {
    setBusy(true);
    setMessage("");
    try {
      setState(await disablePush());
      setMessage(
        "This browser was unsubscribed. Browser permission remains unchanged.",
      );
    } catch {
      setMessage("Couldn’t remove the browser subscription. Try again.");
      await refresh();
    } finally {
      setBusy(false);
    }
  }

  const canEnable = state === "not-requested" || state === "granted";

  return (
    <div aria-busy={busy || state === "checking"} className="space-y-5">
      <div>
        <h3 className="text-lg font-semibold">Browser push</h3>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">
          Permission is requested only when you choose Enable. Your subscription
          stays in this browser; Signa’s delivery service is not connected yet.
        </p>
      </div>

      <p
        className="text-sm font-medium"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        {busy ? "Updating notification settings…" : stateLabels[state]}
      </p>

      {message && (
        <p className="text-sm leading-6 text-foreground" role="status">
          {message}
        </p>
      )}

      {!publicKey &&
        state !== "subscribed" &&
        state !== "denied-subscribed" && (
          <p className="text-sm leading-6 text-muted-foreground">
            A public VAPID key is required to subscribe. No private key belongs
            in the browser.
          </p>
        )}

      <div className="flex flex-wrap gap-3">
        {canEnable && (
          <Button
            disabled={busy || !publicKey}
            onClick={handleEnable}
            type="button"
          >
            Enable notifications
          </Button>
        )}
        {(state === "subscribed" ||
          state === "denied-subscribed" ||
          state === "default-subscribed") && (
          <Button
            disabled={busy}
            onClick={handleDisable}
            type="button"
            variant="outline"
          >
            Remove browser subscription
          </Button>
        )}
        {state === "unsupported" && (
          <p className="text-sm leading-6 text-muted-foreground">
            Use a supported browser over HTTPS to manage push notifications.
          </p>
        )}
      </div>
    </div>
  );
}
