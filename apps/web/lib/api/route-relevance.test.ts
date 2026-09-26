import { afterEach, describe, expect, it, vi } from "vitest";
import type { RouteRelevanceRequest } from "./generated";
import { requestRouteRelevance } from "./route-relevance";

const requestBody: RouteRelevanceRequest = {
  origin: { latitude: 6.5244, longitude: 3.3792 },
  destination: { latitude: 6.4541, longitude: 3.3947 },
  waypoints: [{ latitude: 6.49, longitude: 3.39 }],
};

const responseBody = {
  classification: "NOT_RELEVANT",
  route_geometry: {
    type: "LineString",
    coordinates: [
      [3.3792, 6.5244],
      [3.3947, 6.4541],
    ],
  },
  incidents: [],
} as const;

afterEach(() => vi.unstubAllEnvs());

describe("requestRouteRelevance", () => {
  it("uses the generated authenticated operation, request body and abort signal", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_BASE_URL", "https://api.example.test/");
    const fetchImplementation = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify(responseBody), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const controller = new AbortController();

    await expect(
      requestRouteRelevance(
        requestBody,
        controller.signal,
        fetchImplementation,
      ),
    ).resolves.toEqual({ status: "success", response: responseBody });

    const request = fetchImplementation.mock.calls[0][0] as Request;
    expect(request.url).toBe("https://api.example.test/v1/route-relevance");
    expect(request.method).toBe("POST");
    expect(request.credentials).toBe("include");
    expect(request.headers.has("Authorization")).toBe(false);
    expect(await request.json()).toEqual(requestBody);
    expect(request.signal.aborted).toBe(false);
  });

  it.each([
    [
      400,
      { error: { code: "invalid_route_request", message: "invalid" } },
      "validation",
    ],
    [
      401,
      { error: { code: "unauthorized", message: "unauthorized" } },
      "unauthorized",
    ],
    [
      403,
      { error: { code: "origin_not_allowed", message: "forbidden" } },
      "forbidden",
    ],
    [
      500,
      { error: { code: "internal_error", message: "private" } },
      "service-error",
    ],
    [
      503,
      { error: { code: "route_unavailable", message: "unavailable" } },
      "route-unavailable",
    ],
    [
      503,
      { error: { code: "incident_data_unavailable", message: "unavailable" } },
      "incident-data-unavailable",
    ],
  ] as const)(
    "maps HTTP %s to %s without exposing error text",
    async (status, body, expected) => {
      const fetchImplementation = vi.fn<typeof fetch>().mockResolvedValue(
        new Response(JSON.stringify(body), {
          status,
          headers: { "Content-Type": "application/json" },
        }),
      );

      await expect(
        requestRouteRelevance(
          requestBody,
          new AbortController().signal,
          fetchImplementation,
        ),
      ).resolves.toEqual({ status: expected });
    },
  );

  it("preserves the Retry-After header for a rate-limited request", async () => {
    const fetchImplementation = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(
        JSON.stringify({ error: { code: "rate_limited", message: "wait" } }),
        {
          status: 429,
          headers: {
            "Content-Type": "application/json",
            "Retry-After": "30",
          },
        },
      ),
    );

    await expect(
      requestRouteRelevance(
        requestBody,
        new AbortController().signal,
        fetchImplementation,
      ),
    ).resolves.toEqual({ status: "rate-limited", retryAfter: "30" });
  });

  it("classifies network failures without exposing transport details", async () => {
    const fetchImplementation = vi
      .fn<typeof fetch>()
      .mockRejectedValue(new Error("private network detail"));

    await expect(
      requestRouteRelevance(
        requestBody,
        new AbortController().signal,
        fetchImplementation,
      ),
    ).resolves.toEqual({ status: "network-error" });
  });

  it("distinguishes cancellation from a network failure", async () => {
    const controller = new AbortController();
    controller.abort();
    const fetchImplementation = vi
      .fn<typeof fetch>()
      .mockRejectedValue(new DOMException("Aborted", "AbortError"));

    await expect(
      requestRouteRelevance(
        requestBody,
        controller.signal,
        fetchImplementation,
      ),
    ).resolves.toEqual({ status: "aborted" });
  });
});
