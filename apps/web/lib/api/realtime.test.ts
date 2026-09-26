import { afterEach, describe, expect, it, vi } from "vitest";
import { getRealtimeHttpStatus, streamAuthenticatedEvents } from "./realtime";

const sseResponse = () =>
  new Response(
    'id: next-cursor\nevent: incident.created.v1\ndata: {"private":"ignored"}\n\n',
    {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    },
  );

afterEach(() => vi.unstubAllEnvs());

describe("streamAuthenticatedEvents", () => {
  it("uses the generated endpoint with credentialed browser cookies", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_BASE_URL", "https://api.example.test");
    const fetchImplementation = vi
      .fn<typeof fetch>()
      .mockResolvedValue(sseResponse());
    const controller = new AbortController();
    const onEvent = vi.fn();
    const result = await streamAuthenticatedEvents({
      signal: controller.signal,
      fetchImplementation,
      onSseEvent: onEvent,
      sseSleepFn: vi.fn().mockResolvedValue(undefined),
    });

    await result.stream.next();

    const request = fetchImplementation.mock.calls[0][0] as Request;
    expect(request.url).toBe("https://api.example.test/events");
    expect(request.credentials).toBe("include");
    expect(onEvent).toHaveBeenCalledWith(
      expect.objectContaining({
        id: "next-cursor",
        event: "incident.created.v1",
        data: { private: "ignored" },
      }),
    );
  });

  it("passes an opaque cursor as Last-Event-ID without interpreting it", async () => {
    const fetchImplementation = vi
      .fn<typeof fetch>()
      .mockResolvedValue(sseResponse());
    const controller = new AbortController();
    const result = await streamAuthenticatedEvents({
      signal: controller.signal,
      lastEventId: "opaque-base64url-cursor",
      fetchImplementation,
      sseSleepFn: vi.fn().mockResolvedValue(undefined),
    });

    await result.stream.next();

    const request = fetchImplementation.mock.calls[0][0] as Request;
    expect(request.headers.get("Last-Event-ID")).toBe(
      "opaque-base64url-cursor",
    );
  });

  it("uses the generated client's bounded network retry and reports HTTP errors", async () => {
    const fetchImplementation = vi
      .fn<typeof fetch>()
      .mockRejectedValueOnce(new Error("network down"))
      .mockResolvedValue(sseResponse());
    const sleep = vi.fn().mockResolvedValue(undefined);
    const onError = vi.fn();
    const result = await streamAuthenticatedEvents({
      signal: new AbortController().signal,
      fetchImplementation,
      onSseError: onError,
      sseSleepFn: sleep,
    });

    for await (const event of result.stream) {
      // Consume the event stream to completion.
      void event;
    }

    expect(fetchImplementation).toHaveBeenCalledTimes(2);
    expect(sleep).toHaveBeenCalledOnce();
    expect(onError).toHaveBeenCalledOnce();
  });

  it("recognizes only the status exposed by the generated stream error", () => {
    expect(
      getRealtimeHttpStatus(new Error("SSE failed: 401 Unauthorized")),
    ).toBe(401);
    expect(getRealtimeHttpStatus(new Error("offline"))).toBeNull();
  });
});
