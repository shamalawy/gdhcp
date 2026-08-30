import { ApiErrorEnvelope } from '../shared/models';

/** Pulls the human-readable message out of the CONTRACTS.md Part 6 error
 * envelope (`{ error: { code, message, ... } }`) that `HttpClient` surfaces
 * as `HttpErrorResponse.error`. Falls back to a generic message for network
 * failures or malformed error bodies. */
export function extractErrorMessage(err: unknown): string {
  if (err && typeof err === 'object' && 'error' in err) {
    const body = (err as { error?: Partial<ApiErrorEnvelope> }).error;
    const message = body?.error?.message;
    if (typeof message === 'string' && message.length > 0) return message;
  }
  return 'Something went wrong. Please try again.';
}
