import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PushSettings } from "./push-settings";

vi.mock("./push", () => ({
  getVapidPublicKey: vi.fn(() => undefined),
  readPushState: vi.fn().mockResolvedValue("not-requested"),
  enablePush: vi.fn(),
  disablePush: vi.fn(),
}));

import { enablePush, readPushState } from "./push";

describe("PushSettings", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(() => cleanup());

  it("does not prompt automatically and explains missing configuration", async () => {
    render(<PushSettings />);
    expect(
      await screen.findByText(
        "Notifications are off. Permission hasn’t been requested.",
      ),
    ).toBeTruthy();
    expect(enablePush).not.toHaveBeenCalled();
    expect(
      screen
        .getByRole("button", { name: "Enable notifications" })
        .hasAttribute("disabled"),
    ).toBe(true);
    expect(screen.getByText(/A public VAPID key is required/)).toBeTruthy();
  });

  it("refreshes browser permission after returning to the page", async () => {
    render(<PushSettings />);
    await screen.findByText(
      "Notifications are off. Permission hasn’t been requested.",
    );
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      value: "visible",
    });
    fireEvent(document, new Event("visibilitychange"));
    await waitFor(() => expect(readPushState).toHaveBeenCalledTimes(2));
  });
});
