import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  VerificationRequestView,
  VerificationResponse,
} from "./generated";
import {
  fetchVerificationRequest,
  fetchVerificationRequests,
  sendVerificationResponse,
} from "./verifications";

const requestId = "11111111-1111-4111-8111-111111111111";
const request: VerificationRequestView = {
  id: requestId,
  created_at: "2026-09-29T09:00:00Z",
  expires_at: "2026-09-29T11:00:00Z",
  safety_prompt:
    "Respond only from what you already safely know or observed. Never approach an incident or unsafe area to verify it.",
  incident: {
    id: "22222222-2222-4222-8222-222222222222",
    event_type: "ROAD_CLOSURE",
    status: "OPEN",
    confidence_state: "EMERGING",
    severity: "MODERATE",
    public_geometry: { type: "Polygon", coordinates: [] },
    started_at: null,
    last_signal_at: null,
    updated_at: "2026-09-29T09:00:00Z",
  },
};
const response: VerificationResponse = {
  id: "33333333-3333-4333-8333-333333333333",
  request_id: requestId,
  incident_id: request.incident.id,
  conclusion: "CONFIRM",
  observation: null,
  created_at: "2026-09-29T09:10:00Z",
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function createFetch(response: Response) {
  const calls: { input: RequestInfo | URL; init?: RequestInit }[] = [];
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ input, init });
      return response;
    },
  );
  return { fetcher: fetcher as typeof fetch, calls };
}

describe("verification API client", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("lists requests with the generated operation and cookie credentials", async () => {
    const { fetcher, calls } = createFetch(jsonResponse([request]));
    const result = await fetchVerificationRequests(undefined, fetcher);

    expect(result).toEqual({ ok: true, data: [request] });
    expect(fetcher).toHaveBeenCalledOnce();
    const requestOptions = calls[0].input as Request;
    expect(requestOptions.url).toBe(
      "http://localhost:8080/v1/verifications/requests",
    );
    expect(requestOptions).toMatchObject({
      method: "GET",
      credentials: "include",
    });
  });

  it("reads one request using the generated parameterized operation", async () => {
    const signal = new AbortController().signal;
    const { fetcher, calls } = createFetch(jsonResponse(request));
    const result = await fetchVerificationRequest(requestId, signal, fetcher);

    expect(result).toEqual({ ok: true, data: request });
    const requestOptions = calls[0].input as Request;
    expect(requestOptions.url).toBe(
      `http://localhost:8080/v1/verifications/requests/${requestId}`,
    );
    expect(requestOptions).toMatchObject({
      method: "GET",
      credentials: "include",
      signal,
    });
  });

  it.each([200, 201])(
    "accepts %i as successful response submission and sends the key",
    async (status) => {
      const { fetcher, calls } = createFetch(jsonResponse(response, status));
      const result = await sendVerificationResponse(
        requestId,
        { conclusion: "CONFIRM" },
        "opaque-idempotency-key",
        fetcher,
      );

      expect(result).toEqual({ ok: true, data: response });
      const requestOptions = calls[0].input as Request;
      expect(requestOptions.url).toBe(
        `http://localhost:8080/v1/verifications/requests/${requestId}/responses`,
      );
      expect(requestOptions.method).toBe("POST");
      expect(requestOptions.credentials).toBe("include");
      expect(requestOptions.headers.get("Idempotency-Key")).toBe(
        "opaque-idempotency-key",
      );
      expect(requestOptions.headers.get("Content-Type")).toBe(
        "application/json",
      );
      await expect(requestOptions.clone().text()).resolves.toBe(
        JSON.stringify({ conclusion: "CONFIRM" }),
      );
    },
  );

  it.each([400, 401, 403, 404, 409, 503])(
    "returns HTTP %i without exposing server error content",
    async (status) => {
      const { fetcher } = createFetch(
        jsonResponse(
          { error: { code: "PRIVATE", message: "internal detail" } },
          status,
        ),
      );
      await expect(
        sendVerificationResponse(
          requestId,
          { observation: "HEARD" },
          "key",
          fetcher,
        ),
      ).resolves.toEqual({ ok: false, status });
    },
  );

  it("maps network failures to a safe recoverable result", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockRejectedValue(new Error("private transport details"));
    await expect(
      fetchVerificationRequests(undefined, fetcher),
    ).resolves.toEqual({ ok: false, status: 0 });
  });

  it("rejects a malformed inbox payload instead of trusting a non-list response", async () => {
    const { fetcher } = createFetch(jsonResponse({ requests: [request] }));
    await expect(
      fetchVerificationRequests(undefined, fetcher),
    ).resolves.toEqual({
      ok: false,
      status: 0,
    });
  });
});
