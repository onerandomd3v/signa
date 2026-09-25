import { getCurrentSession, type CurrentSession } from "./generated";
import { createClient } from "./generated/client";
import { getApiBaseUrl } from "./reports";

export async function fetchCurrentSession(
  fetchImplementation: typeof fetch = globalThis.fetch,
): Promise<{ data?: CurrentSession; error?: unknown; response?: Response }> {
  const client = createClient({
    baseUrl: getApiBaseUrl(),
    credentials: "include",
    fetch: fetchImplementation,
  });
  return getCurrentSession({ client });
}
