import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { RouteRelevanceResponse } from "@/lib/api/generated";
import type { RouteRelevanceResult } from "@/lib/api/route-relevance";
import {
  RouteRelevanceExperience,
  RouteRelevancePanel,
  type RouteRelevanceEvaluator,
} from "./route-relevance";

afterEach(() => cleanup());

const routeResponse: RouteRelevanceResponse = {
  classification: "NOT_RELEVANT",
  route_geometry: {
    type: "LineString",
    coordinates: [
      [3.3792, 6.5244],
      [3.3947, 6.4541],
    ],
  },
  incidents: [],
};

const success = (
  classification: RouteRelevanceResponse["classification"] = "NOT_RELEVANT",
): RouteRelevanceResult => ({
  status: "success",
  response: { ...routeResponse, classification },
});

function fillRoute(originLatitude = "6.5244", destinationLongitude = "3.3947") {
  const origin = within(screen.getByRole("group", { name: "Origin" }));
  const destination = within(
    screen.getByRole("group", { name: "Destination" }),
  );
  fireEvent.change(origin.getByLabelText(/Latitude/), {
    target: { value: originLatitude },
  });
  fireEvent.change(origin.getByLabelText(/Longitude/), {
    target: { value: "3.3792" },
  });
  fireEvent.change(destination.getByLabelText(/Latitude/), {
    target: { value: "6.4541" },
  });
  fireEvent.change(destination.getByLabelText(/Longitude/), {
    target: { value: destinationLongitude },
  });
}

describe("RouteRelevancePanel", () => {
  it("announces no-route and loading states", () => {
    const { rerender } = render(
      <RouteRelevancePanel state={{ status: "no-route" }} />,
    );
    expect(
      screen.getByRole("heading", { name: "No route checked" }),
    ).toBeTruthy();
    rerender(<RouteRelevancePanel state={{ status: "loading" }} />);
    expect(screen.getByRole("status").textContent).toContain(
      "Waiting for the route result.",
    );
  });

  it("does not describe UNKNOWN or NOT_RELEVANT as proof of safety", () => {
    const { rerender } = render(
      <RouteRelevancePanel state={{ status: "unknown" }} />,
    );
    expect(screen.getByText(/not a safety assessment/i)).toBeTruthy();
    rerender(<RouteRelevancePanel state={{ status: "not-relevant" }} />);
    expect(
      screen.getByText(/does not confirm that the route is safe/i),
    ).toBeTruthy();
    rerender(<RouteRelevancePanel state={{ status: "relevant" }} />);
    expect(
      screen.getByText(/not proof an incident is occurring/i),
    ).toBeTruthy();
  });

  it("renders distinct authentication, authorization, rate-limit and backend states", () => {
    const retry = vi.fn();
    const { rerender } = render(
      <RouteRelevancePanel state={{ status: "unauthorized" }} />,
    );
    expect(screen.getByText(/active Signa session is required/i)).toBeTruthy();
    rerender(<RouteRelevancePanel state={{ status: "forbidden" }} />);
    expect(
      screen.getByText(/isn’t authorized to request route checks/i),
    ).toBeTruthy();
    rerender(
      <RouteRelevancePanel
        state={{ status: "rate-limited", retryAfter: "30", onRetry: retry }}
      />,
    );
    expect(screen.getByText(/try again in about 30 seconds/i)).toBeTruthy();
    const retryButton = screen.getByRole("button", {
      name: "Retry route check",
    });
    retryButton.focus();
    expect(document.activeElement).toBe(retryButton);
    fireEvent.click(retryButton);
    expect(retry).toHaveBeenCalledOnce();
    rerender(
      <RouteRelevancePanel
        state={{ status: "incident-data-unavailable", onRetry: retry }}
      />,
    );
    expect(
      screen.getByText(/missing data does not mean the route is safe/i),
    ).toBeTruthy();
  });
});

