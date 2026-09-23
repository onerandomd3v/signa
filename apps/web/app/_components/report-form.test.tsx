import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ReportForm } from "./report-form";

describe("ReportForm", () => {
  afterEach(() => cleanup());

  it("submits a text-only report using the generated request shape", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<ReportForm onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText(/what happened/i), {
      target: { value: "  Smoke seen near the station.  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit report" }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith({
        raw_text: "Smoke seen near the station.",
      });
    });
    expect(await screen.findByText("Report submitted.")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "Write another report" }),
    ).toBeTruthy();
  });

  it("requires non-empty text and does not invoke the submit handler", () => {
    const onSubmit = vi.fn();
    render(<ReportForm onSubmit={onSubmit} />);

    fireEvent.click(screen.getByRole("button", { name: "Submit report" }));

    expect(screen.getByRole("alert").textContent).toBe(
      "Add a short description.",
    );
    expect(
      screen.getByLabelText(/what happened/i).getAttribute("aria-invalid"),
    ).toBe("true");
    expect(
      screen.getByLabelText(/what happened/i).getAttribute("aria-required"),
    ).toBe("true");
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("shows a submitting state and prevents duplicate submissions", async () => {
    let resolveSubmit: (() => void) | undefined;
    const onSubmit = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          resolveSubmit = resolve;
        }),
    );
    render(<ReportForm onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText(/what happened/i), {
      target: { value: "A road is blocked." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit report" }));

    expect(
      screen
        .getByRole("button", { name: "Submitting…" })
        .hasAttribute("disabled"),
    ).toBe(true);
    expect(screen.getByText("Submitting report…")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Submitting…" }));
    expect(onSubmit).toHaveBeenCalledTimes(1);

    resolveSubmit?.();
    expect(await screen.findByText("Report submitted.")).toBeTruthy();
  });

  it("shows a recoverable error when submission fails", async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error("offline"));
    render(<ReportForm onSubmit={onSubmit} />);

    fireEvent.change(screen.getByLabelText(/what happened/i), {
      target: { value: "Water is rising near the bridge." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit report" }));

    expect((await screen.findByRole("alert")).textContent).toContain(
      "Could not submit. Try again.",
    );
    expect(
      screen
        .getByRole("button", { name: "Submit report" })
        .hasAttribute("disabled"),
    ).toBe(false);
  });

  it("never claims a report was sent when no submit handler is provided", async () => {
    render(<ReportForm />);
    fireEvent.change(screen.getByLabelText(/what happened/i), {
      target: { value: "Smoke seen near the station." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit report" }));

    expect((await screen.findByRole("alert")).textContent).toContain(
      "Not sent — report submission isn’t available yet.",
    );
  });
});
