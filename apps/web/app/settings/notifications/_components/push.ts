export type PushState =
  | "checking"
  | "unsupported"
  | "not-requested"
  | "denied"
  | "denied-subscribed"
  | "granted"
  | "subscribed";

export class PushUnavailableError extends Error {}

export function getVapidPublicKey(): string | undefined {
  const key = process.env.NEXT_PUBLIC_VAPID_PUBLIC_KEY?.trim();
  return key || undefined;
}

export function decodeVapidPublicKey(value: string): Uint8Array<ArrayBuffer> {
  const normalized = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, "=");

  try {
    const binary = window.atob(padded);
    const bytes = new Uint8Array(binary.length);
    for (let index = 0; index < binary.length; index += 1) {
      bytes[index] = binary.charCodeAt(index);
    }
    if (bytes.length !== 65 || bytes[0] !== 4) throw new Error("Invalid key");
    return bytes;
  } catch {
    throw new PushUnavailableError("The public VAPID key is invalid.");
  }
}

function browserSupportsPush(): boolean {
  return (
    typeof window !== "undefined" &&
    window.isSecureContext &&
    "Notification" in window &&
    "serviceWorker" in navigator &&
    "PushManager" in window
  );
}

export async function readPushState(): Promise<PushState> {
  if (!browserSupportsPush()) return "unsupported";

  const registration =
    await navigator.serviceWorker.getRegistration("/service-worker.js");
  const subscription = await registration?.pushManager.getSubscription();
  if (Notification.permission === "denied") {
    return subscription ? "denied-subscribed" : "denied";
  }
  if (subscription) return "subscribed";
  return Notification.permission === "granted" ? "granted" : "not-requested";
}

export async function enablePush(publicKey: string): Promise<PushState> {
  if (!browserSupportsPush()) return "unsupported";

  const applicationServerKey = decodeVapidPublicKey(publicKey);
  const permission = await Notification.requestPermission();
  if (permission === "denied") return "denied";
  if (permission !== "granted") return "not-requested";

  const registration = await navigator.serviceWorker.register(
    "/service-worker.js",
    { scope: "/" },
  );
  const existingSubscription = await registration.pushManager.getSubscription();
  if (existingSubscription) return "subscribed";

  await registration.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey,
  });
  return "subscribed";
}

export async function disablePush(): Promise<PushState> {
  if (!browserSupportsPush()) return "unsupported";
  const registration =
    await navigator.serviceWorker.getRegistration("/service-worker.js");
  const subscription = await registration?.pushManager.getSubscription();
  if (subscription && !(await subscription.unsubscribe())) {
    throw new PushUnavailableError(
      "The browser could not remove the subscription.",
    );
  }

  // Unsubscribing removes this browser's push endpoint; it does not revoke
  // Notification permission or remove an endpoint saved by a server.
  return Notification.permission === "denied" ? "denied" : "granted";
}
