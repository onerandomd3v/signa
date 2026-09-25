import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PublicIncident } from "@/lib/api/generated";
import { IncidentMapExperience } from "./incident-map-experience";

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
        loadIncidents={async () => [incident]}
        mapStyleUrl={null}
      />,
    );
    expect(await screen.findByText(/no map style is configured/i)).toBeTruthy();
    rerender(
      <IncidentMapExperience
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
        loadIncidents={loadIncidents}
        mapStyleUrl={null}
      />,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
  });
});
