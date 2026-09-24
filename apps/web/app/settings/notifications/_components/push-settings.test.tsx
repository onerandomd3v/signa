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

import { enablePush, getVapidPublicKey, readPushState } from "./push";

describe("PushSettings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(getVapidPublicKey).mockReturnValue(undefined);
  });
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

  it("announces pending work and allows retry after a subscription failure", async () => {
    vi.mocked(getVapidPublicKey).mockReturnValue("public-key");
    vi.mocked(enablePush)
      .mockRejectedValueOnce(new Error("subscription failed"))
      .mockResolvedValueOnce("subscribed");
    render(<PushSettings />);

    const button = await screen.findByRole("button", {
      name: "Enable notifications",
    });
    fireEvent.click(button);
    expect(
      await screen.findByText("Updating notification settings…"),
    ).toBeTruthy();
    expect(
      await screen.findByText(
        "Couldn’t enable push. Check the browser and public key settings.",
      ),
    ).toBeTruthy();

    fireEvent.click(button);
    expect(
      await screen.findByText(
        "This browser is subscribed. Server delivery is not connected yet.",
      ),
    ).toBeTruthy();
    expect(enablePush).toHaveBeenCalledTimes(2);
  });
});
