import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { VerificationInbox } from "./verification-inbox";
import { verificationRequest } from "./verification-test-fixtures";

const mocks = vi.hoisted(() => ({
  fetchVerificationRequests: vi.fn(),
}));

vi.mock("../../../lib/api/verifications", () => mocks);

describe("VerificationInbox", () => {
  afterEach(() => {
    cleanup();
    vi.resetAllMocks();
  });

  it("loads server-authorized requests and links to accessible detail", async () => {
    mocks.fetchVerificationRequests.mockResolvedValue({
      ok: true,
      data: [verificationRequest],
    });
    render(<VerificationInbox />);

    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
    expect(screen.getByText("Open")).toBeTruthy();
    expect(screen.getByText("Moderate severity")).toBeTruthy();
    expect(screen.getByText("Requested")).toBeTruthy();
    expect(screen.getByText("Expires")).toBeTruthy();
    expect(
      screen
        .getByRole("link", { name: "Review request: Road Closure" })
        .getAttribute("href"),
    ).toBe(`/verifications/${verificationRequest.id}`);
    expect(mocks.fetchVerificationRequests).toHaveBeenCalledWith(
      expect.any(AbortSignal),
    );
    expect(screen.queryByText(/3\.37/)).toBeNull();
    expect(
      screen.getByRole("list", { name: "Active verification requests" })
        .className,
    ).toContain("space-y-3");
    expect(
      screen.getByRole("link", { name: "Review request: Road Closure" })
        .className,
    ).toContain("min-h-11");
  });

  it("shows an empty inbox without inventing requests", async () => {
    mocks.fetchVerificationRequests.mockResolvedValue({ ok: true, data: [] });
    render(<VerificationInbox />);
    expect(
      await screen.findByText(/no verification requests to review/i),
    ).toBeTruthy();
    expect(screen.queryByRole("link", { name: /review request/i })).toBeNull();
  });

  it("keeps 401 and 403 as distinct states", async () => {
    mocks.fetchVerificationRequests
      .mockResolvedValueOnce({ ok: false, status: 401 })
      .mockResolvedValueOnce({ ok: false, status: 403 });
    const { unmount } = render(<VerificationInbox />);
    expect(
      await screen.findByText("Sign in to view verification requests."),
    ).toBeTruthy();
    unmount();
    render(<VerificationInbox />);
    expect(
      await screen.findByText(
        "This account cannot access verification requests.",
      ),
    ).toBeTruthy();
    expect(screen.queryByRole("link", { name: /review request/i })).toBeNull();
  });

  it("offers recovery for a 503 and reloads the bounded server result", async () => {
    mocks.fetchVerificationRequests
      .mockResolvedValueOnce({ ok: false, status: 503 })
      .mockResolvedValueOnce({ ok: true, data: [verificationRequest] });
    render(<VerificationInbox />);
    fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
    expect(
      await screen.findByRole("heading", { name: "Road Closure" }),
    ).toBeTruthy();
    expect(mocks.fetchVerificationRequests).toHaveBeenCalledTimes(2);
  });

  it("does not allow a late protected read to repopulate an unmounted inbox", async () => {
    let resolveRead!: (result: {
      ok: true;
      data: (typeof verificationRequest)[];
    }) => void;
    let signal: AbortSignal | undefined;
    mocks.fetchVerificationRequests.mockImplementation(
      (nextSignal: AbortSignal) => {
        signal = nextSignal;
        return new Promise((resolve) => {
          resolveRead = resolve;
        });
      },
    );
    const { unmount } = render(<VerificationInbox />);
    await waitFor(() =>
      expect(mocks.fetchVerificationRequests).toHaveBeenCalledOnce(),
    );
    unmount();

    await act(async () => {
      resolveRead({ ok: true, data: [verificationRequest] });
    });
    expect(signal?.aborted).toBe(true);
    expect(screen.queryByText("Road Closure")).toBeNull();
  });

  it("supports keyboard navigation to request links", async () => {
    mocks.fetchVerificationRequests.mockResolvedValue({
      ok: true,
      data: [verificationRequest],
    });
    render(<VerificationInbox />);
    const link = await screen.findByRole("link", {
      name: "Review request: Road Closure",
    });
    act(() => link.focus());
    expect(document.activeElement).toBe(link);
    expect(link.className).toContain("focus-visible:outline");
  });
});
