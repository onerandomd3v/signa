import {
  cleanup,
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AlertRead, PublicIncident } from "@/lib/api/generated";
import type { AlertReadResult } from "@/lib/api/alerts";
import type { AlertReader } from "./use-alert-reconciliation";
import type { RealtimeConnector } from "./use-realtime-updates";
import type { RouteLineGeometry } from "./route-geometry";
import type { RouteRelevanceResult } from "@/lib/api/route-relevance";
import type { IncidentFeatureCollection } from "./incident-geojson";
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
    routeGeometry,
    featureCollection,
  }: {
    onSelect: (id: string) => void;
    onUnavailable: () => void;
    routeGeometry: RouteLineGeometry | null;
    featureCollection: IncidentFeatureCollection;
  }) => (
    <div
      data-testid="map-stage"
      data-route-coordinates={JSON.stringify(routeGeometry?.coordinates ?? [])}
      data-incident-count={featureCollection.features.length}
    >
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

const authorizedAlert: AlertRead = {
  alert_id: "550e8400-e29b-41d4-a716-446655440000",
  incident_id: "550e8400-e29b-41d4-a716-446655440001",
  alert_type: "IMMEDIATE",
  confidence_snapshot: "CORROBORATED",
  severity_snapshot: "HIGH",
  status_snapshot: "OPEN",
  priority_snapshot: "P1",
  freshness_snapshot: "FRESH",
  message: "Authorized alert snapshot message.",
  as_of: "2026-09-26T10:00:00Z",
  created_at: "2026-09-26T10:00:00Z",
  supersedes_alert_id: null,
};
const pendingAuthorizedAlert: AlertRead = {
  ...authorizedAlert,
  alert_id: "550e8400-e29b-41d4-a716-446655440010",
  message: "Late protected snapshot must not return.",
};

const emptyNotRelevantRoute: RouteRelevanceResult = {
  status: "success",
  response: {
    classification: "NOT_RELEVANT",
    route_geometry: {
      type: "LineString",
      coordinates: [
        [3.3792, 6.5244],
        [3.3947, 6.4541],
      ],
    },
    incidents: [],
  },
};

function submitRoute() {
  const origin = screen.getByRole("group", { name: "Origin" });
  const destination = screen.getByRole("group", { name: "Destination" });
  fireEvent.change(within(origin).getByLabelText(/Latitude/), {
    target: { value: "6.5244" },
  });
  fireEvent.change(within(origin).getByLabelText(/Longitude/), {
    target: { value: "3.3792" },
  });
  fireEvent.change(within(destination).getByLabelText(/Latitude/), {
    target: { value: "6.4541" },
  });
  fireEvent.change(within(destination).getByLabelText(/Longitude/), {
    target: { value: "3.3947" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Check route" }));
}

afterEach(() => cleanup());

describe("IncidentMapExperience", () => {
  it("shows an evaluated NOT_RELEVANT route on the map when incident results are empty", async () => {
    const evaluateRoute = vi.fn(async () => emptyNotRelevantRoute);
    render(
      <IncidentMapExperience
        evaluateRoute={evaluateRoute}
        loadIncidents={async () => []}
        mapStyleUrl="https://tiles.example/style.json"
      />,
    );

    submitRoute();
    expect(
      await screen.findByRole("heading", {
        name: "No evaluated incident was route-relevant",
      }),
    ).toBeTruthy();
    expect(
      screen.getByRole("heading", { name: "No active incidents" }),
    ).toBeTruthy();
    expect(
      screen.getByTestId("map-stage").getAttribute("data-route-coordinates"),
    ).toBe(
      JSON.stringify([
        [3.3792, 6.5244],
        [3.3947, 6.4541],
      ]),
    );
    expect(
      screen.getByTestId("map-stage").getAttribute("data-incident-count"),
    ).toBe("0");
  });

  it("keeps an accessible route text alternative when the map is unavailable", async () => {
    render(
      <IncidentMapExperience
        evaluateRoute={async () => emptyNotRelevantRoute}
        loadIncidents={async () => []}
        mapStyleUrl={null}
      />,
    );
    submitRoute();
    expect(
      await screen.findByText(
        /route line from approximately 6\.5244, 3\.3792/i,
      ),
    ).toBeTruthy();
    expect(screen.getByText(/no map style is configured/i)).toBeTruthy();
    expect(
      screen.getByRole("heading", { name: "No active incidents" }),
    ).toBeTruthy();
  });

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
    expect(screen.getByRole("link", { name: /view details/i })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Check a route" })).toBeTruthy();
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

  it("loads alert snapshots from the API, not the raw SSE event, and retries failures", async () => {
    let reconnect: (() => void) | undefined;
    let emit:
      | ((event: { event?: string; id?: string; data: unknown }) => void)
      | undefined;
    const connectRealtime: RealtimeConnector = async (options) => {
      options.onConnection?.();
      reconnect = options.onConnection;
      emit = options.onSseEvent;
      return idleRealtime(options);
    };
    const readAlert: AlertReader = vi
      .fn()
      .mockResolvedValueOnce({ status: "unavailable" })
      .mockResolvedValueOnce({ status: "authorized", alert: authorizedAlert })
      .mockResolvedValueOnce({ status: "authorized", alert: authorizedAlert });
    render(
      <IncidentMapExperience
        connectRealtime={connectRealtime}
        loadIncidents={async () => [incident]}
        mapStyleUrl={null}
        readAlert={readAlert}
      />,
    );
    await screen.findByRole("heading", { name: "Road Closure" });

    act(() => {
      emit?.({
        event: "alert.created.v1",
        id: "opaque-alert-cursor",
        data: {
          alert_id: authorizedAlert.alert_id,
          incident_id: authorizedAlert.incident_id,
          message: "untrusted event message must not render",
          severity: "untrusted event severity",
          priority: "P3",
        },
      });
      for (let index = 0; index < 5; index += 1) {
        emit?.({
          event: "alert.created.v1",
          id: `replay-${index}`,
          data: {
            alert_id: authorizedAlert.alert_id,
            incident_id: authorizedAlert.incident_id,
          },
        });
      }
    });

    expect(
      await screen.findByText(/alert details are unavailable/i),
    ).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Road Closure" })).toBeTruthy();
    expect(readAlert).toHaveBeenCalledOnce();
    expect(screen.queryByText(/untrusted event message/i)).toBeNull();

    fireEvent.click(
      screen.getByRole("button", { name: "Retry alert details" }),
    );
    expect(
      await screen.findByText("Authorized alert snapshot message."),
    ).toBeTruthy();
    expect(screen.getByText("P1")).toBeTruthy();
    expect(screen.getByText("As of")).toBeTruthy();
    expect(
      screen.getByText(/does not confirm current incident conditions/i),
    ).toBeTruthy();
    expect(readAlert).toHaveBeenCalledTimes(2);

    act(() => reconnect?.());
    await waitFor(() => expect(readAlert).toHaveBeenCalledTimes(3));
    expect(
      screen.getAllByText("Authorized alert snapshot message."),
    ).toHaveLength(1);
  });

  it("keeps public incidents visible and removes alert data after a 401", async () => {
    let emit:
      | ((event: { event?: string; id?: string; data: unknown }) => void)
      | undefined;
    let reconnect: (() => void) | undefined;
    const connectRealtime: RealtimeConnector = async (options) => {
      options.onConnection?.();
      reconnect = options.onConnection;
      emit = options.onSseEvent;
      return idleRealtime(options);
    };
    const readAlert: AlertReader = vi
      .fn()
      .mockResolvedValueOnce({ status: "authorized", alert: authorizedAlert })
      .mockResolvedValueOnce({ status: "unauthorized" });
    render(
      <IncidentMapExperience
        connectRealtime={connectRealtime}
        loadIncidents={async () => [incident]}
        mapStyleUrl={null}
        readAlert={readAlert}
      />,
    );

    await screen.findByRole("heading", { name: "Road Closure" });
    act(() =>
      emit?.({
        event: "alert.created.v1",
        id: "alert-auth-cursor",
        data: {
          alert_id: authorizedAlert.alert_id,
          incident_id: authorizedAlert.incident_id,
        },
      }),
    );
    expect(
      await screen.findByText("Authorized alert snapshot message."),
    ).toBeTruthy();

    act(() => reconnect?.());
    await waitFor(() => expect(readAlert).toHaveBeenCalledTimes(2));
    expect(screen.getByRole("heading", { name: "Road Closure" })).toBeTruthy();
    expect(screen.queryByText("Authorized alert snapshot message.")).toBeNull();
    expect(screen.queryByText(/alert details are unavailable/i)).toBeNull();
  });

  it("clears authorized snapshots on SSE 401 and ignores pending protected reads", async () => {
    let emit:
      | ((event: { event?: string; id?: string; data: unknown }) => void)
      | undefined;
    let loseAuthentication: (() => void) | undefined;
    let pendingSignal: AbortSignal | undefined;
    let resolvePendingRead: ((result: AlertReadResult) => void) | undefined;
    const pendingRead = new Promise<AlertReadResult>((resolve) => {
      resolvePendingRead = resolve;
    });
    const connectRealtime: RealtimeConnector = async (options) => {
      options.onConnection?.();
      emit = options.onSseEvent;
      loseAuthentication = () =>
        options.onSseError?.(new Error("SSE failed: 401 Unauthorized"));
      return idleRealtime(options);
    };
    const readAlert: AlertReader = vi
      .fn()
      .mockResolvedValueOnce({ status: "authorized", alert: authorizedAlert })
      .mockImplementationOnce((_alertId, signal) => {
        pendingSignal = signal;
        return pendingRead;
      });
    render(
      <IncidentMapExperience
        connectRealtime={connectRealtime}
        loadIncidents={async () => [incident]}
        mapStyleUrl={null}
        readAlert={readAlert}
      />,
    );

    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
    act(() =>
      emit?.({
        event: "alert.created.v1",
        id: "map-auth-first-alert",
        data: {
          alert_id: authorizedAlert.alert_id,
          incident_id: authorizedAlert.incident_id,
        },
      }),
    );
    expect(
      await screen.findByText("Authorized alert snapshot message."),
    ).toBeTruthy();

    act(() =>
      emit?.({
        event: "alert.created.v1",
        id: "map-auth-pending-alert",
        data: {
          alert_id: pendingAuthorizedAlert.alert_id,
          incident_id: pendingAuthorizedAlert.incident_id,
        },
      }),
    );
    await waitFor(() => expect(readAlert).toHaveBeenCalledTimes(2));
    expect(pendingSignal?.aborted).toBe(false);

    act(() => loseAuthentication?.());
    await waitFor(() => expect(pendingSignal?.aborted).toBe(true));
    expect(screen.queryByText("Authorized alert snapshot message.")).toBeNull();
    expect(screen.getByRole("heading", { name: "Road Closure" })).toBeTruthy();

    await act(async () => {
      resolvePendingRead?.({
        status: "authorized",
        alert: pendingAuthorizedAlert,
      });
      await pendingRead;
    });
    expect(
      screen.queryByText("Late protected snapshot must not return."),
    ).toBeNull();
    expect(screen.queryByText("Authorized alert snapshot message.")).toBeNull();
  });

  it("commits serial snapshots during invalidations and ends with the newest result", async () => {
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
      await screen.findByRole("heading", { name: "Stale Snapshot" }),
    ).toBeTruthy();

    act(() => pending[1]([fresh]));
    expect(
      await screen.findByRole("heading", { name: "Fresh Snapshot" }),
    ).toBeTruthy();
    expect(
      screen.queryByRole("heading", { name: "Stale Snapshot" }),
    ).toBeNull();
  });

  it("reports a failed read while another realtime invalidation is queued", async () => {
    let emit:
      | ((event: { event?: string; id?: string; data: unknown }) => void)
      | undefined;
    const updatedIncident: PublicIncident = {
      ...incident,
      status: "RESOLVING",
    };
    const pending: Array<{
      resolve: (value: PublicIncident[]) => void;
      reject: (reason?: unknown) => void;
    }> = [];
    const loadIncidents = vi.fn(
      () =>
        new Promise<PublicIncident[]>((resolve, reject) => {
          pending.push({ resolve, reject });
        }),
    );
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
    await waitFor(() => expect(pending).toHaveLength(1));
    act(() => pending[0].resolve([incident]));
    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();

    act(() =>
      emit?.({ event: "incident.updated.v1", id: "cursor-1", data: {} }),
    );
    await waitFor(() => expect(pending).toHaveLength(2));
    act(() =>
      emit?.({ event: "incident.updated.v1", id: "cursor-2", data: {} }),
    );
    await new Promise((resolve) => setTimeout(resolve, 120));

    act(() => pending[1].reject(new Error("temporary failure")));
    await waitFor(() => expect(pending).toHaveLength(3));
    expect(
      await screen.findByText(/displayed information may be out of date/i),
    ).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Road Closure" })).toBeTruthy();
    expect(screen.getByText("Open")).toBeTruthy();

    act(() => pending[2].resolve([updatedIncident]));
    expect(await screen.findByText("Resolving")).toBeTruthy();
    expect(
      screen.queryByText(/displayed information may be out of date/i),
    ).toBeNull();
  });
});
