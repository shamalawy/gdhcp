import { HttpClient } from '@angular/common/http';
import { Injectable, computed, signal } from '@angular/core';
import { Observable, tap } from 'rxjs';

import { LoginRequest, LoginResponse, MessageResponse, UserRole } from '../shared/models';

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
    if (!session.token || new Date(session.expiresAt).getTime() <= Date.now()) {
      return null;
    }
    return session;
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

  constructor(private readonly http: HttpClient) {}

  token(): string | null {
    return this.sessionSignal()?.token ?? null;
  }

  login(credentials: LoginRequest): Observable<LoginResponse> {
    return this.http.post<LoginResponse>('/api/v1/auth/login', credentials).pipe(
      tap((res) => {
        const claims = decodeJwtClaims(res.token);
        const session: Session = {
          token: res.token,
          expiresAt: res.expires_at,
          username: claims.username ?? claims.sub ?? credentials.username,
          role: claims.role ?? null,
        };
        this.persist(session);
      }),
    );
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
