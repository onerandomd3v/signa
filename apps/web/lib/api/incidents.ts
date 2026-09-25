import { listIncidents } from "./generated";
import type { PublicIncident } from "./generated";
import { createClient } from "./generated/client";
import { getApiBaseUrl } from "./reports";

export async function fetchPublicIncidents(
  fetchImplementation: typeof fetch = globalThis.fetch,
): Promise<PublicIncident[]> {
  const result = await listIncidents({
    client: createClient({
      baseUrl: getApiBaseUrl(),
      fetch: fetchImplementation,
    }),
  });
  if (result.data) return result.data;
  throw new Error(`Incident request failed (${result.response?.status ?? 0})`);
}

export function getPublicMapStyleUrl(): string | null {
  const configured = process.env.NEXT_PUBLIC_MAP_STYLE_URL?.trim();
  if (!configured) return null;

  try {
    const url = new URL(configured);
    if (
      (url.protocol === "https:" || url.protocol === "http:") &&
      url.host &&
      !url.username &&
      !url.password
    ) {
      return configured;
    }
  } catch {
    // An invalid public style URL is treated as an unavailable map.
  }
  return null;
}
