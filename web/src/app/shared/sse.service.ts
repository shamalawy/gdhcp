import { Injectable, OnDestroy, signal } from '@angular/core';
import { Subject } from 'rxjs';

import { AuthService } from '../auth/auth.service';
import { ClusterHealth, DashboardStats, SSEClusterEvent, SSEEvent, SSELeaseEvent, SSEStatsEvent } from './models';

/**
 * Wraps the `GET /api/v1/events` Server-Sent Events endpoint (A12): emits
 * named `stats`, `leases`, and `cluster` events. `EventSource` cannot set an
 * `Authorization` header, so the session token is passed as an
 * `?access_token=` query parameter instead — the auth middleware accepts
 * this fallback for exactly this one path (see middleware.go's
 * authenticator.Middleware).
 *
 * Polling fallback is acceptable per A12; callers that want a "poll if SSE
 * unavailable" strategy can watch `connected()` and fall back to
 * `ApiService.dashboardStats()` on an interval when it stays false.
 */
@Injectable({ providedIn: 'root' })
export class SseService implements OnDestroy {
  private eventSource: EventSource | null = null;

  private readonly connectedSignal = signal(false);
  private readonly latestStatsSignal = signal<DashboardStats | null>(null);
  private readonly latestClusterSignal = signal<ClusterHealth | null>(null);

  private readonly messages = new Subject<SSEEvent>();

  readonly connected = this.connectedSignal.asReadonly();
  readonly latestStats = this.latestStatsSignal.asReadonly();
  readonly latestCluster = this.latestClusterSignal.asReadonly();

  /** Raw event stream for callers that want every event as it arrives
   * (e.g. a lease churn ticker), rather than just the latest snapshot. */
  readonly message$ = this.messages.asObservable();

  constructor(private readonly auth: AuthService) {}

  connect(): void {
    if (this.eventSource) return;

    const token = this.auth.token();
    const url = token ? `/api/v1/events?access_token=${encodeURIComponent(token)}` : '/api/v1/events';

    const source = new EventSource(url);
    this.eventSource = source;

    source.onopen = () => this.connectedSignal.set(true);
    source.onerror = () => this.connectedSignal.set(false);

    source.addEventListener('stats', (evt) => {
      const data = this.parse<DashboardStats>((evt as MessageEvent).data);
      if (data === null) return;
      this.latestStatsSignal.set(data);
      this.messages.next({ type: 'stats', data } satisfies SSEStatsEvent);
    });

    source.addEventListener('leases', (evt) => {
      const data = this.parse<SSELeaseEvent['data']>((evt as MessageEvent).data);
      if (data === null) return;
      this.messages.next({ type: 'leases', data } satisfies SSELeaseEvent);
    });

    source.addEventListener('cluster', (evt) => {
      const data = this.parse<ClusterHealth>((evt as MessageEvent).data);
      if (data === null) return;
      this.latestClusterSignal.set(data);
      this.messages.next({ type: 'cluster', data } satisfies SSEClusterEvent);
    });
  }

  disconnect(): void {
    this.eventSource?.close();
    this.eventSource = null;
    this.connectedSignal.set(false);
  }

  ngOnDestroy(): void {
    this.disconnect();
  }

  private parse<T>(raw: string): T | null {
    try {
      return JSON.parse(raw) as T;
    } catch {
      return null;
    }
  }
}
