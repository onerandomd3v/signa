import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { VerificationDetailExperience } from "./verification-detail-experience";
import { requestId, verificationRequest } from "./verification-test-fixtures";

const mocks = vi.hoisted(() => ({
  fetchVerificationRequest: vi.fn(),
  sendVerificationResponse: vi.fn(),
}));

vi.mock("../../../lib/api/verifications", () => mocks);

const accepted = {
  id: "33333333-3333-4333-8333-333333333333",
  request_id: requestId,
  incident_id: verificationRequest.incident.id,
  conclusion: "CONFIRM",
  observation: null,
  created_at: "2026-09-29T09:10:00Z",
};
const success = { ok: true as const, data: accepted };
const failure = (status: number) => ({ ok: false as const, status });

describe("VerificationDetailExperience", () => {
  afterEach(() => {
    cleanup();
    vi.resetAllMocks();
  });

  function openDetail() {
    mocks.fetchVerificationRequest.mockResolvedValue({
      ok: true,
      data: verificationRequest,
    });
    mocks.sendVerificationResponse.mockResolvedValue(success);
    return render(<VerificationDetailExperience requestId={requestId} />);
  }

  it("shows the exact safety prompt before response controls and only public fields", async () => {
    const requestWithPrivateNoise = {
      ...verificationRequest,
      reporter_id: "private-reporter",
      raw_text: "Private source report text",
      device_location: { latitude: 6.52, longitude: 3.37 },
      verifier_id: "private-verifier",
    };
    mocks.fetchVerificationRequest.mockResolvedValue({
      ok: true,
      data: requestWithPrivateNoise,
    });
    render(<VerificationDetailExperience requestId={requestId} />);

    const safety = await screen.findByText(
      "Respond only from what you already safely know or observed. Never approach an incident or unsafe area to verify it.",
    );
    const responseHeading = screen.getByRole("heading", {
      name: "Your response",
    });
    expect(
      safety.compareDocumentPosition(responseHeading) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Road Closure" })).toBeTruthy();
    expect(screen.queryByText("Private source report text")).toBeNull();
    expect(screen.queryByText("private-reporter")).toBeNull();
    expect(screen.queryByText("private-verifier")).toBeNull();
    expect(screen.queryByText(/3\.37/)).toBeNull();
    expect(screen.queryByRole("img")).toBeNull();
    expect(screen.getByText(/Requested/)).toBeTruthy();
    expect(screen.getByText(/Expires/)).toBeTruthy();
  });

  it("shows a privacy-safe 404 and a safe return path", async () => {
    mocks.fetchVerificationRequest.mockResolvedValue(failure(404));
    render(<VerificationDetailExperience requestId={requestId} />);
    expect(
      await screen.findByText(
        "This verification request is no longer available.",
      ),
    ).toBeTruthy();
    expect(
      screen
        .getByRole("link", { name: "Return to inbox" })
        .getAttribute("href"),
    ).toBe("/verifications");
    expect(
      screen.queryByText(/expired|cancelled|assigned|private/i),
    ).toBeNull();
    expect(screen.queryByRole("heading", { name: "Road Closure" })).toBeNull();
  });

  it("keeps unauthenticated and forbidden detail states distinct", async () => {
    mocks.fetchVerificationRequest
      .mockResolvedValueOnce(failure(401))
      .mockResolvedValueOnce(failure(403));
    const { unmount } = render(
      <VerificationDetailExperience requestId={requestId} />,
    );
    expect(
      await screen.findByText("Sign in to view this verification request."),
    ).toBeTruthy();
    unmount();
    render(<VerificationDetailExperience requestId={requestId} />);
    expect(
      await screen.findByText(
        "This account cannot access verification requests.",
      ),
    ).toBeTruthy();
  });

  it.each([
    ["CONFIRM", "Confirm", { conclusion: "CONFIRM" }],
    ["CANNOT_CONFIRM", "Cannot confirm", { conclusion: "CANNOT_CONFIRM" }],
    ["DISPUTE", "Dispute", { conclusion: "DISPUTE" }],
    ["SAW", "Saw it", { observation: "SAW" }],
    ["HEARD", "Heard it", { observation: "HEARD" }],
  ] as const)(
    "submits the independent response %s",
    async (_value, label, body) => {
      openDetail();
      fireEvent.click(await screen.findByLabelText(label));
      fireEvent.click(screen.getByRole("button", { name: "Send response" }));
      await screen.findByText(/Your response was recorded as evidence/);
      expect(mocks.sendVerificationResponse).toHaveBeenCalledWith(
        requestId,
        body,
        expect.stringMatching(/^[0-9a-f-]{36}$/i),
      );
    },
  );

  it("supports both dimensions without inferring one from the other", async () => {
    openDetail();
    fireEvent.click(await screen.findByLabelText("Confirm"));
    fireEvent.click(screen.getByLabelText("Heard it"));
    fireEvent.click(screen.getByRole("button", { name: "Send response" }));
    await screen.findByText(/Your response was recorded as evidence/);
    expect(mocks.sendVerificationResponse.mock.calls[0][1]).toEqual({
      conclusion: "CONFIRM",
      observation: "HEARD",
    });
  });

  it("requires at least one response dimension", async () => {
    openDetail();
    const responseHeading = await screen.findByRole("heading", {
      name: "Your response",
    });
    fireEvent.submit(
      responseHeading.closest("section")!.querySelector("form")!,
    );
    expect((await screen.findByRole("alert")).textContent).toContain(
      "Choose a conclusion or an observation before submitting.",
    );
    expect(mocks.sendVerificationResponse).not.toHaveBeenCalled();
  });

  it.each([200, 201])("treats HTTP %i as recorded evidence", async () => {
    openDetail();
    mocks.sendVerificationResponse.mockResolvedValue(success);
    fireEvent.click(await screen.findByLabelText("Cannot confirm"));
    fireEvent.click(screen.getByRole("button", { name: "Send response" }));
    expect((await screen.findByRole("status")).textContent).toContain(
      "Your response was recorded as evidence. It does not determine incident truth or change confidence.",
    );
    expect(screen.queryByText(/request is completed/i)).toBeNull();
    expect(screen.queryByRole("button", { name: "Send response" })).toBeNull();
  });

  it("creates one opaque key and reuses it for an unchanged retry", async () => {
    openDetail();
    mocks.sendVerificationResponse
      .mockResolvedValueOnce(failure(503))
      .mockResolvedValueOnce(success);
    fireEvent.click(await screen.findByLabelText("Confirm"));
    fireEvent.click(screen.getByRole("button", { name: "Send response" }));
    expect((await screen.findByRole("status")).textContent).toMatch(
      /service is unavailable/i,
    );
    fireEvent.click(screen.getByRole("button", { name: "Send response" }));
    await screen.findByText(/Your response was recorded as evidence/);
    const firstKey = mocks.sendVerificationResponse.mock.calls[0][2];
    expect(firstKey).toMatch(/^[0-9a-f-]{36}$/i);
    expect(mocks.sendVerificationResponse.mock.calls[1][2]).toBe(firstKey);
    expect(mocks.sendVerificationResponse.mock.calls[0][1]).toEqual(
      mocks.sendVerificationResponse.mock.calls[1][1],
    );
  });

  it("creates a new key when response content changes after failure", async () => {
    openDetail();
    mocks.sendVerificationResponse
      .mockResolvedValueOnce(failure(503))
      .mockResolvedValueOnce(success);
    fireEvent.click(await screen.findByLabelText("Confirm"));
    fireEvent.click(screen.getByRole("button", { name: "Send response" }));
    await screen.findByRole("status");
    fireEvent.click(screen.getByLabelText("Dispute"));
    fireEvent.click(screen.getByRole("button", { name: "Send response" }));
    await screen.findByText(/Your response was recorded as evidence/);
    expect(mocks.sendVerificationResponse.mock.calls[0][2]).not.toBe(
      mocks.sendVerificationResponse.mock.calls[1][2],
    );
    expect(mocks.sendVerificationResponse.mock.calls[1][1]).toEqual({
      conclusion: "DISPUTE",
    });
  });

  it("prevents duplicate submissions while a request is in flight", async () => {
    openDetail();
    let resolveSend!: (result: { ok: true; data: unknown }) => void;
    mocks.sendVerificationResponse.mockImplementation(
      () => new Promise((resolve) => (resolveSend = resolve)),
    );
    fireEvent.click(await screen.findByLabelText("Saw it"));
    const form = screen
      .getByRole("button", { name: "Send response" })
      .closest("form")!;
    fireEvent.submit(form);
    fireEvent.submit(form);
    expect(mocks.sendVerificationResponse).toHaveBeenCalledOnce();
    await act(async () => resolveSend(success));
    expect(
      await screen.findByText(/Your response was recorded as evidence/),
    ).toBeTruthy();
  });

  it("uses a fresh key after a 409 and explains the conflict", async () => {
    openDetail();
    mocks.sendVerificationResponse
      .mockResolvedValueOnce(failure(409))
      .mockResolvedValueOnce(success);
    fireEvent.click(await screen.findByLabelText("Heard it"));
    fireEvent.click(screen.getByRole("button", { name: "Send response" }));
    expect((await screen.findByRole("status")).textContent).toMatch(
      /key conflicts/i,
    );
    fireEvent.click(screen.getByRole("button", { name: "Send response" }));
    await screen.findByText(/Your response was recorded as evidence/);
    expect(mocks.sendVerificationResponse.mock.calls[1][2]).not.toBe(
      mocks.sendVerificationResponse.mock.calls[0][2],
    );
  });

  it.each([
    [401, "Sign in to view this verification request."],
    [403, "This account cannot access verification requests."],
    [404, "This verification request is no longer available."],
  ])(
    "clears protected detail after submit HTTP %i",
    async (status, message) => {
      openDetail();
      mocks.sendVerificationResponse.mockResolvedValue(failure(status));
      fireEvent.click(await screen.findByLabelText("Confirm"));
      fireEvent.click(screen.getByRole("button", { name: "Send response" }));
      expect(await screen.findByText(message)).toBeTruthy();
      expect(
        screen.queryByRole("heading", { name: "Road Closure" }),
      ).toBeNull();
      expect(screen.queryByRole("radio")).toBeNull();
    },
  );

  it.each([
    [400, "Check your response and try again."],
    [503, "The service is unavailable. Try again."],
    [0, "Couldn’t send your response. Check your connection and try again."],
  ])(
    "keeps the request available and shows safe recovery for status %i",
    async (status, message) => {
      openDetail();
      mocks.sendVerificationResponse.mockResolvedValue(failure(status));
      fireEvent.click(await screen.findByLabelText("Confirm"));
      fireEvent.click(screen.getByRole("button", { name: "Send response" }));
      expect((await screen.findByRole("status")).textContent).toContain(
        message,
      );
      expect(
        screen.getByRole("heading", { name: "Road Closure" }),
      ).toBeTruthy();
      expect(screen.getByRole("radio", { name: "Confirm" })).toBeTruthy();
    },
  );

  it("cancels an obsolete detail read on unmount", async () => {
    let signal: AbortSignal | undefined;
    let resolveRead!: (result: {
      ok: true;
      data: typeof verificationRequest;
    }) => void;
    mocks.fetchVerificationRequest.mockImplementation(
      (_requestId: string, nextSignal: AbortSignal) => {
        signal = nextSignal;
        return new Promise((resolve) => (resolveRead = resolve));
      },
    );
    const { unmount } = render(
      <VerificationDetailExperience requestId={requestId} />,
    );
    await waitFor(() =>
      expect(mocks.fetchVerificationRequest).toHaveBeenCalledOnce(),
    );
    unmount();
    await act(async () => resolveRead({ ok: true, data: verificationRequest }));
    expect(signal?.aborted).toBe(true);
    expect(screen.queryByText("Road Closure")).toBeNull();
  });

  it("keeps response controls keyboard-operable and visibly focused", async () => {
    openDetail();
    const confirm = await screen.findByLabelText("Confirm");
    act(() => confirm.focus());
    expect(document.activeElement).toBe(confirm);
    expect(confirm.closest("label")?.className).toContain(
      "focus-within:ring-2",
    );
    expect(screen.getByRole("group", { name: /conclusion/i })).toBeTruthy();
    expect(screen.getByRole("group", { name: /observation/i })).toBeTruthy();
  });
});
