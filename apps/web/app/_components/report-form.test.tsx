import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  DeviceLocation,
  ReportAcknowledgement,
} from "../../lib/api/generated";
import { DeviceLocationError } from "./device-location";
import { ReportForm } from "./report-form";

const accepted: ReportAcknowledgement = {
  report_id: "report-123",
  status: "accepted",
  submitted_at: "2026-09-23T12:00:00Z",
};
const success = { ok: true as const, acknowledgement: accepted };
const failure = (status: number, retryAfter?: string) => ({
  ok: false as const,
  status,
  retryAfter,
});

describe("ReportForm", () => {
  afterEach(() => cleanup());

  function enterReport(text = "Smoke near the station.") {
    fireEvent.change(screen.getByLabelText(/what happened/i), {
      target: { value: text },
    });
  }

  function submit() {
    fireEvent.click(screen.getByRole("button", { name: "Submit report" }));
  }

  it("submits text and shows the accepted reference", async () => {
    const send = vi.fn().mockResolvedValue(success);
    render(<ReportForm submitReport={send} />);
    enterReport("  Smoke near the station.  ");
    submit();

    await waitFor(() => expect(send).toHaveBeenCalledOnce());
    expect(send.mock.calls[0][0]).toEqual({
      raw_text: "Smoke near the station.",
    });
    expect(send.mock.calls[0][1]).toMatch(/^[0-9a-f-]{36}$/i);
    expect(
      await screen.findByText("Report accepted for processing."),
    ).toBeTruthy();
    expect(screen.getByText(/report-123/)).toBeTruthy();
  });

  it("requires non-empty text", () => {
    const send = vi.fn();
    render(<ReportForm submitReport={send} />);
    fireEvent.submit(document.querySelector("form")!);
    expect(screen.getByRole("alert").textContent).toBe(
      "Add a short description.",
    );
    expect(
      screen.getByLabelText(/what happened/i).getAttribute("aria-invalid"),
    ).toBe("true");
    expect(send).not.toHaveBeenCalled();
  });

  it("prevents duplicate submissions while the request is pending", async () => {
    let resolve!: (value: typeof success) => void;
    const send = vi.fn(
      () =>
        new Promise<typeof success>((done) => {
          resolve = done;
        }),
    );
    render(<ReportForm submitReport={send} />);
    enterReport();
    submit();
    fireEvent.click(screen.getByRole("button", { name: "Submitting…" }));
    expect(send).toHaveBeenCalledOnce();
    resolve(success);
    expect(
      await screen.findByText("Report accepted for processing."),
    ).toBeTruthy();
  });

  it("keeps a key for an unchanged retry and rotates it when the text changes", async () => {
    const send = vi
      .fn()
      .mockResolvedValueOnce(failure(500))
      .mockResolvedValueOnce(failure(400))
      .mockResolvedValueOnce(success);
    render(<ReportForm submitReport={send} />);
    enterReport();
    submit();
    await screen.findByRole("alert");
    submit();
    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send.mock.calls[1][1]).toBe(send.mock.calls[0][1]);
    expect((await screen.findByRole("alert")).textContent).toBe(
      "Please check the report text and try again.",
    );
    enterReport("Smoke spreading by the station.");
    submit();
    await screen.findByText("Report accepted for processing.");
    expect(send.mock.calls[2][1]).not.toBe(send.mock.calls[1][1]);
  });

  it("recovers from an idempotency conflict with a fresh key", async () => {
    const send = vi
      .fn()
      .mockResolvedValueOnce(failure(409))
      .mockResolvedValueOnce(success);
    render(<ReportForm submitReport={send} />);
    enterReport();
    submit();
    expect((await screen.findByRole("alert")).textContent).toContain(
      "conflicted",
    );
    submit();
    await screen.findByText("Report accepted for processing.");
    expect(send.mock.calls[1][1]).not.toBe(send.mock.calls[0][1]);
  });

  it("rotates the key after success when starting another report", async () => {
    const send = vi.fn().mockResolvedValue(success);
    render(<ReportForm submitReport={send} />);
    enterReport();
    submit();
    await screen.findByText("Report accepted for processing.");
    fireEvent.click(
      screen.getByRole("button", { name: "Write another report" }),
    );
    enterReport("Water rising by the bridge.");
    submit();
    await waitFor(() => expect(send).toHaveBeenCalledTimes(2));
    expect(send.mock.calls[1][1]).not.toBe(send.mock.calls[0][1]);
  });

  it("honors Retry-After and retains the idempotency key after rate limiting", async () => {
    const send = vi
      .fn()
      .mockResolvedValueOnce(failure(429, "15"))
      .mockResolvedValueOnce(success);
    render(<ReportForm submitReport={send} />);
    enterReport();
    submit();
    expect((await screen.findByRole("alert")).textContent).toContain(
      "15 seconds",
    );
    submit();
    await screen.findByText("Report accepted for processing.");
    expect(send.mock.calls[1][1]).toBe(send.mock.calls[0][1]);
  });

  it("preserves text after a network error and retries with the same key", async () => {
    const send = vi
      .fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce(success);
    render(<ReportForm submitReport={send} />);
    enterReport();
    submit();
    await screen.findByRole("alert");
    expect(
      (screen.getByLabelText(/what happened/i) as HTMLTextAreaElement).value,
    ).toBe("Smoke near the station.");
    submit();
    await screen.findByText("Report accepted for processing.");
    expect(send.mock.calls[1][1]).toBe(send.mock.calls[0][1]);
  });

  it("sends opt-in location without displaying coordinates", async () => {
    const location: DeviceLocation = {
      latitude: 6.5244,
      longitude: 3.3792,
      accuracy: 42,
    };
    const requestLocation = vi.fn().mockResolvedValue(location);
    const send = vi.fn().mockResolvedValue(success);
    render(
      <ReportForm submitReport={send} requestLocation={requestLocation} />,
    );
    expect(requestLocation).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Share my location" }));
    await screen.findByText(/approximate location ready/i);
    expect(screen.queryByText(/6\.5244|3\.3792/)).toBeNull();
    enterReport();
    submit();
    await screen.findByText("Report accepted for processing.");
    expect(send.mock.calls[0][0]).toEqual({
      raw_text: "Smoke near the station.",
      device_location: location,
    });
  });

  it.each(["denied", "unavailable", "timeout", "low-accuracy"] as const)(
    "allows text-only reporting after location %s",
    async (reason) => {
      const send = vi.fn().mockResolvedValue(success);
      render(
        <ReportForm
          submitReport={send}
          requestLocation={vi
            .fn()
            .mockRejectedValue(new DeviceLocationError(reason))}
        />,
      );
      fireEvent.click(
        screen.getByRole("button", { name: "Share my location" }),
      );
      await screen.findByText(/continue without/i);
      enterReport();
      submit();
      await screen.findByText("Report accepted for processing.");
      expect(send.mock.calls[0][0]).toEqual({
        raw_text: "Smoke near the station.",
      });
    },
  );

  it("does not claim a report is unavailable in the default production path", () => {
    render(<ReportForm />);
    expect(
      screen.queryByText(/not sent|submission isn’t available/i),
    ).toBeNull();
  });
});
