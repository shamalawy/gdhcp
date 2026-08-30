import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { MatSnackBar } from '@angular/material/snack-bar';

import { AuthService } from '../auth/auth.service';

/** Admin section guard. Per A15 the whole `/admin` area (users, tokens,
 * audit, settings) is admin-only — operator and viewer roles get bounced to
 * the dashboard with an explanation. This is a UX convenience only: every
 * endpoint underneath is independently enforced server-side. */
export const adminGuard: CanActivateFn = () => {
  const auth = inject(AuthService);
  const router = inject(Router);
  const snackBar = inject(MatSnackBar);

  if (auth.role() === 'admin') {
    return true;
  }

  snackBar.open('The admin area is restricted to admin accounts.', 'Dismiss', { duration: 4000 });
  return router.createUrlTree(['/dashboard']);
};
