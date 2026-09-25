import { describe, expect, it, vi } from "vitest";
import { fetchPublicIncidents, getPublicMapStyleUrl } from "./incidents";

describe("fetchPublicIncidents", () => {
  it("uses the generated public list endpoint", async () => {
    const response = new Response("[]", {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(response);
    expect(await fetchPublicIncidents(fetchMock)).toEqual([]);
    expect(fetchMock.mock.calls[0][0]).toMatchObject({
      url: "http://localhost:8080/incidents",
    });
  });

  it("surfaces API failure instead of treating it as an empty map", async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response("{}", { status: 500 }));
    await expect(fetchPublicIncidents(fetchMock)).rejects.toThrow(
      "Incident request failed (500)",
    );
  });
});

describe("getPublicMapStyleUrl", () => {
  it("rejects credentials and unsupported schemes", () => {
    vi.stubEnv("NEXT_PUBLIC_MAP_STYLE_URL", "javascript:alert(1)");
    expect(getPublicMapStyleUrl()).toBeNull();
    vi.stubEnv(
      "NEXT_PUBLIC_MAP_STYLE_URL",
      "https://user:pass@example.com/style.json",
    );
    expect(getPublicMapStyleUrl()).toBeNull();
  });
});
