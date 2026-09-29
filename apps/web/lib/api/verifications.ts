import {
  getVerificationRequest,
  listVerificationRequests,
  submitVerificationResponse,
} from "./generated";
import type {
  VerificationRequestView,
  VerificationResponse,
  VerificationResponseInput,
} from "./generated";
import { createClient } from "./generated/client";
import { getApiBaseUrl } from "./reports";

export type VerificationApiResult<T> =
  { ok: true; data: T } | { ok: false; status: number };

function createVerificationClient(fetchImplementation: typeof fetch) {
  return createClient({
    baseUrl: getApiBaseUrl(),
    credentials: "include",
    fetch: fetchImplementation,
    parseAs: "json",
  });
}

export async function fetchVerificationRequests(
  signal?: AbortSignal,
  fetchImplementation: typeof fetch = globalThis.fetch,
): Promise<VerificationApiResult<VerificationRequestView[]>> {
  try {
    const result = await listVerificationRequests({
      signal,
      client: createVerificationClient(fetchImplementation),
    });
    if (result.response?.ok && Array.isArray(result.data)) {
      return { ok: true, data: result.data };
    }
    if (result.response?.ok) return { ok: false, status: 0 };
    return { ok: false, status: result.response?.status ?? 0 };
  } catch {
    return { ok: false, status: 0 };
  }
}

export async function fetchVerificationRequest(
  requestId: string,
  signal?: AbortSignal,
  fetchImplementation: typeof fetch = globalThis.fetch,
): Promise<VerificationApiResult<VerificationRequestView>> {
  try {
    const result = await getVerificationRequest({
      path: { request_id: requestId },
      signal,
      client: createVerificationClient(fetchImplementation),
    });
    if (result.response?.ok && result.data) {
      return { ok: true, data: result.data };
    }
    if (result.response?.ok) return { ok: false, status: 0 };
    return { ok: false, status: result.response?.status ?? 0 };
  } catch {
    return { ok: false, status: 0 };
  }
}

export async function sendVerificationResponse(
  requestId: string,
  body: VerificationResponseInput,
  idempotencyKey: string,
  fetchImplementation: typeof fetch = globalThis.fetch,
): Promise<VerificationApiResult<VerificationResponse>> {
  try {
    const result = await submitVerificationResponse({
      body,
      path: { request_id: requestId },
      headers: { "Idempotency-Key": idempotencyKey },
      client: createVerificationClient(fetchImplementation),
    });
    if (result.response?.ok && result.data) {
      return { ok: true, data: result.data };
    }
    if (result.response?.ok) return { ok: false, status: 0 };
    return { ok: false, status: result.response?.status ?? 0 };
  } catch {
    return { ok: false, status: 0 };
  }
}
