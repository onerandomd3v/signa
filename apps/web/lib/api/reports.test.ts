import { afterEach, describe, expect, it, vi } from "vitest";
import { getApiBaseUrl, submitReport } from "./reports";

describe("report API client", () => {
  afterEach(() => vi.unstubAllEnvs());

  it("defaults to the local Go API in development", () => {
    vi.stubEnv("NEXT_PUBLIC_API_BASE_URL", "");
    vi.stubEnv("NODE_ENV", "development");
    expect(getApiBaseUrl()).toBe("http://localhost:8080");
  });

  it("posts through the generated client with the idempotency key", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      report_id: "report-123",
      status: "accepted",
      submitted_at: "2026-09-23T12:00:00Z",
    }), { status: 202, headers: { "Content-Type": "application/json" } }));
    const result = await submitReport({ raw_text: "Smoke near the station." }, "secure-key", fetcher);

    expect(result.ok).toBe(true);
    expect(fetcher).toHaveBeenCalledOnce();
    const [url, init] = fetcher.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/reports");
    expect(init.method).toBe("POST");
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe("secure-key");
    expect(JSON.parse(String(init.body))).toEqual({ raw_text: "Smoke near the station." });
  });

  it("returns typed HTTP failures and Retry-After", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: "rate_limited", message: "Try later" },
    }), { status: 429, headers: { "Content-Type": "application/json", "Retry-After": "12" } }));
    const result = await submitReport({ raw_text: "Smoke." }, "secure-key", fetcher);
    expect(result).toMatchObject({ ok: false, status: 429, retryAfter: "12" });
  });
});
