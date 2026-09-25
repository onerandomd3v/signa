import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PublicIncident } from "@/lib/api/generated";
import { fetchPublicIncident } from "@/lib/api/incidents";
import type { RealtimeConnector } from "./use-realtime-updates";
import { IncidentDetailExperience } from "./incident-detail-experience";

vi.mock("@/lib/api/incidents", () => ({
  fetchPublicIncident: vi.fn(),
}));

const idleRealtime: RealtimeConnector = async ({ signal }) => ({
  stream: (async function* () {
    await new Promise<void>((resolve) => {
      if (signal.aborted) resolve();
      else signal.addEventListener("abort", () => resolve(), { once: true });
    });
  })(),
});

const publicIncident: PublicIncident = {
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
  started_at: "2026-09-25T08:00:00Z",
  last_signal_at: "2026-09-25T08:30:00Z",
  updated_at: "2026-09-25T08:30:00Z",
};

afterEach(() => cleanup());

describe("IncidentDetailExperience", () => {
  it("renders only fields supplied by the public incident endpoint", async () => {
    vi.mocked(fetchPublicIncident).mockResolvedValue({
      status: "ready",
      incident: publicIncident,
    });
    render(
      <IncidentDetailExperience
        connectRealtime={idleRealtime}
        incidentId="incident-1"
      />,
    );

    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
    expect(screen.getByText("Open")).toBeTruthy();
    expect(screen.getByText("Emerging")).toBeTruthy();
    expect(screen.getByText("Moderate")).toBeTruthy();
    expect(screen.getByText(/update history isn’t included/i)).toBeTruthy();
    expect(
      screen.queryByText(/central district|reporter|latitude|longitude/i),
    ).toBeNull();
  });

  it("offers retry for request failures", async () => {
    vi.mocked(fetchPublicIncident)
      .mockResolvedValueOnce({ status: "error" })
      .mockResolvedValueOnce({ status: "ready", incident: publicIncident });
    render(
      <IncidentDetailExperience
        connectRealtime={idleRealtime}
        incidentId="incident-1"
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
  });

  it("shows not-found for an inactive incident", async () => {
    vi.mocked(fetchPublicIncident).mockResolvedValueOnce({
      status: "not-found",
    });
    render(
      <IncidentDetailExperience
        connectRealtime={idleRealtime}
        incidentId="inactive"
      />,
    );
    expect(
      await screen.findByRole("heading", { name: "Incident not found" }),
    ).toBeTruthy();
  });

  it("refetches an open incident after an event and ignores event payload fields", async () => {
    let emit:
      | ((event: { event?: string; id?: string; data: unknown }) => void)
      | undefined;
    vi.mocked(fetchPublicIncident)
      .mockResolvedValueOnce({ status: "ready", incident: publicIncident })
      .mockResolvedValueOnce({
        status: "ready",
        incident: { ...publicIncident, confidence_state: "CORROBORATED" },
      });
    const connectRealtime: RealtimeConnector = async (options) => {
      options.onConnection?.();
      emit = options.onSseEvent;
      return idleRealtime(options);
    };
    render(
      <IncidentDetailExperience
        connectRealtime={connectRealtime}
        incidentId="incident-1"
      />,
    );

    expect(await screen.findByText("Emerging")).toBeTruthy();
    act(() =>
      emit?.({
        event: "incident.confidence_changed.v1",
        id: "opaque-cursor",
        data: { confidence_state: "NOT AUTHORITATIVE" },
      }),
    );
    await waitFor(() => expect(fetchPublicIncident).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("Corroborated")).toBeTruthy();
    expect(screen.queryByText("Not Authoritative")).toBeNull();
    expect(screen.getByRole("status").getAttribute("aria-live")).toBe("polite");
  });

  it("preserves detail through failed reconciliations and recovers on explicit retry", async () => {
    let emit:
      | ((event: { event?: string; id?: string; data: unknown }) => void)
      | undefined;
    vi.mocked(fetchPublicIncident)
      .mockResolvedValueOnce({ status: "ready", incident: publicIncident })
      .mockRejectedValueOnce(new Error("temporary failure"))
      .mockResolvedValueOnce({ status: "error" })
      .mockResolvedValueOnce({
        status: "ready",
        incident: { ...publicIncident, confidence_state: "CORROBORATED" },
      });
    const connectRealtime: RealtimeConnector = async (options) => {
      options.onConnection?.();
      emit = options.onSseEvent;
      return idleRealtime(options);
    };
    render(
      <IncidentDetailExperience
        connectRealtime={connectRealtime}
        incidentId="incident-1"
      />,
    );

    expect(await screen.findByText("Emerging")).toBeTruthy();
    expect(screen.getByText("Connected to realtime updates.")).toBeTruthy();
    act(() =>
      emit?.({ event: "incident.updated.v1", id: "cursor-1", data: {} }),
    );
    expect(
      await screen.findByText(/displayed information may be out of date/i),
    ).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Road Closure" })).toBeTruthy();
    expect(screen.getByText("Emerging")).toBeTruthy();
    expect(screen.queryByText("Connected to realtime updates.")).toBeNull();

    act(() => {
      for (let index = 0; index < 12; index += 1) {
        emit?.({
          event: "incident.updated.v1",
          id: `cursor-${index + 2}`,
          data: { confidence_state: "ignored" },
        });
      }
    });
    await waitFor(() => expect(fetchPublicIncident).toHaveBeenCalledTimes(3));
    await new Promise((resolve) => setTimeout(resolve, 150));
    expect(fetchPublicIncident).toHaveBeenCalledTimes(3);
    expect(screen.getByText("Emerging")).toBeTruthy();

    fireEvent.click(
      screen.getByRole("button", { name: "Retry incident refresh" }),
    );
    expect(await screen.findByText("Corroborated")).toBeTruthy();
    expect(screen.getByText("Connected to realtime updates.")).toBeTruthy();
    expect(fetchPublicIncident).toHaveBeenCalledTimes(4);
  });

  it("preserves the existing incident when the stream returns 503", async () => {
    vi.mocked(fetchPublicIncident).mockResolvedValue({
      status: "ready",
      incident: publicIncident,
    });
    const connectRealtime: RealtimeConnector = async (options) => {
      options.onSseError?.(new Error("SSE failed: 503 Service Unavailable"));
      return { stream: (async function* () {})() };
    };
    render(
      <IncidentDetailExperience
        connectRealtime={connectRealtime}
        incidentId="incident-1"
      />,
    );

    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
    expect(
      await screen.findByText(/realtime updates unavailable/i),
    ).toBeTruthy();
    expect(screen.getByText("Emerging")).toBeTruthy();
  });
});
