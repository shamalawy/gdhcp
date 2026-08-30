import { ChangeDetectionStrategy, Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Router, RouterLink } from '@angular/router';
import { forkJoin, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { StatusDotComponent } from '../../shared/components/status-dot/status-dot.component';
import { UtilizationBarComponent } from '../../shared/components/utilization-bar/utilization-bar.component';
import { ApiService } from '../../shared/api.service';
import { Cluster, DHCPScope, EntityStatus, ListParams, VRF } from '../../shared/models';
import { NotifyService } from '../shared/notify.service';

interface ScopeRow {
  scope: DHCPScope;
  utilization: { used: number; total: number } | null;
  status: EntityStatus;
}

@Component({
  selector: 'app-scopes-list',
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    EmptyStateComponent,
    PageHeaderComponent,
    StatusDotComponent,
    UtilizationBarComponent,
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatProgressBarModule,
    MatSelectModule,
    MatSlideToggleModule,
    MatTooltipModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="DHCP Scopes">
      <a mat-flat-button color="primary" routerLink="/dhcp/scopes/new">
        <mat-icon>add</mat-icon> New scope
      </a>
    </app-page-header>

    <div class="filters">
      <mat-form-field appearance="outline" subscriptSizing="dynamic">
        <mat-label>VRF</mat-label>
        <mat-select [ngModel]="vrfFilter()" (ngModelChange)="vrfFilter.set($event); load()">
          <mat-option [value]="null">All VRFs</mat-option>
          @for (vrf of vrfs(); track vrf.id) {
            <mat-option [value]="vrf.id">{{ vrf.name }}</mat-option>
          }
        </mat-select>
      </mat-form-field>

      <mat-form-field appearance="outline" subscriptSizing="dynamic">
        <mat-label>Cluster</mat-label>
        <mat-select [ngModel]="clusterFilter()" (ngModelChange)="clusterFilter.set($event); load()">
          <mat-option [value]="null">All clusters</mat-option>
          @for (cluster of clusters(); track cluster.id) {
            <mat-option [value]="cluster.id">{{ cluster.name }}</mat-option>
          }
        </mat-select>
      </mat-form-field>
    </div>

    @if (loading()) {
      <mat-progress-bar mode="indeterminate" />
    }

    @if (!loading() && rows().length === 0) {
      <app-empty-state icon="dns" title="No scopes match these filters" message="Create a scope to start serving DHCP on a subnet.">
        <a mat-stroked-button routerLink="/dhcp/scopes/new">Create scope</a>
      </app-empty-state>
    } @else if (rows().length > 0) {
      <div class="table-wrap">
        <table class="gdhcp-table">
          <thead>
            <tr>
              <th></th>
              <th>Name</th>
              <th>Network</th>
              <th>VRF</th>
              <th>Cluster</th>
              <th>Utilization</th>
              <th>Enabled</th>
              <th>Tags</th>
            </tr>
          </thead>
          <tbody>
            @for (row of rows(); track row.scope.id) {
              <tr class="clickable" (click)="open(row.scope.id)">
                <td (click)="$event.stopPropagation()">
                  <app-status-dot [status]="row.status" />
                </td>
                <td>{{ row.scope.name }}</td>
                <td class="gdhcp-mono">{{ row.scope.network }}</td>
                <td>{{ vrfName(row.scope.vrf_id) }}</td>
                <td>{{ clusterName(row.scope.cluster_id) }}</td>
                <td class="util-cell">
                  @if (row.utilization) {
                    <app-utilization-bar [used]="row.utilization.used" [total]="row.utilization.total" [compact]="true" />
                  } @else {
                    <span class="gdhcp-muted">—</span>
                  }
                </td>
                <td (click)="$event.stopPropagation()">
                  <mat-slide-toggle
                    [ngModel]="row.scope.is_enabled"
                    (ngModelChange)="toggleEnabled(row.scope, $event)"
                    [attr.aria-label]="'Enable ' + row.scope.name"
                  />
                </td>
                <td>
                  <span
                    class="gdhcp-muted"
                    matTooltip="Per-entity tag listing isn't available from the API yet (see notes_for_integration) — manage tags from the scope's Settings tab."
                    >—</span
                  >
                </td>
              </tr>
            }
          </tbody>
        </table>
      </div>
    }
  `,
  styles: [
    `
      .filters {
        display: flex;
        gap: 12px;
        margin-bottom: 16px;
        max-width: 480px;
      }
      .filters mat-form-field {
        flex: 1;
      }
      tr.clickable {
        cursor: pointer;
      }
      tr.clickable:hover {
        background: var(--hover-bg);
      }
      .util-cell {
        min-width: 140px;
      }
    `,
  ],
})
export class ScopesListComponent {
  private readonly api = inject(ApiService);
  private readonly router = inject(Router);
  private readonly notify = inject(NotifyService);

  readonly loading = signal(true);
  readonly vrfs = signal<VRF[]>([]);
  readonly clusters = signal<Cluster[]>([]);
  readonly rows = signal<ScopeRow[]>([]);

  readonly vrfFilter = signal<string | null>(null);
  readonly clusterFilter = signal<string | null>(null);

  private readonly vrfById = computed(() => new Map(this.vrfs().map((v) => [v.id, v.name])));
  private readonly clusterById = computed(() => new Map(this.clusters().map((c) => [c.id, c.name])));

  constructor() {
    this.api.listVRFs({ limit: 200 }).subscribe((res) => this.vrfs.set(res.items));
    this.api.listClusters({ limit: 200 }).subscribe((res) => this.clusters.set(res.items));
    this.load();
  }

  vrfName(id: string): string {
    return this.vrfById().get(id) ?? id;
  }

  clusterName(id: string): string {
    return this.clusterById().get(id) ?? id;
  }

  load(): void {
    this.loading.set(true);
    const params: ListParams & { vrf_id?: string; cluster_id?: string } = { limit: 200 };
    if (this.vrfFilter()) params.vrf_id = this.vrfFilter()!;
    if (this.clusterFilter()) params.cluster_id = this.clusterFilter()!;

    this.api.listScopes(params).subscribe({
      next: (page) => this.hydrateRows(page.items),
      error: (err) => {
        this.loading.set(false);
        this.notify.error(err, 'Could not load scopes.');
      },
    });
  }

  /** Per-row utilization + stats are fetched in parallel (no bulk endpoint
   * exists for a scope list rollup — see notes_for_integration) and merged
   * into `rows`; a failed per-row fetch degrades that row gracefully rather
   * than failing the whole list. */
  private hydrateRows(scopes: DHCPScope[]): void {
    if (scopes.length === 0) {
      this.rows.set([]);
      this.loading.set(false);
      return;
    }

    const requests = scopes.map((scope) =>
      forkJoin({
        utilization: this.api.scopeUtilization(scope.id).pipe(catchError(() => of(null))),
        stats: this.api.scopeStats(scope.id).pipe(catchError(() => of(null))),
      }),
    );

    forkJoin(requests).subscribe((results) => {
      this.rows.set(
        scopes.map((scope, i) => {
          const { utilization, stats } = results[i];
          const status: EntityStatus = !scope.is_enabled
            ? 'unknown'
            : stats && (stats.nak_count > 0 || stats.no_leases_count > 0)
              ? 'error'
              : utilization && utilization.pct >= 80
                ? 'warn'
                : 'ok';
          return {
            scope,
            utilization: utilization ? { used: utilization.active + utilization.reserved, total: utilization.total } : null,
            status,
          };
        }),
      );
      this.loading.set(false);
    });
  }

  toggleEnabled(scope: DHCPScope, enabled: boolean): void {
    this.api.updateScope(scope.id, { is_enabled: enabled }).subscribe({
      next: () => this.load(),
      error: (err) => this.notify.error(err, 'Could not update scope.'),
    });
  }

  open(scopeId: string): void {
    this.router.navigate(['/dhcp/scopes', scopeId]);
  }
}
