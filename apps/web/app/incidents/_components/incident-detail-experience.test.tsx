import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PublicIncident } from "@/lib/api/generated";
import { fetchPublicIncident } from "@/lib/api/incidents";
import { IncidentDetailExperience } from "./incident-detail-experience";

vi.mock("@/lib/api/incidents", () => ({
  fetchPublicIncident: vi.fn(),
}));

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
    render(<IncidentDetailExperience incidentId="incident-1" />);

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
    render(<IncidentDetailExperience incidentId="incident-1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
  });

  it("shows not-found for an inactive incident", async () => {
    vi.mocked(fetchPublicIncident).mockResolvedValueOnce({
      status: "not-found",
    });
    render(<IncidentDetailExperience incidentId="inactive" />);
    expect(
      await screen.findByRole("heading", { name: "Incident not found" }),
    ).toBeTruthy();
  });
});
