import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchAuthorizedAlert } from "./alerts";

const alertRead = {
  alert_id: "550e8400-e29b-41d4-a716-446655440000",
  incident_id: "550e8400-e29b-41d4-a716-446655440001",
  alert_type: "IMMEDIATE",
  confidence_snapshot: "CORROBORATED",
  severity_snapshot: "HIGH",
  status_snapshot: "OPEN",
  priority_snapshot: "P1",
  freshness_snapshot: "FRESH",
  message: "Approved alert snapshot.",
  as_of: "2026-09-26T10:00:00Z",
  created_at: "2026-09-26T10:00:00Z",
  supersedes_alert_id: null,
} as const;

afterEach(() => vi.unstubAllEnvs());

describe("fetchAuthorizedAlert", () => {
  it("uses generated getAlert with credentials and the approved path", async () => {
    vi.stubEnv("NEXT_PUBLIC_API_BASE_URL", "https://api.example.test");
    const fetchImplementation = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify(alertRead), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const controller = new AbortController();

    const result = await fetchAuthorizedAlert(
      alertRead.alert_id,
      controller.signal,
      fetchImplementation,
    );

    expect(result).toEqual({ status: "authorized", alert: alertRead });
    const request = fetchImplementation.mock.calls[0][0] as Request;
    expect(request.url).toBe(
      `https://api.example.test/alerts/${alertRead.alert_id}`,
    );
    expect(request.credentials).toBe("include");
    expect(request.signal.aborted).toBe(false);
    expect(request.headers.has("Authorization")).toBe(false);
  });

  it.each([
    [400, "invalid"],
    [401, "unauthorized"],
    [404, "not-found"],
    [503, "unavailable"],
  ] as const)(
    "maps HTTP %s without returning the response body",
    async (status, expected) => {
      const fetchImplementation = vi.fn<typeof fetch>().mockResolvedValue(
        new Response(JSON.stringify({ private: "must not escape" }), {
          status,
          headers: { "Content-Type": "application/json" },
        }),
      );

      expect(
        await fetchAuthorizedAlert(
          alertRead.alert_id,
          new AbortController().signal,
          fetchImplementation,
        ),
      ).toEqual({ status: expected });
    },
  );

  it("classifies network errors without leaking error details", async () => {
    const fetchImplementation = vi
      .fn<typeof fetch>()
      .mockRejectedValue(new Error("private transport detail"));

    expect(
      await fetchAuthorizedAlert(
        alertRead.alert_id,
        new AbortController().signal,
        fetchImplementation,
      ),
    ).toEqual({ status: "unavailable" });
  });
});
