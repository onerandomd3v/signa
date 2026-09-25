import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { IncidentMap } from "./incident-map";

const mockMap = vi.hoisted(() => ({
  shouldThrow: true,
  constructor: vi.fn(),
  instance: {
    addControl: vi.fn(),
    on: vi.fn(),
    remove: vi.fn(),
    isStyleLoaded: vi.fn(() => false),
    getSource: vi.fn(),
  },
}));

vi.mock("maplibre-gl", () => ({
  Map: class {
    constructor(options: unknown) {
      mockMap.constructor(options);
      if (mockMap.shouldThrow) throw new Error("WebGL unavailable");
      return mockMap.instance;
    }
  },
  NavigationControl: class {},
  setWorkerUrl: vi.fn(),
}));

afterEach(() => {
  cleanup();
  mockMap.shouldThrow = true;
  vi.clearAllMocks();
});

describe("IncidentMap", () => {
  it("reports a map constructor failure so the text view can remain available", async () => {
    const onUnavailable = vi.fn();
    render(
      <IncidentMap
        featureCollection={{ type: "FeatureCollection", features: [] }}
        onSelect={vi.fn()}
        onUnavailable={onUnavailable}
        selectedId={null}
        styleUrl="https://tiles.example/style.json"
      />,
    );
    await waitFor(() => expect(onUnavailable).toHaveBeenCalledOnce());
    expect(mockMap.constructor).toHaveBeenCalledOnce();
  });
});
