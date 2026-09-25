import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PublicIncident } from "@/lib/api/generated";
import { IncidentMap } from "./incident-map";
import {
  toIncidentFeatureCollection,
  type IncidentFeatureCollection,
} from "./incident-geojson";

const mockMap = vi.hoisted(() => {
  const source = { setData: vi.fn() };
  const handlers: Record<string, (...args: never[]) => void> = {};
  const instance = {
    addControl: vi.fn(),
    on: vi.fn((event: string, layerOrHandler: unknown, handler?: unknown) => {
      const key =
        typeof layerOrHandler === "string"
          ? `${event}:${layerOrHandler}`
          : event;
      handlers[key] = (
        typeof layerOrHandler === "function" ? layerOrHandler : handler
      ) as (...args: never[]) => void;
    }),
    addSource: vi.fn(),
    addLayer: vi.fn(),
    fitBounds: vi.fn(),
    setPaintProperty: vi.fn(),
    remove: vi.fn(),
    isStyleLoaded: vi.fn(() => false),
    getSource: vi.fn(() => source),
    getCanvas: vi.fn(() => ({ style: { cursor: "" } })),
  };
  return {
    shouldThrow: false,
    constructor: vi.fn(),
    source,
    handlers,
    instance,
  };
});

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

const publicFeatureCollection: IncidentFeatureCollection = {
  type: "FeatureCollection",
  features: [
    {
      type: "Feature",
      id: "incident-1",
      geometry: {
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
      properties: {
        id: "incident-1",
        event_type: "road_closure",
        status: "OPEN",
        confidence_state: "EMERGING",
        severity: "MODERATE",
        started_at: null,
        last_signal_at: null,
        updated_at: "2026-09-25T08:30:00Z",
      },
    },
  ],
};

function fire(event: string, ...args: unknown[]) {
  const handler = mockMap.handlers[event];
  if (handler) handler(...(args as never[]));
}

function makeProps(
  overrides: Partial<{
    featureCollection: IncidentFeatureCollection;
    selectedId: string | null;
    onSelect: (id: string) => void;
    onUnavailable: () => void;
  }> = {},
) {
  return {
    featureCollection: publicFeatureCollection,
    onSelect: vi.fn(),
    onUnavailable: vi.fn(),
    selectedId: null,
    styleUrl: "https://tiles.example/style.json",
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  mockMap.shouldThrow = false;
  mockMap.instance.isStyleLoaded.mockReturnValue(false);
  for (const key of Object.keys(mockMap.handlers)) {
    delete mockMap.handlers[key];
  }
  vi.clearAllMocks();
});

describe("IncidentMap", () => {
  it("loads the style, adds public GeoJSON layers, fits bounds, selects features, highlights selection, and cleans up", async () => {
    const onSelect = vi.fn();
    const props = makeProps({ onSelect });
    const { rerender, unmount } = render(<IncidentMap {...props} />);
    await waitFor(() => expect(mockMap.handlers["style.load"]).toBeTruthy());

    act(() => fire("style.load"));

    expect(mockMap.instance.addSource).toHaveBeenCalledWith(
      "public-incidents",
      expect.objectContaining({
        type: "geojson",
        data: publicFeatureCollection,
      }),
    );
    expect(mockMap.instance.addLayer).toHaveBeenCalledWith(
      expect.objectContaining({ id: "public-incident-areas", type: "fill" }),
    );
    expect(mockMap.instance.addLayer).toHaveBeenCalledWith(
      expect.objectContaining({ id: "public-incident-outlines", type: "line" }),
    );
    expect(mockMap.instance.fitBounds).toHaveBeenCalledWith(
      [
        [3, 6],
        [4, 7],
      ],
      expect.objectContaining({ padding: 48, maxZoom: 11 }),
    );

    act(() =>
      fire("click:public-incident-areas", {
        features: [{ properties: { id: "incident-1" } }],
      }),
    );
    expect(onSelect).toHaveBeenCalledWith("incident-1");

    mockMap.instance.isStyleLoaded.mockReturnValue(true);
    rerender(<IncidentMap {...props} selectedId="incident-1" />);
    expect(mockMap.instance.setPaintProperty).toHaveBeenLastCalledWith(
      "public-incident-areas",
      "fill-opacity",
      expect.arrayContaining(["case", ["==", ["get", "id"], "incident-1"]]),
    );

    unmount();
    expect(mockMap.instance.remove).toHaveBeenCalledOnce();
  });

  it("does not crash or fit bounds when geometry is missing or malformed", async () => {
    const onUnavailable = vi.fn();
    const malformed = {
      type: "Polygon",
      coordinates: [
        [
          [181, 6],
          [4, 6],
          [4, 7],
          [181, 6],
        ],
      ],
    };
    const invalidIncident = {
      id: "incident-invalid",
      event_type: "road_closure",
      status: "OPEN",
      confidence_state: "EMERGING",
      severity: null,
      public_geometry: malformed,
      started_at: null,
      last_signal_at: null,
      updated_at: "2026-09-25T08:30:00Z",
    } as unknown as PublicIncident;
    const missingIncident = {
      ...invalidIncident,
      id: "incident-missing",
      public_geometry: undefined,
    } as unknown as PublicIncident;
    const collection = toIncidentFeatureCollection([
      invalidIncident,
      missingIncident,
    ]);
    expect(collection.features).toEqual([]);
    render(
      <IncidentMap
        {...makeProps({ featureCollection: collection, onUnavailable })}
      />,
    );
    await waitFor(() => expect(mockMap.handlers["style.load"]).toBeTruthy());

    expect(() => act(() => fire("style.load"))).not.toThrow();
    expect(mockMap.instance.addSource).toHaveBeenCalledWith(
      "public-incidents",
      expect.objectContaining({ data: collection }),
    );
    expect(mockMap.instance.fitBounds).not.toHaveBeenCalled();
    expect(onUnavailable).not.toHaveBeenCalled();
  });

  it("does not disable the map for a tile error propagated to the map before style load", async () => {
    const onUnavailable = vi.fn();
    const { unmount } = render(
      <IncidentMap {...makeProps({ onUnavailable })} />,
    );

    act(() =>
      fire("error", {
        target: mockMap.instance,
        sourceId: "basemap",
        error: { message: "tile request failed" },
      }),
    );
    expect(onUnavailable).not.toHaveBeenCalled();

    act(() => fire("style.load"));
    expect(mockMap.instance.addLayer).toHaveBeenCalledTimes(2);
    expect(onUnavailable).not.toHaveBeenCalled();
    unmount();
  });

  it("does not disable the map for a resource error propagated after style load", async () => {
    const onUnavailable = vi.fn();
    const { unmount } = render(
      <IncidentMap {...makeProps({ onUnavailable })} />,
    );

    act(() => fire("style.load"));
    act(() =>
      fire("error", {
        target: mockMap.instance,
        sourceId: "basemap",
        sourceDataType: "tile",
        error: { message: "tile request failed" },
      }),
    );

    expect(mockMap.instance.addLayer).toHaveBeenCalledTimes(2);
    expect(onUnavailable).not.toHaveBeenCalled();
    unmount();
  });

  it("falls back once when GeoJSON source or layer setup fails", () => {
    const onUnavailable = vi.fn();
    mockMap.instance.addLayer.mockImplementationOnce(() => {
      throw new Error("layer setup failed");
    });
    const { unmount } = render(
      <IncidentMap {...makeProps({ onUnavailable })} />,
    );

    act(() => fire("style.load"));
    act(() => fire("style.load"));
    act(() =>
      fire("error", {
        target: mockMap.instance,
        sourceId: "public-incidents",
        error: { message: "source request failed" },
      }),
    );

    expect(mockMap.instance.addSource).toHaveBeenCalledOnce();
    expect(onUnavailable).toHaveBeenCalledOnce();
    unmount();
    expect(mockMap.instance.remove).toHaveBeenCalledOnce();
  });

  it("reports a synchronous WebGL initialization failure", async () => {
    mockMap.shouldThrow = true;
    const onUnavailable = vi.fn();
    render(<IncidentMap {...makeProps({ onUnavailable })} />);
    await waitFor(() => expect(onUnavailable).toHaveBeenCalledOnce());
  });

  it("falls back when the base style never becomes ready", async () => {
    vi.useFakeTimers();
    const onUnavailable = vi.fn();
    const { unmount } = render(
      <IncidentMap {...makeProps({ onUnavailable })} />,
    );

    expect(mockMap.handlers["style.load"]).toBeTruthy();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(14_999);
    });
    expect(onUnavailable).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(onUnavailable).toHaveBeenCalledOnce();
    act(() =>
      fire("error", {
        target: mockMap.instance,
        error: { message: "late style failure" },
      }),
    );
    act(() => fire("style.load"));
    expect(onUnavailable).toHaveBeenCalledOnce();
    expect(mockMap.instance.addSource).not.toHaveBeenCalled();
    unmount();
    expect(mockMap.instance.remove).toHaveBeenCalledOnce();
  });

  it("clears the pending style timeout when unmounted", async () => {
    vi.useFakeTimers();
    const onUnavailable = vi.fn();
    const { unmount } = render(
      <IncidentMap {...makeProps({ onUnavailable })} />,
    );

    expect(mockMap.handlers["style.load"]).toBeTruthy();
    unmount();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });

    expect(onUnavailable).not.toHaveBeenCalled();
    expect(mockMap.instance.remove).toHaveBeenCalledOnce();
  });
});
