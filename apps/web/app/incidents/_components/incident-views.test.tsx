import { cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  IncidentDetail,
  IncidentFeed,
  type IncidentView,
} from "./incident-views";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

// Synthetic presentation data for component tests only; never used by routes.
const syntheticIncident: IncidentView = {
  id: "synthetic-test-incident",
  eventType: "road_closure",
  confidenceState: "emerging",
  severity: "moderate",
  status: "active",
  approximateArea: "Central district",
  lastSignalAt: "2026-09-24T09:59:00.000Z",
  updates: [
    {
      id: "synthetic-update-1",
      occurredAt: "2026-09-24T09:55:00.000Z",
      confidenceState: "EMERGING",
    },
  ],
};

describe("IncidentFeed", () => {
  it("clearly says the live incident feed is unavailable rather than showing samples", () => {
    render(<IncidentFeed state={{ status: "unavailable" }} />);

    expect(
      screen.getByRole("heading", {
        name: "Incident updates aren’t available yet",
      }),
    ).toBeTruthy();
    expect(
      screen.getByText(/no sample incidents are shown as live reports/i),
    ).toBeTruthy();
    expect(screen.queryByRole("list", { name: "Incidents" })).toBeNull();
  });

  it("provides accessible loading and error states", () => {
    const { rerender } = render(<IncidentFeed state={{ status: "loading" }} />);

    expect(screen.getByRole("status").getAttribute("aria-label")).toBe(
      "Loading incidents",
    );

    rerender(<IncidentFeed state={{ status: "error" }} />);
    expect(screen.getByRole("alert").textContent).toContain(
      "Couldn’t load incidents",
    );
  });

  it("renders the public incident projection without reporter or coordinate details", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-24T10:00:00.000Z"));
    render(
      <IncidentFeed
        state={{ status: "ready", incidents: [syntheticIncident] }}
      />,
    );

    const card = screen.getByRole("article");
    expect(
      within(card).getByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
    expect(within(card).getByText("Emerging")).toBeTruthy();
    expect(within(card).getByText("Moderate")).toBeTruthy();
    expect(within(card).getByText("Central District")).toBeTruthy();
    expect(within(card).getByText("1 minute ago")).toBeTruthy();
    expect(
      within(card)
        .getByRole("link", { name: /view incident details/i })
        .getAttribute("href"),
    ).toBe("/incidents/synthetic-test-incident");
    expect(card.textContent).not.toMatch(/reporter|latitude|longitude/i);
  });

  it("renders an honest, safety-aware empty state", () => {
    render(<IncidentFeed state={{ status: "ready", incidents: [] }} />);
    expect(
      screen.getByText(/this does not mean an area is safe/i),
    ).toBeTruthy();
  });
});

describe("IncidentDetail", () => {
  it("renders incident updates and distinguishes confidence from severity", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-24T10:00:00.000Z"));
    render(
      <IncidentDetail
        state={{ status: "ready", incident: syntheticIncident }}
      />,
    );

    expect(screen.getByRole("heading", { name: "Road Closure" })).toBeTruthy();
    expect(screen.getByText("Confidence: Emerging")).toBeTruthy();
    expect(
      screen.getByText(/confidence can change as information arrives/i),
    ).toBeTruthy();
    expect(screen.getByText("1 minute ago")).toBeTruthy();
  });

  it.each([
    ["RESOLVED", /marked resolved by the incident system/i],
    ["EXPIRED", /may no longer reflect current conditions/i],
  ])(
    "shows the backend terminal state %s without claiming certainty",
    (status, message) => {
      render(
        <IncidentDetail
          state={{
            status: "ready",
            incident: { ...syntheticIncident, status },
          }}
        />,
      );

      expect(
        screen.getByText(status === "RESOLVED" ? "Resolved" : "Expired"),
      ).toBeTruthy();
      expect(screen.getByText(message)).toBeTruthy();
    },
  );

  it("has distinct loading, error, not-found, and unavailable states", () => {
    const { rerender } = render(
      <IncidentDetail state={{ status: "loading" }} />,
    );
    expect(screen.getByRole("status").getAttribute("aria-label")).toBe(
      "Loading incident details",
    );

    rerender(<IncidentDetail state={{ status: "error" }} />);
    expect(screen.getByRole("alert").textContent).toContain(
      "Couldn’t load incident details",
    );
    rerender(<IncidentDetail state={{ status: "not-found" }} />);
    expect(
      screen.getByRole("heading", { name: "Incident not found" }),
    ).toBeTruthy();
    rerender(<IncidentDetail state={{ status: "unavailable" }} />);
    expect(
      screen.getByRole("heading", {
        name: "Incident details aren’t available yet",
      }),
    ).toBeTruthy();
  });
});
