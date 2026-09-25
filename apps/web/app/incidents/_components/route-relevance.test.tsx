import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RouteRelevancePanel } from "./route-relevance";

afterEach(() => cleanup());

describe("RouteRelevancePanel", () => {
  it("explains that route checks are not available in production yet", () => {
    render(<RouteRelevancePanel state={{ status: "unavailable" }} />);
    expect(
      screen.getByRole("heading", { name: /route checks aren’t available/i }),
    ).toBeTruthy();
    expect(screen.getByText(/does not mean a route is safe/i)).toBeTruthy();
  });

  it("has a distinct no-route-selected state", () => {
    render(<RouteRelevancePanel state={{ status: "no-route" }} />);
    expect(
      screen.getByRole("heading", { name: "No route selected" }),
    ).toBeTruthy();
  });

  it("announces loading state politely", () => {
    render(<RouteRelevancePanel state={{ status: "loading" }} />);
    expect(screen.getByRole("status").textContent).toContain(
      "Waiting for a route relevance result.",
    );
  });

  it("does not imply that unknown or incident-unavailable means safe", () => {
    const { rerender } = render(
      <RouteRelevancePanel state={{ status: "unknown" }} />,
    );
    expect(screen.getByText(/not a safety assessment/i)).toBeTruthy();

    rerender(
      <RouteRelevancePanel state={{ status: "incident-data-unavailable" }} />,
    );
    expect(
      screen.getByText(/missing data does not mean the route is safe/i),
    ).toBeTruthy();
  });

  it("renders only backend-classified relevance without inferring from nearby geometry", () => {
    const { rerender } = render(
      <RouteRelevancePanel state={{ status: "relevant" }} />,
    );
    expect(
      screen.getByRole("heading", { name: /may affect this route/i }),
    ).toBeTruthy();
    expect(screen.getByText(/backend classified/i)).toBeTruthy();

    rerender(<RouteRelevancePanel state={{ status: "not-relevant" }} />);
    expect(
      screen.getByRole("heading", { name: /no evaluated incident/i }),
    ).toBeTruthy();
    expect(
      screen.getByText(/does not confirm that the route is safe/i),
    ).toBeTruthy();
  });

  it("exposes request failure as an alert and a keyboard-focusable retry button", () => {
    const onRetry = vi.fn();
    render(<RouteRelevancePanel state={{ status: "error", onRetry }} />);
    const retry = screen.getByRole("button", { name: "Retry route check" });
    retry.focus();
    expect(document.activeElement).toBe(retry);
    expect(screen.getByRole("alert")).toBeTruthy();
    fireEvent.click(retry);
    expect(onRetry).toHaveBeenCalledOnce();
  });
});
