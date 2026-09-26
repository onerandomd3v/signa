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
    await waitFor(() =>
      expect(result.current.alerts[0]?.status).toBe("unavailable"),
    );
    expect(JSON.stringify(result.current.alerts)).not.toContain("Authorized");
    unmount();
  });

  it("discards an alert hidden by 404", async () => {
    const readAlert = vi
      .fn<AlertReader>()
      .mockResolvedValue({ status: "not-found" });
    const { result, unmount } = renderHook(() =>
      useAlertReconciliation({ readAlert }),
    );

    act(() => result.current.acceptEvents([event(firstId)]));
    await waitFor(() => expect(readAlert).toHaveBeenCalledOnce());
    await waitFor(() => expect(result.current.alerts).toHaveLength(0));
    unmount();
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
