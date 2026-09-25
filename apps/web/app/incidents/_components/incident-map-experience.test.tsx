import {
  cleanup,
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PublicIncident } from "@/lib/api/generated";
import type { RealtimeConnector } from "./use-realtime-updates";
import { IncidentMapExperience } from "./incident-map-experience";

const idleRealtime: RealtimeConnector = async ({ signal }) => ({
  stream: (async function* () {
    await new Promise<void>((resolve) => {
      if (signal.aborted) resolve();
      else signal.addEventListener("abort", () => resolve(), { once: true });
    });
  })(),
});

vi.mock("./incident-map-stage", () => ({
  IncidentMapStage: ({
    onSelect,
    onUnavailable,
  }: {
    onSelect: (id: string) => void;
    onUnavailable: () => void;
  }) => (
    <div>
      <button onClick={() => onSelect("incident-1")} type="button">
        Select from map
      </button>
      <button onClick={onUnavailable} type="button">
        Simulate map failure
      </button>
    </div>
  ),
}));

const incident: PublicIncident = {
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
  last_signal_at: "2026-09-25T08:30:00Z",
  updated_at: "2026-09-25T08:30:00Z",
};

afterEach(() => cleanup());

describe("IncidentMapExperience", () => {
  it("keeps a keyboard-accessible details list and selection control alongside the map", async () => {
    render(
      <IncidentMapExperience
        connectRealtime={idleRealtime}
        loadIncidents={async () => [incident]}
        mapStyleUrl="https://tiles.example/style.json"
      />,
    );
    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Select from map" }));
    expect(
      screen
        .getByRole("button", { name: "Selected on map" })
        .getAttribute("aria-pressed"),
    ).toBe("true");
    expect(
      screen.getByRole("link", { name: /view details/i }).getAttribute("href"),
    ).toBe("/incidents/incident-1");
  });

  it("shows the map fallback when style is missing or map initialization fails", async () => {
    const { rerender } = render(
      <IncidentMapExperience
        connectRealtime={idleRealtime}
        loadIncidents={async () => [incident]}
        mapStyleUrl={null}
      />,
    );
    expect(await screen.findByText(/no map style is configured/i)).toBeTruthy();
    rerender(
      <IncidentMapExperience
        connectRealtime={idleRealtime}
        loadIncidents={async () => [incident]}
        mapStyleUrl="https://tiles.example/style.json"
      />,
    );
    await screen.findByRole("heading", { name: "Road Closure" });
    fireEvent.click(
      screen.getByRole("button", { name: "Simulate map failure" }),
    );
    await waitFor(() =>
      expect(screen.getByText(/map unavailable right now/i)).toBeTruthy(),
    );
  });

  it("offers retry after an API error", async () => {
    const loadIncidents = vi
      .fn<() => Promise<PublicIncident[]>>()
      .mockRejectedValueOnce(new Error())
      .mockResolvedValueOnce([incident]);
    render(
      <IncidentMapExperience
        connectRealtime={idleRealtime}
        loadIncidents={loadIncidents}
        mapStyleUrl={null}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
  });

  it("reconciles an incident event from REST and coalesces rapid bursts", async () => {
    let emit:
      | ((event: { event?: string; id?: string; data: unknown }) => void)
      | undefined;
    const updatedIncident: PublicIncident = {
      ...incident,
      status: "RESOLVING",
    };
    const loadIncidents = vi
      .fn<(signal: AbortSignal) => Promise<PublicIncident[]>>()
      .mockResolvedValueOnce([incident])
      .mockResolvedValue([updatedIncident]);
    const connectRealtime: RealtimeConnector = async (options) => {
      options.onConnection?.();
      emit = options.onSseEvent;
      return idleRealtime(options);
    };

    render(
      <IncidentMapExperience
        connectRealtime={connectRealtime}
        loadIncidents={loadIncidents}
        mapStyleUrl={null}
      />,
    );
    await screen.findByRole("heading", { name: "Road Closure" });
    expect(loadIncidents).toHaveBeenCalledOnce();

    act(() => {
      emit?.({
        event: "incident.status_changed.v1",
        id: "cursor-1",
        data: "ignored",
      });
      emit?.({
        event: "incident.confidence_changed.v1",
        id: "cursor-2",
        data: "malformed{",
      });
      emit?.({
        event: "incident.resolved.v1",
        id: "cursor-3",
        data: { status: "OPEN" },
      });
    });

    await waitFor(() => expect(loadIncidents).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("Resolving")).toBeTruthy();
  });

  it("keeps the map list visible and exposes explicit recovery after reconciliation failures", async () => {
    let emit:
      | ((event: { event?: string; id?: string; data: unknown }) => void)
      | undefined;
    const updatedIncident: PublicIncident = {
      ...incident,
      status: "RESOLVING",
    };
    const loadIncidents = vi
      .fn<(signal: AbortSignal) => Promise<PublicIncident[]>>()
      .mockResolvedValueOnce([incident])
      .mockRejectedValueOnce(new Error("temporary failure"))
      .mockRejectedValueOnce(new Error("still unavailable"))
      .mockResolvedValueOnce([updatedIncident]);
    const connectRealtime: RealtimeConnector = async (options) => {
      options.onConnection?.();
      emit = options.onSseEvent;
      return idleRealtime(options);
    };

    render(
      <IncidentMapExperience
        connectRealtime={connectRealtime}
        loadIncidents={loadIncidents}
        mapStyleUrl={null}
      />,
    );
    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
    expect(screen.getByText("Connected to realtime updates.")).toBeTruthy();

    act(() =>
      emit?.({ event: "incident.updated.v1", id: "cursor-1", data: {} }),
    );
    expect(
      await screen.findByText(/displayed information may be out of date/i),
    ).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Road Closure" })).toBeTruthy();
    expect(screen.getByText("Open")).toBeTruthy();
    expect(screen.queryByText("Connected to realtime updates.")).toBeNull();

    act(() => {
      for (let index = 0; index < 12; index += 1) {
        emit?.({
          event: "incident.updated.v1",
          id: `cursor-${index + 2}`,
          data: { status: "ignored" },
        });
      }
    });
    await waitFor(() => expect(loadIncidents).toHaveBeenCalledTimes(3));
    await new Promise((resolve) => setTimeout(resolve, 150));
    expect(loadIncidents).toHaveBeenCalledTimes(3);
    expect(screen.getByText("Open")).toBeTruthy();

    fireEvent.click(
      screen.getByRole("button", { name: "Retry incident refresh" }),
    );
    expect(await screen.findByText("Resolving")).toBeTruthy();
    expect(screen.getByText("Connected to realtime updates.")).toBeTruthy();
    expect(loadIncidents).toHaveBeenCalledTimes(4);
  });

  it("keeps fetched incidents visible when realtime authentication is unavailable", async () => {
    const connectRealtime: RealtimeConnector = async (options) => {
      options.onSseError?.(new Error("SSE failed: 401 Unauthorized"));
      return { stream: (async function* () {})() };
    };
    render(
      <IncidentMapExperience
        connectRealtime={connectRealtime}
        loadIncidents={async () => [incident]}
        mapStyleUrl={null}
      />,
    );

    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
    expect(
      await screen.findByText(/realtime updates unavailable/i),
    ).toBeTruthy();
  });

  it("does not render alert payload content and explains the missing alert read API", async () => {
    let emit:
      | ((event: { event?: string; id?: string; data: unknown }) => void)
      | undefined;
    const connectRealtime: RealtimeConnector = async (options) => {
      options.onConnection?.();
      emit = options.onSseEvent;
      return idleRealtime(options);
    };
    render(
      <IncidentMapExperience
        connectRealtime={connectRealtime}
        loadIncidents={async () => [incident]}
        mapStyleUrl={null}
      />,
    );
    await screen.findByRole("heading", { name: "Road Closure" });

    act(() =>
      emit?.({
        event: "alert.created.v1",
        id: "alert-cursor",
        data: { message: "private alert details must not be shown" },
      }),
    );

    expect(
      await screen.findByText(
        /alert details aren’t available in the public API yet/i,
      ),
    ).toBeTruthy();
    expect(screen.queryByText(/private alert details/i)).toBeNull();
  });

  it("does not commit an older list snapshot after a newer invalidation arrives", async () => {
    let emit:
      | ((event: { event?: string; id?: string; data: unknown }) => void)
      | undefined;
    const stale = { ...incident, event_type: "stale_snapshot" };
    const fresh = { ...incident, event_type: "fresh_snapshot" };
    const pending: Array<(value: PublicIncident[]) => void> = [];
    const loadIncidents = vi.fn(
      () => new Promise<PublicIncident[]>((resolve) => pending.push(resolve)),
    );
    const connectRealtime: RealtimeConnector = async (options) => {
      emit = options.onSseEvent;
      return idleRealtime(options);
    };
    render(
      <IncidentMapExperience
        connectRealtime={connectRealtime}
        loadIncidents={loadIncidents}
        mapStyleUrl={null}
      />,
    );
    await waitFor(() => expect(pending).toHaveLength(1));
    act(() => emit?.({ event: "incident.created.v1", id: "cursor", data: {} }));
    await new Promise((resolve) => setTimeout(resolve, 120));

    act(() => pending[0]([stale]));
    await waitFor(() => expect(pending).toHaveLength(2));
    expect(
      screen.queryByRole("heading", { name: "Stale Snapshot" }),
    ).toBeNull();

    act(() => pending[1]([fresh]));
    expect(
      await screen.findByRole("heading", { name: "Fresh Snapshot" }),
    ).toBeTruthy();
  });
});
