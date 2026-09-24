import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  decodeVapidPublicKey,
  disablePush,
  enablePush,
  readPushState,
} from "./push";

const publicKey = `B${"A".repeat(86)}`;

function setBrowser({
  permission = "default",
  registration,
}: {
  permission?: NotificationPermission;
  registration?: Partial<ServiceWorkerRegistration>;
} = {}) {
  const getRegistration = vi.fn().mockResolvedValue(registration);
  Object.defineProperty(window, "isSecureContext", {
    configurable: true,
    value: true,
  });
  Object.defineProperty(window, "PushManager", {
    configurable: true,
    value: class PushManager {},
  });
  Object.defineProperty(window, "Notification", {
    configurable: true,
    value: {
      permission,
      requestPermission: vi.fn().mockResolvedValue(permission),
    },
  });
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      getRegistration,
      register: vi.fn().mockResolvedValue(registration),
    },
  });
  return { getRegistration };
}

describe("browser push settings", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("decodes a URL-safe unpadded VAPID public key", () => {
    const decoded = decodeVapidPublicKey(publicKey);
    expect(decoded).toHaveLength(65);
    expect(decoded[0]).toBe(4);
  });

  it("reports unavailable browsers without requesting permission", async () => {
    Object.defineProperty(window, "isSecureContext", {
      configurable: true,
      value: false,
    });
    expect(await readPushState()).toBe("unsupported");
  });

  it("reads permission and existing subscription without prompting", async () => {
    const getSubscription = vi.fn().mockResolvedValue({ unsubscribe: vi.fn() });
    const { getRegistration } = setBrowser({
      permission: "granted",
      registration: {
        pushManager: { getSubscription } as unknown as PushManager,
      },
    });

    expect(await readPushState()).toBe("subscribed");
    expect(getRegistration).toHaveBeenCalledWith("/service-worker.js");
    expect(Notification.requestPermission).not.toHaveBeenCalled();
  });

  it("keeps cleanup available if permission is revoked while subscribed", async () => {
    const getSubscription = vi.fn().mockResolvedValue({ unsubscribe: vi.fn() });
    setBrowser({
      permission: "denied",
      registration: {
        pushManager: { getSubscription } as unknown as PushManager,
      },
    });

    expect(await readPushState()).toBe("denied-subscribed");
  });

  it("requests permission only when enable is called and reuses a subscription", async () => {
    const subscription = { unsubscribe: vi.fn() };
    const subscribe = vi.fn();
    const getSubscription = vi.fn().mockResolvedValue(subscription);
    const register = vi.fn().mockResolvedValue({
      pushManager: { getSubscription, subscribe },
    });
    setBrowser();
    Object.defineProperty(navigator, "serviceWorker", {
      configurable: true,
      value: { getRegistration: vi.fn(), register },
    });
    Object.defineProperty(window, "Notification", {
      configurable: true,
      value: {
        permission: "granted",
        requestPermission: vi.fn().mockResolvedValue("granted"),
      },
    });

    expect(await enablePush(publicKey)).toBe("subscribed");
    expect(Notification.requestPermission).toHaveBeenCalledOnce();
    expect(register).toHaveBeenCalledWith("/service-worker.js", { scope: "/" });
    expect(subscribe).not.toHaveBeenCalled();
  });

  it("creates a visible subscription with the public key after opt-in", async () => {
    const subscribe = vi.fn().mockResolvedValue({ endpoint: "browser-local" });
    const registration = {
      pushManager: {
        getSubscription: vi.fn().mockResolvedValue(null),
        subscribe,
      },
    };
    const register = vi.fn().mockResolvedValue(registration);
    setBrowser();
    Object.defineProperty(navigator, "serviceWorker", {
      configurable: true,
      value: { getRegistration: vi.fn(), register },
    });
    Object.defineProperty(window, "Notification", {
      configurable: true,
      value: {
        permission: "default",
        requestPermission: vi.fn().mockResolvedValue("granted"),
      },
    });

    expect(await enablePush(publicKey)).toBe("subscribed");
    expect(subscribe).toHaveBeenCalledOnce();
    expect(subscribe.mock.calls[0][0]).toMatchObject({ userVisibleOnly: true });
    expect(subscribe.mock.calls[0][0].applicationServerKey).toHaveLength(65);
  });

  it("surfaces subscription failures so the user can retry", async () => {
    const subscribe = vi
      .fn()
      .mockRejectedValue(new Error("subscription failed"));
    const register = vi.fn().mockResolvedValue({
      pushManager: {
        getSubscription: vi.fn().mockResolvedValue(null),
        subscribe,
      },
    });
    setBrowser();
    Object.defineProperty(navigator, "serviceWorker", {
      configurable: true,
      value: { getRegistration: vi.fn(), register },
    });
    Object.defineProperty(window, "Notification", {
      configurable: true,
      value: {
        permission: "granted",
        requestPermission: vi.fn().mockResolvedValue("granted"),
      },
    });

    await expect(enablePush(publicKey)).rejects.toThrow("subscription failed");
    expect(subscribe).toHaveBeenCalledOnce();
  });

  it("does not register a worker when the user denies permission", async () => {
    setBrowser();
    Object.defineProperty(window, "Notification", {
      configurable: true,
      value: {
        permission: "default",
        requestPermission: vi.fn().mockResolvedValue("denied"),
      },
    });

    expect(await enablePush(publicKey)).toBe("denied");
    expect(navigator.serviceWorker.register).not.toHaveBeenCalled();
  });

  it("unsubscribes without unregistering the worker or revoking permission", async () => {
    const unsubscribe = vi.fn().mockResolvedValue(true);
    const unregister = vi.fn();
    setBrowser({
      permission: "granted",
      registration: {
        pushManager: {
          getSubscription: vi.fn().mockResolvedValue({ unsubscribe }),
        } as unknown as PushManager,
        unregister,
      },
    });

    expect(await disablePush()).toBe("granted");
    expect(unsubscribe).toHaveBeenCalledOnce();
    expect(unregister).not.toHaveBeenCalled();
  });
});
