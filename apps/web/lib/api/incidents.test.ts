import { describe, expect, it, vi } from "vitest";
import {
  fetchPublicIncident,
  fetchPublicIncidents,
  getPublicMapStyleUrl,
} from "./incidents";

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

  it("passes cancellation through to the generated list request", async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      new Response("[]", {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const controller = new AbortController();

    await fetchPublicIncidents(fetchMock, controller.signal);

    expect(fetchMock.mock.calls[0][0]).toMatchObject({
      signal: controller.signal,
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

describe("fetchPublicIncident", () => {
  it("uses the generated detail endpoint and returns its public snapshot", async () => {
    const publicIncident = {
      id: "incident-1",
      event_type: "road_closure",
      status: "OPEN",
      confidence_state: "EMERGING",
      severity: "MODERATE",
      public_geometry: {
        type: "Polygon",
        coordinates: [
          [
            [3, 6],
            [4, 6],
            [4, 7],
            [3, 6],
          ],
        ],
      },
      started_at: null,
      last_signal_at: null,
      updated_at: "2026-09-25T08:30:00Z",
    };
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify(publicIncident), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    expect(await fetchPublicIncident("incident-1", fetchMock)).toEqual({
      status: "ready",
      incident: publicIncident,
    });
    expect(fetchMock.mock.calls[0][0]).toMatchObject({
      url: "http://localhost:8080/incidents/incident-1",
    });
  });

  it("distinguishes a missing incident from a request failure", async () => {
    const notFoundFetch = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response("{}", { status: 404 }));
    const failedFetch = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response("{}", { status: 500 }));
    await expect(fetchPublicIncident("gone", notFoundFetch)).resolves.toEqual({
      status: "not-found",
    });
    await expect(fetchPublicIncident("broken", failedFetch)).resolves.toEqual({
      status: "error",
    });
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
