import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AlertRead } from "@/lib/api/generated";
import type { AlertReader } from "./use-alert-reconciliation";
import { useAlertReconciliation } from "./use-alert-reconciliation";

const firstId = "550e8400-e29b-41d4-a716-446655440000";
const incidentId = "550e8400-e29b-41d4-a716-446655440001";

function snapshot(alertId: string, asOf: string): AlertRead {
  return {
    alert_id: alertId,
    incident_id: incidentId,
    alert_type: "IMMEDIATE",
    confidence_snapshot: "CORROBORATED",
    severity_snapshot: "HIGH",
    status_snapshot: "OPEN",
    priority_snapshot: "P1",
    freshness_snapshot: "FRESH",
    message: `Authorized ${alertId}`,
    as_of: asOf,
    created_at: asOf,
    supersedes_alert_id: null,
  };
}

function event(alertId: string, relatedIncidentId = incidentId) {
  return { alertId, incidentId: relatedIncidentId };
}

describe("useAlertReconciliation", () => {
  it("deduplicates repeated event IDs and displays only the authorized snapshot", async () => {
    const readAlert = vi.fn<AlertReader>(async (alertId) => ({
      status: "authorized",
      alert: snapshot(alertId, "2026-09-26T10:00:00Z"),
    }));
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() =>
      result.current.acceptEvents([
        event(firstId),
        event(firstId),
        event(firstId),
      ]),
    );

    await waitFor(() => expect(result.current.alerts).toHaveLength(1));
    expect(readAlert).toHaveBeenCalledOnce();
    expect(result.current.alerts[0]).toMatchObject({
      status: "authorized",
      alert: { message: `Authorized ${firstId}` },
    });
    unmount();
  });

  it("rejects malformed alert and incident IDs before making a request", () => {
    const readAlert: AlertReader = vi.fn();
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() =>
      result.current.acceptEvents([
        event("not-a-uuid"),
        event(firstId, "not-an-incident-uuid"),
      ]),
    );
    expect(readAlert).not.toHaveBeenCalled();
    expect(result.current.alerts).toEqual([]);
    unmount();
  });

  it("removes protected data after 401", async () => {
    const readAlert = vi
      .fn<AlertReader>()
      .mockResolvedValueOnce({
        status: "authorized",
        alert: snapshot(firstId, "2026-09-26T10:00:00Z"),
      })
      .mockResolvedValueOnce({ status: "unauthorized" });
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await waitFor(() =>
      expect(result.current.alerts[0]?.status).toBe("authorized"),
    );
    act(() => result.current.retry(firstId));
    await waitFor(() => expect(result.current.alerts).toEqual([]));
    expect(JSON.stringify(result.current.alerts)).not.toContain("Authorized");
    unmount();
  });

  it("keeps a direct REST 404 privacy-safe without scheduling retries", async () => {
    const readAlert = vi
      .fn<AlertReader>()
      .mockResolvedValue({ status: "not-found" });
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.retry(firstId));
    await waitFor(() => expect(readAlert).toHaveBeenCalledOnce());
    await waitFor(() => expect(result.current.alerts).toHaveLength(0));
    unmount();
  });

  it("retries an SSE 404 with bounded backoff and renders only the authorized response", async () => {
    vi.useFakeTimers();
    const readAlert = vi
      .fn<AlertReader>()
      .mockResolvedValueOnce({ status: "not-found" })
      .mockResolvedValueOnce({
        status: "authorized",
        alert: snapshot(firstId, "2026-09-26T10:00:00Z"),
      });
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await act(async () => Promise.resolve());
    expect(readAlert).toHaveBeenCalledTimes(1);
    expect(result.current.alerts).toEqual([]);
    expect(JSON.stringify(result.current.alerts)).not.toContain(firstId);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(999);
    });
    expect(readAlert).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(readAlert).toHaveBeenCalledTimes(2);
    expect(result.current.alerts).toMatchObject([
      {
        status: "authorized",
        alert: { alert_id: firstId, message: `Authorized ${firstId}` },
      },
    ]);

    unmount();
    vi.useRealTimers();
  });

  it("silently exhausts three SSE 404 retries and does not restart on reconnect", async () => {
    vi.useFakeTimers();
    const readAlert = vi
      .fn<AlertReader>()
      .mockResolvedValue({ status: "not-found" });
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await act(async () => Promise.resolve());
    for (const [delay, expectedCalls] of [
      [1_000, 2],
      [2_000, 3],
      [4_000, 4],
    ] as const) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(delay);
      });
      expect(readAlert).toHaveBeenCalledTimes(expectedCalls);
      expect(result.current.alerts).toEqual([]);
    }

    act(() => result.current.revalidateKnown());
    act(() => result.current.acceptEvents([event(firstId)]));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(readAlert).toHaveBeenCalledTimes(4);
    expect(result.current.alerts).toEqual([]);

    unmount();
    vi.useRealTimers();
  });

  it("revalidates pending visibility on reconnect without resetting the retry budget", async () => {
    vi.useFakeTimers();
    const readAlert = vi
      .fn<AlertReader>()
      .mockResolvedValue({ status: "not-found" });
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await act(async () => Promise.resolve());
    act(() => result.current.revalidateKnown());
    await act(async () => Promise.resolve());
    expect(readAlert).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_999);
    });
    expect(readAlert).toHaveBeenCalledTimes(2);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(readAlert).toHaveBeenCalledTimes(3);
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    act(() => result.current.revalidateKnown());
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(readAlert).toHaveBeenCalledTimes(4);
    act(() => result.current.revalidateKnown());
    await act(async () => Promise.resolve());
    expect(readAlert).toHaveBeenCalledTimes(4);
    expect(result.current.alerts).toEqual([]);

    unmount();
    vi.useRealTimers();
  });

  it("keeps a transient failure degraded and explicitly retryable without leaving a stranded pending ID", async () => {
    vi.useFakeTimers();
    const readAlert = vi
      .fn<AlertReader>()
      .mockResolvedValueOnce({ status: "not-found" })
      .mockResolvedValueOnce({ status: "unavailable" })
      .mockResolvedValueOnce({
        status: "authorized",
        alert: snapshot(firstId, "2026-09-26T10:00:00Z"),
      });
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await act(async () => Promise.resolve());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(readAlert).toHaveBeenCalledTimes(2);
    expect(result.current.alerts[0]?.status).toBe("unavailable");

    act(() => result.current.revalidateKnown());
    await act(async () => Promise.resolve());
    expect(readAlert).toHaveBeenCalledTimes(2);

    act(() => result.current.retry(firstId));
    await act(async () => Promise.resolve());
    expect(readAlert).toHaveBeenCalledTimes(3);
    expect(result.current.alerts[0]?.status).toBe("authorized");

    unmount();
    vi.useRealTimers();
  });

  it("terminally expires a due retry when queue saturation prevents admission", async () => {
    vi.useFakeTimers();
    const otherIds = Array.from(
      { length: 15 },
      (_, index) =>
        `550e8400-e29b-41d4-a716-${String(index + 100).padStart(12, "0")}`,
    );
    const requests = new Map<
      string,
      (value: Awaited<ReturnType<AlertReader>>) => void
    >();
    const signals = new Map<string, AbortSignal>();
    let active = 0;
    let maxActive = 0;
    const readAlert = vi.fn<AlertReader>(
      (alertId, signal) =>
        new Promise((resolve) => {
          let settled = false;
          const finish = (value: Awaited<ReturnType<AlertReader>>) => {
            if (settled) return;
            settled = true;
            active -= 1;
            resolve(value);
          };
          active += 1;
          maxActive = Math.max(maxActive, active);
          requests.set(alertId, finish);
          signals.set(alertId, signal);
          signal.addEventListener(
            "abort",
            () => finish({ status: "unavailable" }),
            { once: true },
          );
        }),
    );
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await act(async () => Promise.resolve());
    act(() => requests.get(firstId)?.({ status: "not-found" }));
    await act(async () => Promise.resolve());
    expect(vi.getTimerCount()).toBe(1);

    act(() => result.current.acceptEvents(otherIds.map((id) => event(id))));
    expect(readAlert).toHaveBeenCalledTimes(4);
    expect(maxActive).toBe(3);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
    });
    expect(result.current.hasOverflow).toBe(true);
    expect(vi.getTimerCount()).toBe(0);

    await act(async () => {
      requests.get(otherIds[0])?.({ status: "unavailable" });
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(readAlert).toHaveBeenCalledTimes(5);
    expect(maxActive).toBe(3);

    act(() => result.current.revalidateKnown());
    act(() => result.current.acceptEvents([event(firstId)]));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(readAlert.mock.calls.filter(([id]) => id === firstId)).toHaveLength(
      1,
    );
    expect(signals.get(firstId)?.aborted).toBe(false);

    unmount();
    expect(active).toBe(0);
    vi.useRealTimers();
  });

  it("expires a pending retry when reconnect cannot enqueue it, then makes no retry when capacity returns", async () => {
    vi.useFakeTimers();
    const otherIds = Array.from(
      { length: 15 },
      (_, index) =>
        `550e8400-e29b-41d4-a716-${String(index + 200).padStart(12, "0")}`,
    );
    const requests = new Map<
      string,
      (value: Awaited<ReturnType<AlertReader>>) => void
    >();
    const readAlert = vi.fn<AlertReader>(
      (alertId, signal) =>
        new Promise((resolve) => {
          let settled = false;
          const finish = (value: Awaited<ReturnType<AlertReader>>) => {
            if (settled) return;
            settled = true;
            resolve(value);
          };
          requests.set(alertId, finish);
          signal.addEventListener(
            "abort",
            () => finish({ status: "unavailable" }),
            { once: true },
          );
        }),
    );
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await act(async () => Promise.resolve());
    act(() => requests.get(firstId)?.({ status: "not-found" }));
    await act(async () => Promise.resolve());
    act(() => result.current.acceptEvents(otherIds.map((id) => event(id))));
    expect(readAlert).toHaveBeenCalledTimes(4);
    expect(vi.getTimerCount()).toBe(1);

    act(() => result.current.revalidateKnown());
    expect(result.current.hasOverflow).toBe(true);
    expect(vi.getTimerCount()).toBe(0);

    await act(async () => {
      requests.get(otherIds[0])?.({ status: "unavailable" });
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(readAlert).toHaveBeenCalledTimes(5);
    act(() => result.current.revalidateKnown());
    act(() => result.current.acceptEvents([event(firstId)]));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(readAlert.mock.calls.filter(([id]) => id === firstId)).toHaveLength(
      1,
    );

    unmount();
    vi.useRealTimers();
  });

  it("keeps simultaneous pending-alert retries within the three-read concurrency limit", async () => {
    vi.useFakeTimers();
    const ids = [firstId, incidentId, "550e8400-e29b-41d4-a716-446655440002"];
    const attempts = new Map<string, number>();
    let active = 0;
    let maxActive = 0;
    const readAlert = vi.fn<AlertReader>(
      (alertId, signal) =>
        new Promise((resolve) => {
          let settled = false;
          const finish = (value: Awaited<ReturnType<AlertReader>>) => {
            if (settled) return;
            settled = true;
            active -= 1;
            resolve(value);
          };
          active += 1;
          maxActive = Math.max(maxActive, active);
          const attempt = (attempts.get(alertId) ?? 0) + 1;
          attempts.set(alertId, attempt);
          if (attempt === 1)
            queueMicrotask(() => finish({ status: "not-found" }));
          signal.addEventListener(
            "abort",
            () => finish({ status: "unavailable" }),
            { once: true },
          );
        }),
    );
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents(ids.map((id) => event(id))));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(readAlert).toHaveBeenCalledTimes(3);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000);
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(readAlert).toHaveBeenCalledTimes(6);
    expect(maxActive).toBeLessThanOrEqual(3);
    expect(maxActive).toBe(3);

    unmount();
    expect(active).toBe(0);
    vi.useRealTimers();
  });

  it("cancels pending visibility timers on authentication loss and unmount", async () => {
    vi.useFakeTimers();
    const readAlert = vi
      .fn<AlertReader>()
      .mockResolvedValue({ status: "not-found" });
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await act(async () => Promise.resolve());
    act(() => result.current.clearProtectedAlerts());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(readAlert).toHaveBeenCalledOnce();

    unmount();

    const second = renderHook(() => useAlertReconciliation({ readAlert }));
    act(() => second.result.current.acceptEvents([event(firstId)]));
    await act(async () => Promise.resolve());
    expect(readAlert).toHaveBeenCalledTimes(2);
    second.unmount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(readAlert).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });

  it("keeps an authorized snapshot visibly stale on 503 and recovers by explicit retry", async () => {
    const readAlert = vi
      .fn<AlertReader>()
      .mockResolvedValueOnce({
        status: "authorized",
        alert: snapshot(firstId, "2026-09-26T10:00:00Z"),
      })
      .mockResolvedValueOnce({ status: "unavailable" })
      .mockResolvedValueOnce({
        status: "authorized",
        alert: snapshot(firstId, "2026-09-26T10:05:00Z"),
      });
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await waitFor(() =>
      expect(result.current.alerts[0]?.status).toBe("authorized"),
    );
    act(() => result.current.revalidateKnown());
    await waitFor(() =>
      expect(result.current.alerts[0]).toMatchObject({
        status: "authorized",
        stale: true,
      }),
    );
    act(() => result.current.retry(firstId));
    await waitFor(() =>
      expect(result.current.alerts[0]).toMatchObject({
        status: "authorized",
        stale: false,
        alert: { as_of: "2026-09-26T10:05:00Z" },
      }),
    );
    unmount();
  });

  it("bounds concurrent reads while preserving authoritative ordering and capped history", async () => {
    const ids = Array.from(
      { length: 5 },
      (_, index) =>
        `550e8400-e29b-41d4-a716-${String(index + 1).padStart(12, "0")}`,
    );
    const pending = new Map<
      string,
      (value: Awaited<ReturnType<AlertReader>>) => void
    >();
    const readAlert = vi.fn<AlertReader>(
      (alertId) =>
        new Promise((resolve) => {
          pending.set(alertId, resolve);
        }),
    );
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents(ids.map((id) => event(id))));
    await waitFor(() => expect(readAlert).toHaveBeenCalledTimes(3));
    act(() => {
      pending.get(ids[2])?.({
        status: "authorized",
        alert: snapshot(ids[2], "2026-09-26T10:05:00Z"),
      });
    });
    await waitFor(() => expect(readAlert).toHaveBeenCalledTimes(4));
    act(() => {
      pending.get(ids[3])?.({
        status: "authorized",
        alert: snapshot(ids[3], "2026-09-26T10:03:00Z"),
      });
    });
    await waitFor(() => expect(readAlert).toHaveBeenCalledTimes(5));
    act(() => {
      pending.get(ids[4])?.({
        status: "authorized",
        alert: snapshot(ids[4], "2026-09-26T10:04:00Z"),
      });
      pending.get(ids[0])?.({
        status: "authorized",
        alert: snapshot(ids[0], "2026-09-26T10:00:00Z"),
      });
      pending.get(ids[1])?.({
        status: "authorized",
        alert: snapshot(ids[1], "2026-09-26T10:01:00Z"),
      });
    });
    await waitFor(() => expect(result.current.alerts).toHaveLength(3));
    const newest = result.current.alerts[0];
    expect(newest?.status).toBe("authorized");
    if (newest?.status === "authorized") {
      expect(newest.alert.alert_id).toBe(ids[2]);
    }
    unmount();
  });

  it("caps active and queued reads and exposes a single overload state", async () => {
    const ids = Array.from(
      { length: 20 },
      (_, index) =>
        `550e8400-e29b-41d4-a716-${String(index + 1).padStart(12, "0")}`,
    );
    const settle = new Map<
      string,
      (result: Awaited<ReturnType<AlertReader>>) => void
    >();
    let active = 0;
    let maxActive = 0;
    const readAlert = vi.fn<AlertReader>(
      (alertId, signal) =>
        new Promise((resolve) => {
          let finished = false;
          const finish = (value: Awaited<ReturnType<AlertReader>>) => {
            if (finished) return;
            finished = true;
            active -= 1;
            resolve(value);
          };
          active += 1;
          maxActive = Math.max(maxActive, active);
          settle.set(alertId, finish);
          signal.addEventListener(
            "abort",
            () => finish({ status: "unavailable" }),
            { once: true },
          );
        }),
    );
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents(ids.map((id) => event(id))));
    await waitFor(() => expect(readAlert).toHaveBeenCalledTimes(3));
    expect(result.current.hasOverflow).toBe(true);
    expect(maxActive).toBe(3);

    act(() => settle.get(ids[0])?.({ status: "not-found" }));
    await waitFor(() => expect(readAlert).toHaveBeenCalledTimes(4));
    expect(maxActive).toBe(3);
    unmount();
    expect(active).toBe(0);
  });

  it("aborts active reads and discards queued reads after SSE authentication loss", async () => {
    const ids = Array.from(
      { length: 4 },
      (_, index) =>
        `550e8400-e29b-41d4-a716-${String(index + 30).padStart(12, "0")}`,
    );
    const signals = new Map<string, AbortSignal>();
    const settle = new Map<
      string,
      (result: Awaited<ReturnType<AlertReader>>) => void
    >();
    const readAlert = vi.fn<AlertReader>(
      (alertId, signal) =>
        new Promise((resolve) => {
          signals.set(alertId, signal);
          settle.set(alertId, resolve);
        }),
    );
    const { result } = renderHook(() => useAlertReconciliation({ readAlert }));

    act(() => result.current.acceptEvents(ids.map((id) => event(id))));
    await waitFor(() => expect(readAlert).toHaveBeenCalledTimes(3));
    expect(result.current.hasOverflow).toBe(false);

    act(() => result.current.clearProtectedAlerts());
    expect(ids.slice(0, 3).every((id) => signals.get(id)?.aborted)).toBe(true);
    expect(result.current.alerts).toEqual([]);

    act(() => result.current.acceptEvents([event(ids[3])]));
    expect(readAlert).toHaveBeenCalledTimes(3);

    await act(async () => {
      for (const id of ids.slice(0, 3)) {
        settle.get(id)?.({
          status: "authorized",
          alert: snapshot(id, "2026-09-26T10:00:00Z"),
        });
      }
      await Promise.resolve();
    });
    expect(result.current.alerts).toEqual([]);
    expect(readAlert).toHaveBeenCalledTimes(3);
  });

  it("aborts in-flight alert requests on unmount", async () => {
    let requestSignal: AbortSignal | undefined;
    const readAlert = vi.fn<AlertReader>(
      (_alertId, signal) =>
        new Promise((resolve) => {
          requestSignal = signal;
          signal.addEventListener(
            "abort",
            () => resolve({ status: "unavailable" }),
            {
              once: true,
            },
          );
        }),
    );
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await waitFor(() => expect(readAlert).toHaveBeenCalledOnce());
    unmount();
    expect(requestSignal?.aborted).toBe(true);
  });
});
