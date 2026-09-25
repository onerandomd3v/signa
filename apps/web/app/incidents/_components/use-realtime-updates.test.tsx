import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AuthenticatedRealtimeOptions } from "@/lib/api/realtime";
import type { RealtimeConnector } from "./use-realtime-updates";
import { sleepWithSignal, useRealtimeUpdates } from "./use-realtime-updates";

function completedStream() {
  return { stream: (async function* () {})() };
}

function waitingStream(signal: AbortSignal) {
  return {
    stream: (async function* () {
      await new Promise<void>((resolve) => {
        if (signal.aborted) resolve();
        else signal.addEventListener("abort", () => resolve(), { once: true });
      });
    })(),
  };
}

describe("useRealtimeUpdates", () => {
  it("coalesces rapid incident and alert notifications and ignores unknown payloads", async () => {
    const connect: RealtimeConnector = vi.fn(async (options) => {
      options.onConnection?.();
      options.onSseEvent?.({
        event: "incident.created.v1",
        id: "opaque-1",
        data: {},
      });
      options.onSseEvent?.({
        event: "incident.status_changed.v1",
        id: "opaque-2",
        data: "not-json",
      });
      options.onSseEvent?.({
        event: "alert.created.v1",
        id: "opaque-3",
        data: { private: "do not render" },
      });
      options.onSseEvent?.({
        event: "future.unsupported.v1",
        data: { corrupt: true },
      });
      return waitingStream(options.signal);
    });
    const onInvalidation = vi.fn();
    const { result, unmount } = renderHook(() =>
      useRealtimeUpdates({ onInvalidation, connect, coalesceMs: 0 }),
    );

    await waitFor(() => expect(onInvalidation).toHaveBeenCalledOnce());
    expect(onInvalidation).toHaveBeenCalledWith({
      incidents: true,
      alerts: true,
    });
    expect(result.current.alertUpdateReceived).toBe(true);
    expect(result.current.status).toBe("live");
    unmount();
  });

  it("reconnects after clean EOF with the latest opaque Last-Event-ID", async () => {
    let secondOptions: AuthenticatedRealtimeOptions | undefined;
    let calls = 0;
    const connect: RealtimeConnector = vi.fn(async (options) => {
      calls += 1;
      if (calls === 1) {
        options.onConnection?.();
        options.onSseEvent?.({
          event: "incident.created.v1",
          id: "opaque-cursor-value",
          data: { incident_id: "ignored" },
        });
        return completedStream();
      }
      secondOptions = options;
      options.onConnection?.();
      return waitingStream(options.signal);
    });
    const sleep = vi.fn(async () => {});
    const { unmount } = renderHook(() =>
      useRealtimeUpdates({ onInvalidation: vi.fn(), connect, sleep }),
    );

    await waitFor(() => expect(connect).toHaveBeenCalledTimes(2));
    expect(secondOptions?.lastEventId).toBe("opaque-cursor-value");
    expect(sleep).toHaveBeenCalledWith(1_000, expect.any(AbortSignal));
    unmount();
  });

  it("marks an unauthenticated 401 unavailable without retrying or affecting REST", async () => {
    const connect: RealtimeConnector = vi.fn(async (options) => {
      options.onSseError?.(new Error("SSE failed: 401 Unauthorized"));
      return completedStream();
    });
    const { result } = renderHook(() =>
      useRealtimeUpdates({ onInvalidation: vi.fn(), connect }),
    );

    await waitFor(() => expect(result.current.status).toBe("unavailable"));
    expect(connect).toHaveBeenCalledOnce();
  });

  it("keeps retrying a 503 with bounded backoff and recovers to connected", async () => {
    let calls = 0;
    const connect: RealtimeConnector = vi.fn(async (options) => {
      calls += 1;
      if (calls === 1) {
        options.onSseError?.(new Error("SSE failed: 503 Service Unavailable"));
        return completedStream();
      }
      options.onConnection?.();
      return waitingStream(options.signal);
    });
    const sleep = vi.fn(async () => {});
    const { result, unmount } = renderHook(() =>
      useRealtimeUpdates({ onInvalidation: vi.fn(), connect, sleep }),
    );

    await waitFor(() => expect(connect).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(result.current.status).toBe("live"));
    expect(sleep).toHaveBeenCalledWith(1_000, expect.any(AbortSignal));
    unmount();
  });

  it("does not start a duplicate stream after rerender and aborts it on unmount", async () => {
    let streamSignal: AbortSignal | undefined;
    const connect: RealtimeConnector = vi.fn(async (options) => {
      streamSignal = options.signal;
      return waitingStream(options.signal);
    });
    const { rerender, unmount } = renderHook(() =>
      useRealtimeUpdates({ onInvalidation: vi.fn(), connect }),
    );

    await waitFor(() => expect(connect).toHaveBeenCalledOnce());
    rerender();
    expect(connect).toHaveBeenCalledOnce();
    unmount();
    expect(streamSignal?.aborted).toBe(true);
  });

  it("aborts a pending reconnect delay when unmounted", async () => {
    let delaySignal: AbortSignal | undefined;
    const connect: RealtimeConnector = vi.fn(async (options) => {
      options.onConnection?.();
      return completedStream();
    });
    const sleep = vi.fn((_ms: number, signal: AbortSignal) => {
      delaySignal = signal;
      return sleepWithSignal(60_000, signal);
    });
    const { unmount } = renderHook(() =>
      useRealtimeUpdates({ onInvalidation: vi.fn(), connect, sleep }),
    );

    await waitFor(() => expect(sleep).toHaveBeenCalledOnce());
    unmount();
    expect(delaySignal?.aborted).toBe(true);
    expect(connect).toHaveBeenCalledOnce();
  });

  it("ignores malformed payloads because only the event name invalidates state", async () => {
    const connect: RealtimeConnector = vi.fn(async (options) => {
      options.onSseEvent?.({ event: "incident.status_changed.v1", data: "{" });
      return waitingStream(options.signal);
    });
    const onInvalidation = vi.fn();
    const { unmount } = renderHook(() =>
      useRealtimeUpdates({ onInvalidation, connect, coalesceMs: 0 }),
    );

    await waitFor(() => expect(onInvalidation).toHaveBeenCalledOnce());
    expect(onInvalidation).toHaveBeenCalledWith({
      incidents: true,
      alerts: false,
    });
    unmount();
  });
});
