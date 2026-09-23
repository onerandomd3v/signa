import type { DeviceLocation } from "../../lib/api/generated";

export const MAX_LOCATION_ACCURACY_METERS = 1_000;

const POSITION_ERROR = {
  permissionDenied: 1,
  unavailable: 2,
  timeout: 3,
} as const;

export type LocationFailure =
  | "denied"
  | "unavailable"
  | "timeout"
  | "unsupported"
  | "low-accuracy"
  | "unknown";

export class DeviceLocationError extends Error {
  constructor(public readonly reason: LocationFailure) {
    super(reason);
    this.name = "DeviceLocationError";
  }
}

export async function requestDeviceLocation(
  geolocation: Pick<
    Geolocation,
    "getCurrentPosition"
  > | null = typeof navigator === "undefined" ? null : navigator.geolocation,
): Promise<DeviceLocation> {
  if (!geolocation) {
    throw new DeviceLocationError("unsupported");
  }

  const position = await new Promise<GeolocationPosition>((resolve, reject) => {
    geolocation.getCurrentPosition(
      resolve,
      (error) => {
        const reason: LocationFailure =
          error.code === POSITION_ERROR.permissionDenied
            ? "denied"
            : error.code === POSITION_ERROR.unavailable
              ? "unavailable"
              : error.code === POSITION_ERROR.timeout
                ? "timeout"
                : "unknown";
        reject(new DeviceLocationError(reason));
      },
      { enableHighAccuracy: true, maximumAge: 0, timeout: 10_000 },
    );
  });

  const { accuracy, latitude, longitude } = position.coords;
  if (
    !Number.isFinite(latitude) ||
    latitude < -90 ||
    latitude > 90 ||
    !Number.isFinite(longitude) ||
    longitude < -180 ||
    longitude > 180 ||
    !Number.isFinite(accuracy) ||
    accuracy < 0
  ) {
    throw new DeviceLocationError("unknown");
  }
  if (accuracy > MAX_LOCATION_ACCURACY_METERS) {
    throw new DeviceLocationError("low-accuracy");
  }

  return { latitude, longitude, accuracy };
}
