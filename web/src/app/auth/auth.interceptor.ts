import { HttpErrorResponse, HttpInterceptorFn, HttpRequest } from '@angular/common/http';
import { inject } from '@angular/core';
import { Router } from '@angular/router';
import { catchError, switchMap, throwError } from 'rxjs';

import { AuthService } from './auth.service';

/** Requests that must never trigger a refresh attempt.
 *
 * /auth/refresh is the important one: letting a 401 from the refresh call
 * start another refresh would recurse. login and logout are here because a
 * 401 from either is a real answer — wrong credentials, or a session that is
 * already gone — not an expired access token. */
function isAuthEndpoint(url: string): boolean {
  return (
    url.endsWith('/auth/login') || url.endsWith('/auth/refresh') || url.endsWith('/auth/logout')
  );
}

function withBearer(req: HttpRequest<unknown>, token: string | null): HttpRequest<unknown> {
  return token ? req.clone({ setHeaders: { Authorization: `Bearer ${token}` } }) : req;
}

/** Attaches `Authorization: Bearer <token>` to every request.
 *
 * On a 401 it spends the refresh token once and replays the original request
 * with the new access token, so an access token expiring mid-session is
 * invisible to the user. Only when the refresh itself fails — revoked,
 * expired, or the account deactivated, all of which the server re-checks on
 * refresh — is the session cleared and the user sent to /login. */
export const authInterceptor: HttpInterceptorFn = (req, next) => {
  const auth = inject(AuthService);
  const router = inject(Router);

  return next(withBearer(req, auth.token())).pipe(
    catchError((err: unknown) => {
      const unauthorized = err instanceof HttpErrorResponse && err.status === 401;
      if (!unauthorized || isAuthEndpoint(req.url)) {
        return throwError(() => err);
      }

      return auth.refresh().pipe(
        // The replayed request is a fresh chain, so a 401 on the retry ends
        // here rather than looping back into this handler.
        switchMap((session) => next(withBearer(req, session.token))),
        catchError((refreshErr: unknown) => {
          auth.clear();
          void router.navigate(['/login'], { queryParams: { redirect: router.url } });
          return throwError(() => refreshErr);
        }),
      );
    }),
  );
};
