import { HttpErrorResponse } from '@angular/common/http';

import { ApiErrorEnvelope } from '../../shared/models';

/** A friendly, already-unwrapped view of `ApiErrorEnvelope` (CONTRACTS.md
 * Part 6 / models.ts), for inline form errors and snackbar messages. */
export interface FriendlyApiError {
  code: string | null;
  message: string;
  field: string | null;
}

function isErrorEnvelope(body: unknown): body is ApiErrorEnvelope {
  return (
    typeof body === 'object' &&
    body !== null &&
    'error' in body &&
    typeof (body as { error?: unknown }).error === 'object'
  );
}

/** Extracts a display-ready message (and, when present, the offending
 * field/error code) from an HTTP error thrown by `ApiService`. Falls back
 * to a generic message for network failures or malformed error bodies. */
export function extractApiError(
  err: unknown,
  fallback = 'Something went wrong. Please try again.',
): FriendlyApiError {
  if (err instanceof HttpErrorResponse) {
    if (isErrorEnvelope(err.error)) {
      const detail = err.error.error;
      return {
        code: detail.code ?? null,
        message: detail.message || fallback,
        field: detail.details?.field ?? null,
      };
    }
    if (err.status === 0) {
      return { code: null, message: 'Network error — check your connection and try again.', field: null };
    }
    if (err.status === 401 || err.status === 403) {
      return { code: null, message: "You don't have permission to do that.", field: null };
    }
  }
  return { code: null, message: fallback, field: null };
}
