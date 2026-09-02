import { HttpClient } from '@angular/common/http';
import { Injectable, computed, signal } from '@angular/core';
import { Observable, finalize, map, shareReplay, tap, throwError } from 'rxjs';

import {
  LoginRequest,
  LoginResponse,
  MessageResponse,
  RefreshRequest,
  RefreshResponse,
  UserRole,
} from '../shared/models';

const STORAGE_KEY = 'gdhcp_session';

/** Claims we optimistically read out of the JWT payload for display purposes.
 * This is NOT a security check — the token is still validated server-side on
 * every request; we just decode it locally to know who's logged in without a
 * dedicated `/auth/me` endpoint (none exists in docs/api/endpoints.md). */
interface JwtClaims {
  sub?: string;
  username?: string;
  role?: UserRole;
  exp?: number;
}

export interface Session {
  token: string;
  /** Set from the login/refresh response. Null only for a session stored by
   * a build that predates refresh support, which simply cannot renew. */
  refreshToken: string | null;
  expiresAt: string;
  username: string | null;
  role: UserRole | null;
}

function decodeJwtClaims(token: string): JwtClaims {
  try {
    const payload = token.split('.')[1];
    if (!payload) return {};
    const normalized = payload.replace(/-/g, '+').replace(/_/g, '/');
    const padded = normalized.padEnd(normalized.length + ((4 - (normalized.length % 4)) % 4), '=');
    return JSON.parse(atob(padded)) as JwtClaims;
  } catch {
    return {};
  }
}

function loadSession(): Session | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const session = JSON.parse(raw) as Session;
    if (!session.token) return null;

    // An expired access token is not on its own a reason to throw the
    // session away: if a refresh token came with it, the interceptor can
    // trade it for a new one on the first request. Only a session with
    // nothing left to renew is discarded here, which is what a session
    // stored before refresh support looks like.
    const expired = new Date(session.expiresAt).getTime() <= Date.now();
    if (expired && !session.refreshToken) return null;

    return { ...session, refreshToken: session.refreshToken ?? null };
  } catch {
    return null;
  }
}

@Injectable({ providedIn: 'root' })
export class AuthService {
  private readonly sessionSignal = signal<Session | null>(loadSession());

  readonly session = this.sessionSignal.asReadonly();
  readonly isAuthenticated = computed(() => this.sessionSignal() !== null);
  readonly username = computed(() => this.sessionSignal()?.username ?? null);
  readonly role = computed(() => this.sessionSignal()?.role ?? null);

  /** The single in-flight refresh, shared by every caller; see refresh(). */
  private refreshInFlight: Observable<Session> | null = null;

  constructor(private readonly http: HttpClient) {}

  token(): string | null {
    return this.sessionSignal()?.token ?? null;
  }

  login(credentials: LoginRequest): Observable<LoginResponse> {
    return this.http.post<LoginResponse>('/api/v1/auth/login', credentials).pipe(
      tap((res) => this.persist(this.sessionFrom(res, credentials.username))),
    );
  }

  /** Trades the stored refresh token for a fresh access token.
   *
   * Concurrent callers share one request. Several in-flight requests can 401
   * in the same instant, and the server rotates the refresh token on every
   * use — so firing one refresh per failure would invalidate the token the
   * other calls are still holding and log the user out for the exact reason
   * this method exists to prevent. */
  refresh(): Observable<Session> {
    if (this.refreshInFlight) return this.refreshInFlight;

    const refreshToken = this.sessionSignal()?.refreshToken;
    if (!refreshToken) {
      return throwError(() => new Error('no refresh token available'));
    }

    const body: RefreshRequest = { refresh_token: refreshToken };
    this.refreshInFlight = this.http.post<RefreshResponse>('/api/v1/auth/refresh', body).pipe(
      map((res) => this.sessionFrom(res, this.sessionSignal()?.username ?? null)),
      tap((session) => this.persist(session)),
      finalize(() => {
        this.refreshInFlight = null;
      }),
      shareReplay({ bufferSize: 1, refCount: false }),
    );
    return this.refreshInFlight;
  }

  /** Builds a Session from a login or refresh response. The refresh token
   * falls back to the one already held: the field is `omitempty` server-side,
   * so an empty one in the response means "unchanged", not "revoked". */
  private sessionFrom(res: LoginResponse, fallbackUsername: string | null): Session {
    const claims = decodeJwtClaims(res.token);
    return {
      token: res.token,
      refreshToken: res.refresh_token ?? this.sessionSignal()?.refreshToken ?? null,
      expiresAt: res.expires_at,
      username: claims.username ?? claims.sub ?? fallbackUsername,
      role: claims.role ?? null,
    };
  }

  /** Calls the server-side revoke endpoint, then clears local session state
   * regardless of the outcome so the UI never gets stuck signed-in. */
  logout(): Observable<MessageResponse> {
    const result$ = this.http.post<MessageResponse>('/api/v1/auth/logout', {});
    result$.subscribe({ complete: () => this.clear(), error: () => this.clear() });
    return result$;
  }

  /** Local-only sign-out (used by the 401 interceptor path, where calling
   * `/auth/logout` with an already-invalid token would just 401 again). */
  clear(): void {
    this.sessionSignal.set(null);
    this.refreshInFlight = null;
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch {
      // Storage unavailable (private browsing, etc.) — session state still
      // clears in-memory, which is what matters for the running app.
    }
  }

  private persist(session: Session): void {
    this.sessionSignal.set(session);
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(session));
    } catch {
      // Non-fatal: session still works for the current tab via the signal.
    }
  }
}
