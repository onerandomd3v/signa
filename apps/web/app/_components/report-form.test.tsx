import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { DeviceLocation } from "../../lib/api/generated";
import { DeviceLocationError } from "./device-location";
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

  it("requests location only after opt-in and includes it only after success", async () => {
    const location: DeviceLocation = {
      latitude: 6.5244,
      longitude: 3.3792,
      accuracy: 42,
    };
    const requestLocation = vi.fn().mockResolvedValue(location);
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(
      <ReportForm onSubmit={onSubmit} requestLocation={requestLocation} />,
    );

    expect(requestLocation).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Share my location" }));

    expect(await screen.findByText(/approximate location ready/i)).toBeTruthy();
    expect(screen.queryByText(/6\.5244|3\.3792/)).toBeNull();
    fireEvent.change(screen.getByLabelText(/what happened/i), {
      target: { value: "Smoke near the station." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit report" }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith({
        raw_text: "Smoke near the station.",
        device_location: location,
      });
    });
  });

  it.each([
    ["denied", "Permission denied. You can continue without location."],
    ["unavailable", "Location unavailable. You can continue without it."],
    [
      "timeout",
      "Location request timed out. Try again or continue without it.",
    ],
    [
      "low-accuracy",
      "Location was too imprecise. Try again or continue without it.",
    ],
  ] as const)(
    "handles %s without blocking a text-only report",
    async (reason, copy) => {
      const requestLocation = vi
        .fn()
        .mockRejectedValue(new DeviceLocationError(reason));
      const onSubmit = vi.fn().mockResolvedValue(undefined);
      render(
        <ReportForm onSubmit={onSubmit} requestLocation={requestLocation} />,
      );

      fireEvent.click(
        screen.getByRole("button", { name: "Share my location" }),
      );
      expect(await screen.findByText(copy)).toBeTruthy();
      fireEvent.change(screen.getByLabelText(/what happened/i), {
        target: { value: "Road blocked." },
      });
      fireEvent.click(screen.getByRole("button", { name: "Submit report" }));

      await waitFor(() => {
        expect(onSubmit).toHaveBeenCalledWith({ raw_text: "Road blocked." });
      });
    },
  );

  it("lets the user remove an accepted location before submitting", async () => {
    const location = { latitude: 6.5, longitude: 3.3, accuracy: 80 };
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(
      <ReportForm
        onSubmit={onSubmit}
        requestLocation={vi.fn().mockResolvedValue(location)}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Share my location" }));
    expect(await screen.findByText(/approximate location ready/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Remove location" }));
    fireEvent.change(screen.getByLabelText(/what happened/i), {
      target: { value: "Road blocked." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit report" }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith({ raw_text: "Road blocked." });
    });
  });

  it("ignores a location result after the user continues without it", async () => {
    let resolveLocation: ((location: DeviceLocation) => void) | undefined;
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(
      <ReportForm
        onSubmit={onSubmit}
        requestLocation={() =>
          new Promise((resolve) => {
            resolveLocation = resolve;
          })
        }
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Share my location" }));
    fireEvent.click(
      screen.getByRole("button", { name: "Continue without location" }),
    );
    resolveLocation?.({ latitude: 6.5, longitude: 3.3, accuracy: 50 });
    fireEvent.change(screen.getByLabelText(/what happened/i), {
      target: { value: "Road blocked." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Submit report" }));

    await waitFor(() => {
      expect(onSubmit).toHaveBeenCalledWith({ raw_text: "Road blocked." });
    });
  });
});