describe("RouteRelevanceExperience", () => {
  it("waits for an explicit, validated coordinate request", async () => {
    const evaluate = vi.fn<RouteRelevanceEvaluator>(async () => success());
    const onResultChange = vi.fn();
    render(
      <RouteRelevanceExperience
        evaluate={evaluate}
        onResultChange={onResultChange}
      />,
    );
    expect(
      screen.getByRole("heading", { name: "No route checked" }),
    ).toBeTruthy();
    expect(evaluate).not.toHaveBeenCalled();

    fillRoute();
    fireEvent.click(screen.getByRole("button", { name: "Check route" }));

    expect(
      await screen.findByText(/does not confirm that the route is safe/i),
    ).toBeTruthy();
    expect(evaluate).toHaveBeenCalledOnce();
    expect(evaluate.mock.calls[0][0]).toEqual({
      origin: { latitude: 6.5244, longitude: 3.3792 },
      destination: { latitude: 6.4541, longitude: 3.3947 },
    });
    expect(evaluate.mock.calls[0][1]).toBeInstanceOf(AbortSignal);
    expect(onResultChange).toHaveBeenLastCalledWith({
      geometry: routeResponse.route_geometry,
      incidents: [],
    });
  });

  it("validates latitude, longitude, and ordered waypoint fields before requesting", () => {
    const evaluate = vi.fn<RouteRelevanceEvaluator>(async () => success());
    render(
      <RouteRelevanceExperience evaluate={evaluate} onResultChange={vi.fn()} />,
    );
    fillRoute("91");
    fireEvent.submit(
      screen.getByRole("form", { name: "Check route relevance" }),
    );
    expect(
      screen.getByText(/latitude must be between -90 and 90/i),
    ).toBeTruthy();
    expect(evaluate).not.toHaveBeenCalled();

    fireEvent.change(
      within(screen.getByRole("group", { name: "Origin" })).getByLabelText(
        /Latitude/,
      ),
      { target: { value: "6.5244" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "Add waypoint" }));
    fireEvent.click(screen.getByRole("button", { name: "Check route" }));
    expect(
      screen.getByText(/both latitude and longitude for waypoint 1/i),
    ).toBeTruthy();
    expect(evaluate).not.toHaveBeenCalled();
  });

  it("retries the latest explicitly submitted route request", async () => {
    const evaluate = vi
      .fn<RouteRelevanceEvaluator>()
      .mockResolvedValueOnce({ status: "rate-limited", retryAfter: "0" })
      .mockResolvedValueOnce(success());
    render(
      <RouteRelevanceExperience evaluate={evaluate} onResultChange={vi.fn()} />,
    );
    fillRoute("6.5", "3.8");
    fireEvent.click(screen.getByRole("button", { name: "Check route" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Retry route check" }),
    );
    await screen.findByText(/does not confirm that the route is safe/i);
    expect(evaluate).toHaveBeenCalledTimes(2);
    expect(evaluate.mock.calls[1][0]).toEqual(evaluate.mock.calls[0][0]);
    expect(evaluate.mock.calls[0][0].origin.latitude).toBe(6.5);
    expect(evaluate.mock.calls[0][0].destination.longitude).toBe(3.8);
  });

  it("cancels obsolete requests and ignores their late responses", async () => {
    let resolveFirst: ((result: RouteRelevanceResult) => void) | undefined;
    let firstSignal: AbortSignal | undefined;
    const evaluate = vi
      .fn<RouteRelevanceEvaluator>()
      .mockImplementationOnce((_request, signal) => {
        firstSignal = signal;
        return new Promise((resolve) => {
          resolveFirst = resolve;
        });
      })
      .mockResolvedValueOnce(success("NOT_RELEVANT"));
    render(
      <RouteRelevanceExperience evaluate={evaluate} onResultChange={vi.fn()} />,
    );
    fillRoute("6.5", "3.8");
    fireEvent.click(screen.getByRole("button", { name: "Check route" }));
    fillRoute("6.6", "3.9");
    expect(firstSignal?.aborted).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Check route" }));
    await screen.findByText(/does not confirm that the route is safe/i);

    await act(async () => {
      resolveFirst?.(success("RELEVANT"));
    });
    expect(
      screen.queryByRole("heading", { name: /may affect this route/i }),
    ).toBeNull();
    expect(evaluate.mock.calls[1][0].origin.latitude).toBe(6.6);
  });

  it("aborts a pending request when unmounted", async () => {
    let resolveRequest: ((result: RouteRelevanceResult) => void) | undefined;
    let signal: AbortSignal | undefined;
    const evaluate = vi.fn((_request, requestSignal: AbortSignal) => {
      signal = requestSignal;
      return new Promise<RouteRelevanceResult>((resolve) => {
        resolveRequest = resolve;
      });
    });
    const { unmount } = render(
      <RouteRelevanceExperience evaluate={evaluate} onResultChange={vi.fn()} />,
    );
    fillRoute();
    fireEvent.click(screen.getByRole("button", { name: "Check route" }));
    await waitFor(() => expect(evaluate).toHaveBeenCalledOnce());
    unmount();
    expect(signal?.aborted).toBe(true);
    resolveRequest?.(success());
  });
});
