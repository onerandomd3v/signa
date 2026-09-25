import { describe, expect, it, vi } from "vitest";
import { fetchCurrentSession } from "./session";

describe("fetchCurrentSession", () => {
  it("uses the generated cookie-auth operation with browser credentials", async () => {
    const fetcher = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify({ user_id: "00000000-0000-0000-0000-000000000001" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const result = await fetchCurrentSession(fetcher);

    expect(result.data?.user_id).toBe("00000000-0000-0000-0000-000000000001");
    const [request] = fetcher.mock.calls[0] as [Request];
    expect(request.credentials).toBe("include");
    expect(request.url).toBe("http://localhost:8080/auth/session");
  });
});
