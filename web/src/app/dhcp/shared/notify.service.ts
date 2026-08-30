import { Injectable, inject } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { MatSnackBar } from '@angular/material/snack-bar';

import { ApiErrorEnvelope } from '../../shared/models';

/** Thin `MatSnackBar` wrapper scoped to the DHCP feature — extracts the
 * CONTRACTS.md Part 2 error envelope's `message` for a readable toast
 * instead of a raw HTTP status line. */
@Injectable({ providedIn: 'root' })
export class NotifyService {
  private readonly snackBar = inject(MatSnackBar);

  success(message: string): void {
    this.snackBar.open(message, undefined, { duration: 3000 });
  }

  error(err: unknown, fallback = 'Something went wrong.'): void {
    this.snackBar.open(this.describe(err, fallback), 'Dismiss', { duration: 6000 });
  }

  describe(err: unknown, fallback = 'Something went wrong.'): string {
    if (err instanceof HttpErrorResponse) {
      const body = err.error as ApiErrorEnvelope | undefined;
      if (body?.error?.message) return body.error.message;
      if (err.status === 0) return 'Network error — server unreachable.';
    }
    return fallback;
  }
}
