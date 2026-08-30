import { ChangeDetectionStrategy, Component, DestroyRef, OnDestroy, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatSnackBar } from '@angular/material/snack-bar';
import { catchError, forkJoin, interval, map, of, switchMap } from 'rxjs';

import { ApiService } from '../shared/api.service';
import { SseService } from '../shared/sse.service';
import { PageHeaderComponent } from '../shared/components/page-header/page-header.component';
import { UtilizationBarComponent } from '../shared/components/utilization-bar/utilization-bar.component';
import { StatusDotComponent } from '../shared/components/status-dot/status-dot.component';
import { StatCardComponent } from './stat-card/stat-card.component';
import { UtilizationChartComponent, ChartRange } from './utilization-chart/utilization-chart.component';
import { TopScopesComponent, TopScopeEntry } from './top-scopes/top-scopes.component';
import { DhcpActivityComponent } from './dhcp-activity/dhcp-activity.component';
import { ClusterHealthStripComponent, ClusterHealthStripEntry } from './cluster-health-strip/cluster-health-strip.component';
import { ChartPoint, ClusterHealth, DHCPStats, DashboardStats, EntityStatus } from '../shared/models';

const POLL_FALLBACK_MS = 30_000;
const TOP_SCOPES_REFRESH_MS = 60_000;

function chartDays(range: ChartRange): number {
  return range === '24h' ? 1 : range === '7d' ? 7 : 30;
}

/** `/dashboard` — the operator's landing page: top-row stat cards, a hand-
 * rolled utilization trend chart, top scopes by utilization, the DHCP
 * transaction activity panel, and a cluster health strip. Live-updates via
 * `SseService` (`stats` + `cluster` events); falls back to 30s polling of
 * `ApiService.dashboardStats()` / `dashboardDhcpStats()` / cluster health
 * while the SSE connection is down, per A12. */
