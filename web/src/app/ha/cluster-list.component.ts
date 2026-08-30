import { ChangeDetectionStrategy, Component, OnInit, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { catchError, forkJoin, map, of, switchMap } from 'rxjs';

import { ApiService } from '../shared/api.service';
import { AuthService } from '../auth/auth.service';
import { PageHeaderComponent } from '../shared/components/page-header/page-header.component';
import { StatusDotComponent } from '../shared/components/status-dot/status-dot.component';
import { UtilizationBarComponent } from '../shared/components/utilization-bar/utilization-bar.component';
import { EmptyStateComponent } from '../shared/components/empty-state/empty-state.component';
import { Cluster, ClusterHealthServer, ClusterState, DHCPScope, EntityStatus, Paginated } from '../shared/models';
import { CreateClusterDialogComponent } from './create-cluster-dialog.component';
import { extractErrorMessage } from './http-error.util';

interface ClusterRow {
  cluster: Cluster;
  state: ClusterState;
  servers: ClusterHealthServer[];
  scopeUsed: number;
  scopeTotal: number;
}

function statusFor(state: ClusterState): EntityStatus {
  switch (state) {
    case 'HEALTHY':
      return 'ok';
    case 'DEGRADED':
      return 'warn';
    case 'DOWN':
      return 'error';
    default:
      return 'unknown';
  }
}

/** `/ha` — cluster list: name, VIP, servers, state, and the aggregate
 * utilization of the scopes each cluster serves. "New cluster" is
 * admin-only per A15 (cluster config is excluded from the operator role). */
@Component({
  selector: 'app-cluster-list',
  standalone: true,
  imports: [
    RouterLink,
    MatButtonModule,
    MatDialogModule,
    MatIconModule,
    MatTableModule,
    MatTooltipModule,
    PageHeaderComponent,
    StatusDotComponent,
    UtilizationBarComponent,
    EmptyStateComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="Clusters">
      @if (isAdmin()) {
        <button mat-flat-button color="primary" (click)="openCreate()">
          <mat-icon>add</mat-icon>
          New cluster
        </button>
      }
    </app-page-header>

    @if (loading()) {
      <p class="hint">Loading clusters…</p>
    } @else if (rows().length === 0) {
      <app-empty-state icon="hub" title="No clusters yet" message="Create a cluster to enable HA DHCP serving across multiple nodes.">
        @if (isAdmin()) {
          <button mat-stroked-button (click)="openCreate()">
            <mat-icon>add</mat-icon>
            New cluster
          </button>
        }
      </app-empty-state>
    } @else {
      <div class="table-wrap gdhcp-card">
        <table mat-table [dataSource]="rows()">
          <ng-container matColumnDef="name">
            <th mat-header-cell *matHeaderCellDef>Name</th>
            <td mat-cell *matCellDef="let row">
              <a [routerLink]="['/ha', 'clusters', row.cluster.id]" class="name-link">{{ row.cluster.name }}</a>
              @if (row.cluster.description) {
                <span class="row-desc">{{ row.cluster.description }}</span>
              }
            </td>
          </ng-container>

          <ng-container matColumnDef="vip">
            <th mat-header-cell *matHeaderCellDef>VIP</th>
            <td mat-cell *matCellDef="let row" class="gdhcp-mono">
              {{ row.cluster.vip_address ?? '—' }}
              @if (row.cluster.vip_interface) {
                <span class="row-desc">on {{ row.cluster.vip_interface }}</span>
              }
            </td>
          </ng-container>

          <ng-container matColumnDef="servers">
            <th mat-header-cell *matHeaderCellDef>Servers</th>
            <td mat-cell *matCellDef="let row">
              @if (row.servers.length === 0) {
                <span class="row-desc">none registered</span>
              } @else {
                <div class="server-chips">
                  @for (server of row.servers; track server.server_id) {
                    <span class="server-chip" [matTooltip]="server.node_name + ' — ' + server.role">
                      <app-status-dot [status]="server.is_healthy ? 'ok' : 'error'" />
                      {{ server.node_name }}
                    </span>
                  }
                </div>
              }
            </td>
          </ng-container>

          <ng-container matColumnDef="state">
            <th mat-header-cell *matHeaderCellDef>State</th>
            <td mat-cell *matCellDef="let row">
              <span class="state-cell">
                <app-status-dot [status]="statusOf(row.state)" [tooltip]="row.state" />
                {{ row.state }}
              </span>
            </td>
          </ng-container>

          <ng-container matColumnDef="utilization">
            <th mat-header-cell *matHeaderCellDef>Scope utilization</th>
            <td mat-cell *matCellDef="let row">
              <app-utilization-bar [used]="row.scopeUsed" [total]="row.scopeTotal" />
            </td>
          </ng-container>

          <tr mat-header-row *matHeaderRowDef="columns"></tr>
          <tr mat-row *matRowDef="let row; columns: columns" [routerLink]="['/ha', 'clusters', row.cluster.id]" class="clickable-row"></tr>
        </table>
      </div>
    }
  `,
  styles: [
    `
      .hint {
        color: var(--text-secondary);
        font-size: 13px;
      }

      .table-wrap {
        padding: 0;
        overflow-x: auto;
      }

      table {
        width: 100%;
        background: transparent;
      }

      .clickable-row {
        cursor: pointer;
      }

      .clickable-row:hover {
        background: var(--hover-bg);
      }

      .name-link {
        color: var(--text-primary);
        font-weight: 500;
        text-decoration: none;
      }

      .name-link:hover {
        text-decoration: underline;
      }

      .row-desc {
        display: block;
        font-size: 11px;
        color: var(--text-secondary);
      }

      .server-chips {
        display: flex;
        flex-wrap: wrap;
        gap: 6px;
      }

      .server-chip {
        display: inline-flex;
        align-items: center;
        gap: 4px;
        font-size: 12px;
        color: var(--text-secondary);
        background: var(--bg-secondary);
        border-radius: 4px;
        padding: 2px 6px;
      }

      .state-cell {
        display: inline-flex;
        align-items: center;
        gap: 6px;
        font-size: 12px;
      }
    `,
  ],
})
export class ClusterListComponent implements OnInit {
  private readonly api = inject(ApiService);
  private readonly auth = inject(AuthService);
  private readonly dialog = inject(MatDialog);
  private readonly snackBar = inject(MatSnackBar);

  readonly columns = ['name', 'vip', 'servers', 'state', 'utilization'];
  readonly rows = signal<ClusterRow[]>([]);
  readonly loading = signal(true);

  readonly isAdmin = () => this.auth.role() === 'admin';

  ngOnInit(): void {
    this.load();
  }

  statusOf(state: ClusterState): EntityStatus {
    return statusFor(state);
  }

  openCreate(): void {
    this.dialog
      .open(CreateClusterDialogComponent, { width: '520px' })
      .afterClosed()
      .subscribe((cluster: Cluster | undefined) => {
        if (cluster) {
          this.snackBar.open(`Cluster "${cluster.name}" created`, 'Dismiss', { duration: 3000 });
          this.load();
        }
      });
  }

  private load(): void {
    this.loading.set(true);
    this.api
      .listClusters({ limit: 100 })
      .pipe(
        switchMap((res) => {
          if (res.items.length === 0) return of([] as ClusterRow[]);
          return forkJoin(res.items.map((cluster) => this.loadRow(cluster)));
        }),
        catchError((err: unknown) => {
          this.snackBar.open(extractErrorMessage(err), 'Dismiss', { duration: 4000 });
          return of([] as ClusterRow[]);
        }),
      )
      .subscribe((rows) => {
        this.rows.set(rows);
        this.loading.set(false);
      });
  }

  private loadRow(cluster: Cluster) {
    return forkJoin({
      health: this.api.clusterHealth(cluster.id).pipe(catchError(() => of(null))),
      scopes: this.api
        .listScopes({ cluster_id: cluster.id, limit: 100 })
        .pipe(catchError(() => of<Paginated<DHCPScope>>({ items: [], total: 0, limit: 0, offset: 0 }))),
    }).pipe(
      switchMap(({ health, scopes }) => {
        const state = health?.cluster_state ?? 'UNKNOWN';
        const servers = health?.servers ?? [];
        if (scopes.items.length === 0) {
          return of<ClusterRow>({ cluster, state, servers, scopeUsed: 0, scopeTotal: 0 });
        }
        return forkJoin(
          scopes.items.map((scope) => this.api.scopeUtilization(scope.id).pipe(catchError(() => of({ total: 0, active: 0, reserved: 0, pct: 0 })))),
        ).pipe(
          map((utils) => {
            const scopeUsed = utils.reduce((sum, u) => sum + u.active + u.reserved, 0);
            const scopeTotal = utils.reduce((sum, u) => sum + u.total, 0);
            return { cluster, state, servers, scopeUsed, scopeTotal } satisfies ClusterRow;
          }),
        );
      }),
    );
  }
}
