import { describe, expect, it, vi } from "vitest";
import {
  DeviceLocationError,
  MAX_LOCATION_ACCURACY_METERS,
  requestDeviceLocation,
} from "./device-location";

function position(latitude: number, longitude: number, accuracy: number) {
  return {
    coords: { latitude, longitude, accuracy },
  } as GeolocationPosition;
}

describe("requestDeviceLocation", () => {
  it("requests a fresh high-accuracy position", async () => {
    const getCurrentPosition = vi.fn((success: PositionCallback) => {
      success(position(6.52, 3.38, 45));
    });

    await expect(
      requestDeviceLocation({ getCurrentPosition }),
    ).resolves.toEqual({ latitude: 6.52, longitude: 3.38, accuracy: 45 });
    expect(getCurrentPosition).toHaveBeenCalledWith(
      expect.any(Function),
      expect.any(Function),
      { enableHighAccuracy: true, maximumAge: 0, timeout: 10_000 },
    );
  });

  it.each([
    [1, "denied"],
    [2, "unavailable"],
    [3, "timeout"],
  ] as const)("maps browser error %i to %s", async (code, reason) => {
    const getCurrentPosition = vi.fn(
      (_success: PositionCallback, error: PositionErrorCallback) => {
        error({
          code,
          message: "geolocation error",
          PERMISSION_DENIED: 1,
          POSITION_UNAVAILABLE: 2,
          TIMEOUT: 3,
        } as GeolocationPositionError);
      },
    );

    await expect(
      requestDeviceLocation({ getCurrentPosition }),
    ).rejects.toMatchObject({ reason });
  });

  it("rejects low-accuracy readings and unavailable geolocation", async () => {
    const getCurrentPosition = vi.fn((success: PositionCallback) => {
      success(position(6.52, 3.38, MAX_LOCATION_ACCURACY_METERS + 1));
    });

    await expect(requestDeviceLocation({ getCurrentPosition })).rejects.toEqual(
      new DeviceLocationError("low-accuracy"),
    );
    await expect(requestDeviceLocation(null)).rejects.toEqual(
      new DeviceLocationError("unsupported"),
    );
  });
});