@Component({
  selector: 'app-dashboard',
  standalone: true,
  imports: [
    PageHeaderComponent,
    StatCardComponent,
    UtilizationBarComponent,
    StatusDotComponent,
    UtilizationChartComponent,
    TopScopesComponent,
    DhcpActivityComponent,
    ClusterHealthStripComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="Dashboard" />

    <div class="stat-row">
      <app-stat-card icon="lan" label="Total IPs managed" [value]="formatNumber(stats()?.total_addresses ?? null)" [loading]="statsLoading()">
        @if (stats(); as s) {
          <span class="mini-note">{{ formatNumber(usedAddresses(s)) }} used · {{ formatNumber(s.total_available_addresses) }} available</span>
        }
      </app-stat-card>

      <app-stat-card icon="devices" label="Active leases" [value]="formatNumber(activeLeases())" [loading]="activeLeasesLoading()" />

      <app-stat-card
        icon="donut_large"
        label="Overall utilization"
        [value]="stats() ? stats()!.overall_utilization_percentage.toFixed(1) + '%' : '—'"
        [loading]="statsLoading()"
        [alert]="(stats()?.overall_utilization_percentage ?? 0) >= highUtilizationAlertPct"
      >
        @if (stats(); as s) {
          <app-utilization-bar [used]="usedAddresses(s)" [total]="s.total_addresses" [compact]="true" />
          @if (s.overall_utilization_percentage >= highUtilizationAlertPct) {
            <span class="mini-note mini-note-alert">{{ s.overall_utilization_percentage.toFixed(1) }}% utilized</span>
          }
        }
      </app-stat-card>

      <app-stat-card icon="dns" label="Scopes" [value]="formatNumber(stats()?.total_scopes ?? null)" [loading]="statsLoading()" />

      <app-stat-card
        icon="hub"
        label="Clusters healthy"
        [value]="clustersTotal() > 0 ? clustersHealthy() + ' / ' + clustersTotal() : '—'"
        [loading]="clustersLoading()"
        [alert]="clustersTotal() > 0 && clustersHealthy() < clustersTotal()"
      >
        @if (clustersTotal() > 0) {
          <app-status-dot [status]="clusterFleetStatus()" />
        }
      </app-stat-card>
    </div>

    <div class="main-grid">
      <app-utilization-chart [points]="chartPoints()" [loading]="chartLoading()" [range]="chartRange()" (rangeChange)="onRangeChange($event)" />
      <app-dhcp-activity [stats]="dhcpStats()" [loading]="dhcpStatsLoading()" />
    </div>

    <div class="main-grid">
      <app-top-scopes [scopes]="topScopes()" [loading]="topScopesLoading()" />
      <app-cluster-health-strip [clusters]="clusterEntries()" [loading]="clustersLoading()" />
    </div>
  `,
  styles: [
    `
      :host {
        display: block;
      }

      .stat-row {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
        gap: 12px;
        margin-bottom: 16px;
      }

      .main-grid {
        display: grid;
        grid-template-columns: 2fr 1fr;
        gap: 16px;
        margin-bottom: 16px;
      }

      @media (max-width: 960px) {
        .main-grid {
          grid-template-columns: 1fr;
        }
      }

      .mini-note {
        font-size: 11px;
        color: var(--text-secondary);
      }

      .mini-note-alert {
        color: var(--status-error);
        font-weight: 600;
      }
    `,
  ],
})
export class DashboardComponent implements OnInit, OnDestroy {
  private readonly api = inject(ApiService);
  private readonly sse = inject(SseService);
  private readonly snackBar = inject(MatSnackBar);
  private readonly destroyRef = inject(DestroyRef);

  /** Utilization percentage at/above which the overall-utilization stat
   * card flags itself as an alert. DashboardStats carries no server-side
   * "near capacity" signal (no pools_near_capacity field), so this is a
   * plain frontend threshold on the one utilization figure the API does
   * return. */
  readonly highUtilizationAlertPct = 90;

  readonly stats = signal<DashboardStats | null>(null);
  readonly statsLoading = signal(true);

  readonly activeLeases = signal<number | null>(null);
  readonly activeLeasesLoading = signal(true);

  readonly chartPoints = signal<ChartPoint[]>([]);
  readonly chartLoading = signal(true);
  readonly chartRange = signal<ChartRange>('24h');

  readonly dhcpStats = signal<DHCPStats | null>(null);
  readonly dhcpStatsLoading = signal(true);

  readonly topScopes = signal<TopScopeEntry[]>([]);
  readonly topScopesLoading = signal(true);

  readonly clusterEntries = signal<ClusterHealthStripEntry[]>([]);
  readonly clustersLoading = signal(true);

  readonly clustersHealthy = computed(() => this.clusterEntries().filter((c) => c.state === 'HEALTHY').length);
  readonly clustersTotal = computed(() => this.clusterEntries().length);
  readonly clusterFleetStatus = computed<EntityStatus>(() => {
    const total = this.clustersTotal();
    if (total === 0) return 'unknown';
    const healthy = this.clustersHealthy();
    if (healthy === total) return 'ok';
    if (healthy === 0) return 'error';
    return 'warn';
  });

  constructor() {
    // Live updates: the `stats` SSE event carries the same DashboardStats
    // rollup as GET /dashboard/stats, so we can just adopt it directly.
    this.sse.message$.pipe(takeUntilDestroyed()).subscribe((event) => {
      if (event.type === 'stats') {
        this.stats.set(event.data);
        this.statsLoading.set(false);
      } else if (event.type === 'cluster') {
        this.mergeClusterHealth(event.data);
      } else if (event.type === 'leases') {
        // Lease churn is cheap to react to: nudge the active-leases count
        // without a full refetch (re-synced by the 30s poll regardless).
        this.loadActiveLeases();
      }
    });
  }

  ngOnInit(): void {
    this.sse.connect();
    // loadChart/loadDhcpStats need a concrete scope id (see primaryScopeId)
    // that only becomes available once loadStats() resolves, so they're
    // chained off its callback rather than fired in parallel here.
    this.loadStats();
    this.loadActiveLeases();
    this.loadTopScopes();
    this.loadClusters();

    interval(POLL_FALLBACK_MS)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        if (this.sse.connected()) return;
        this.loadStats();
        this.loadActiveLeases();
        this.loadDhcpStats();
        this.loadClusters();
      });

    interval(TOP_SCOPES_REFRESH_MS)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => this.loadTopScopes());
  }

  ngOnDestroy(): void {
    // SseService is a root-scoped singleton — release the EventSource when
    // leaving the dashboard so it isn't left streaming in the background
    // while the user is on another page. Reconnects cleanly on return.
    this.sse.disconnect();
  }

  onRangeChange(range: ChartRange): void {
    this.chartRange.set(range);
    this.loadChart(range);
  }

  formatNumber(n: number | null): string {
    return n === null || n === undefined ? '—' : n.toLocaleString();
  }

  /** DashboardStats has no direct "used addresses" field — derive it as
   * everything not currently available (active leases + reservations). */
  usedAddresses(s: DashboardStats): number {
    return s.total_addresses - s.total_available_addresses;
  }

  private loadStats(): void {
    this.statsLoading.set(true);
    this.api
      .dashboardStats()
      .pipe(
        catchError(() => {
          this.notifyError('Failed to load dashboard stats');
          return of(null);
        }),
      )
      .subscribe((s) => {
        if (s) {
          this.stats.set(s);
          // The utilization-chart and dhcp-stats endpoints have no
          // all-scopes/global rollup (server/internal/api/dashboard/
          // handler.go requires a concrete entity_type+entity_id /
          // scope_id) — drive both off the busiest scope from this same
          // response instead of a nonexistent "global" entity.
          this.loadChart(this.chartRange());
          this.loadDhcpStats();
        }
        this.statsLoading.set(false);
      });
  }

  /** The scope to chart/aggregate DHCP stats for, absent a real all-scopes
   * rollup endpoint: the busiest scope by utilization, per DashboardStats's
   * own `top_utilized_scopes` ranking. */
  private primaryScopeId(): string | null {
    return this.stats()?.top_utilized_scopes?.[0]?.scope_id ?? null;
  }

  private loadActiveLeases(): void {
    this.activeLeasesLoading.set(true);
    this.api
      .listLeases({ limit: 1, state: 'active' })
      .pipe(catchError(() => of(null)))
      .subscribe((res) => {
        if (res) this.activeLeases.set(res.total);
        this.activeLeasesLoading.set(false);
      });
  }

  private loadChart(range: ChartRange): void {
    const scopeId = this.primaryScopeId();
    if (!scopeId) {
      this.chartPoints.set([]);
      this.chartLoading.set(false);
      return;
    }
    this.chartLoading.set(true);
    this.api
      .dashboardUtilizationChart('scope', scopeId, chartDays(range))
      .pipe(catchError(() => of([])))
      .subscribe((points) => {
        this.chartPoints.set(points);
        this.chartLoading.set(false);
      });
  }

  private loadDhcpStats(): void {
    const scopeId = this.primaryScopeId();
    if (!scopeId) {
      this.dhcpStats.set(null);
      this.dhcpStatsLoading.set(false);
      return;
    }
    this.dhcpStatsLoading.set(true);
    this.api
      .dashboardDhcpStats(scopeId)
      .pipe(catchError(() => of(null)))
      .subscribe((s) => {
        this.dhcpStats.set(s);
        this.dhcpStatsLoading.set(false);
      });
  }

  private loadTopScopes(): void {
    this.topScopesLoading.set(true);
    this.api
      .listScopes({ limit: 50 })
      .pipe(
        switchMap((res) => {
          if (res.items.length === 0) return of([] as (TopScopeEntry | null)[]);
          return forkJoin(
            res.items.map((scope) =>
              this.api.scopeUtilization(scope.id).pipe(
                map((u): TopScopeEntry => ({ id: scope.id, name: scope.name, network: scope.network, used: u.active + u.reserved, total: u.total })),
                catchError(() => of(null)),
              ),
            ),
          );
        }),
        map((entries) => entries.filter((e): e is TopScopeEntry => e !== null)),
        map((entries) =>
          entries
            .slice()
            .sort((a, b) => (b.total > 0 ? b.used / b.total : 0) - (a.total > 0 ? a.used / a.total : 0))
            .slice(0, 8),
        ),
        catchError(() => of([] as TopScopeEntry[])),
      )
      .subscribe((entries) => {
        this.topScopes.set(entries);
        this.topScopesLoading.set(false);
      });
  }

  private loadClusters(): void {
    this.clustersLoading.set(true);
    this.api
      .listClusters({ limit: 50 })
      .pipe(
        switchMap((res) => {
          if (res.items.length === 0) return of([] as ClusterHealthStripEntry[]);
          return forkJoin(
            res.items.map((cluster) =>
              this.api.clusterHealth(cluster.id).pipe(
                map((health) => this.toStripEntry(cluster.id, cluster.name, cluster.vip_address, health)),
                catchError(() =>
                  of<ClusterHealthStripEntry>({
                    id: cluster.id,
                    name: cluster.name,
                    vip_address: cluster.vip_address,
                    state: 'UNKNOWN',
                    primary_node: null,
                    servers: [],
                  }),
                ),
              ),
            ),
          );
        }),
        catchError(() => of([] as ClusterHealthStripEntry[])),
      )
      .subscribe((entries) => {
        this.clusterEntries.set(entries);
        this.clustersLoading.set(false);
      });
  }

  /** The `cluster` SSE event carries a single ClusterHealth snapshot (A12
   * doesn't specify which cluster on a multi-cluster deployment — see
   * notes_for_integration). We merge it into the matching row by
   * `cluster_id` when present, and otherwise leave the polled data as-is. */
  private mergeClusterHealth(health: ClusterHealth): void {
    this.clusterEntries.update((entries) => {
      const idx = entries.findIndex((e) => e.id === health.cluster_id);
      if (idx === -1) return entries;
      const next = entries.slice();
      next[idx] = this.toStripEntry(health.cluster_id, entries[idx].name, entries[idx].vip_address, health);
      return next;
    });
  }

  private toStripEntry(
    id: string,
    name: string,
    vip: string | null,
    health: ClusterHealth,
  ): ClusterHealthStripEntry {
    return {
      id,
      name,
      vip_address: vip,
      state: health.cluster_state,
      primary_node: health.servers.find((s) => s.role === 'PRIMARY')?.node_name ?? null,
      servers: health.servers.map((s) => ({ id: s.server_id, node_name: s.node_name, role: s.role, is_healthy: s.is_healthy })),
    };
  }

  private notifyError(message: string): void {
    this.snackBar.open(message, 'Dismiss', { duration: 4000 });
  }
}
