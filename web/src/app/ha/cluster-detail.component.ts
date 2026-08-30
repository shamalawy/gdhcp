import { DatePipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { FormsModule } from '@angular/forms';
import { catchError, interval, of } from 'rxjs';

import { ApiService } from '../shared/api.service';
import { AuthService } from '../auth/auth.service';
import { PageHeaderComponent } from '../shared/components/page-header/page-header.component';
import { StatusDotComponent } from '../shared/components/status-dot/status-dot.component';
import { DescriptionFieldComponent } from '../shared/components/description-field/description-field.component';
import { ConfirmDialogService } from '../shared/components/confirm-dialog/confirm-dialog.component';
import { EmptyStateComponent } from '../shared/components/empty-state/empty-state.component';
import { Cluster, ClusterHealth, ClusterHealthServer, ClusterServer, ClusterState, EntityStatus, Heartbeat } from '../shared/models';
import { EditTimersDialogComponent } from './edit-timers-dialog.component';
import { RegisterServerDialogComponent } from './register-server-dialog.component';
import { extractErrorMessage } from './http-error.util';

const HEALTH_POLL_MS = 5000;
const TICK_MS = 1000;

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

function formatAge(ms: number): string {
  if (ms < 0) ms = 0;
  if (ms < 1000) return 'just now';
  const seconds = Math.floor(ms / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s ago`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m ago`;
}

/** `/ha/clusters/:id` — cluster detail: servers table with a continuously
 * ticking heartbeat age, a heartbeat note (per docs/design/data-model.md
 * `cluster_heartbeat` keeps only the *latest* heartbeat per server — there
 * is no history log to draw a real timeline from, so this renders the
 * current per-server heartbeat rows plus an explanation of the mechanism),
 * a manual-failover control, cluster settings/timers editing, and
 * server-registration help. Destructive/config actions are admin-only per
 * A15 (cluster config is excluded from the operator role). */
@Component({
  selector: 'app-cluster-detail',
  standalone: true,
  imports: [
    DatePipe,
    FormsModule,
    RouterLink,
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatSelectModule,
    MatTableModule,
    MatTooltipModule,
    PageHeaderComponent,
    StatusDotComponent,
    DescriptionFieldComponent,
    EmptyStateComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (cluster(); as cluster) {
      <app-page-header [title]="cluster.name" [breadcrumbs]="[{ label: 'Clusters', link: '/ha' }, { label: cluster.name }]">
        @if (isAdmin()) {
          <button mat-stroked-button (click)="openEdit(cluster)">
            <mat-icon>tune</mat-icon>
            Edit timers
          </button>
        }
      </app-page-header>

      <section class="gdhcp-card overview">
        <div class="overview-row">
          <div class="overview-field">
            <span class="field-label">State</span>
            <span class="field-value">
              <app-status-dot [status]="statusOf(state())" [tooltip]="state()" />
              {{ state() }}
            </span>
          </div>
          <div class="overview-field">
            <span class="field-label">VIP address</span>
            <span class="field-value gdhcp-mono">{{ cluster.vip_address ?? '—' }}</span>
          </div>
          <div class="overview-field">
            <span class="field-label">VIP interface</span>
            <span class="field-value">{{ cluster.vip_interface ?? '—' }}</span>
          </div>
          <div class="overview-field">
            <span class="field-label">Primary</span>
            <span class="field-value">{{ primaryNodeName() ?? '—' }}</span>
          </div>
        </div>
        <div class="description-row">
          <span class="field-label">Description</span>
          <app-description-field [value]="cluster.description" [editable]="isAdmin()" (save)="saveDescription($event)" />
        </div>
      </section>

      <section class="gdhcp-card">
        <div class="section-header">
          <h2>Servers</h2>
          @if (isAdmin()) {
            <button mat-button (click)="openRegister(cluster.id)">
              <mat-icon>add</mat-icon>
              Register server
            </button>
          }
        </div>

        @if (healthLoading() && servers().length === 0) {
          <p class="hint">Loading servers…</p>
        } @else if (servers().length === 0) {
          <app-empty-state icon="dns" title="No servers registered" message="Servers self-register the first time they boot with this cluster's ID configured — see the help below." />
        } @else {
          <table mat-table [dataSource]="servers()">
            <ng-container matColumnDef="node">
              <th mat-header-cell *matHeaderCellDef>Node</th>
              <td mat-cell *matCellDef="let s">
                <app-status-dot [status]="s.is_healthy ? 'ok' : 'error'" [tooltip]="s.is_healthy ? 'Healthy' : 'Heartbeat stale'" />
                {{ s.node_name }}
              </td>
            </ng-container>

            <ng-container matColumnDef="role">
              <th mat-header-cell *matHeaderCellDef>Role</th>
              <td mat-cell *matCellDef="let s">
                <span class="role-badge" [class.role-primary]="s.role === 'PRIMARY'">{{ s.role }}</span>
              </td>
            </ng-container>

            <ng-container matColumnDef="heartbeat">
              <th mat-header-cell *matHeaderCellDef>Heartbeat</th>
              <td mat-cell *matCellDef="let s">{{ liveAge(s) }}</td>
            </ng-container>

            <ng-container matColumnDef="vip">
              <th mat-header-cell *matHeaderCellDef>VIP assigned</th>
              <td mat-cell *matCellDef="let s">
                <mat-icon [class.vip-yes]="s.vip_assigned" [class.vip-no]="!s.vip_assigned">
                  {{ s.vip_assigned ? 'check_circle' : 'remove_circle_outline' }}
                </mat-icon>
              </td>
            </ng-container>

            <ng-container matColumnDef="api">
              <th mat-header-cell *matHeaderCellDef>API address</th>
              <td mat-cell *matCellDef="let s" class="gdhcp-mono">{{ s.api_address }}</td>
            </ng-container>

            <ng-container matColumnDef="actions">
              <th mat-header-cell *matHeaderCellDef></th>
              <td mat-cell *matCellDef="let s">
                @if (isAdmin()) {
                  <button mat-icon-button matTooltip="Deregister server" (click)="deregister(cluster.id, s)">
                    <mat-icon>person_remove</mat-icon>
                  </button>
                }
              </td>
            </ng-container>

            <tr mat-header-row *matHeaderRowDef="serverColumns"></tr>
            <tr mat-row *matRowDef="let row; columns: serverColumns"></tr>
          </table>
        }

        <p class="help-note">
          <mat-icon>info</mat-icon>
          Servers self-register with this endpoint the first time they boot with <code class="gdhcp-mono">ha.enabled: true</code> and
          this cluster's ID in their config — no manual step is normally required. Use "Register server" only to
          pre-provision a node or recover a lost registration.
        </p>
      </section>

      <section class="gdhcp-card">
        <h2>Heartbeat timeline</h2>
        <p class="help-note plain">
          Each server keeps only its most recent heartbeat (no historical log is stored) — the primary writes a new
          one every <strong>{{ cluster.heartbeat_interval_ms }}ms</strong>; a heartbeat older than
          <strong>{{ cluster.heartbeat_ttl_ms }}ms</strong> is treated as dead and the next election
          (every <strong>{{ cluster.election_interval_ms }}ms</strong>) hands PRIMARY to another node.
        </p>
        @if (heartbeats().length > 0) {
          <ul class="heartbeat-list">
            @for (hb of heartbeats(); track hb.id) {
              <li>
                <span class="hb-dot" [class.hb-primary]="hb.role === 'PRIMARY'"></span>
                <span class="hb-node">{{ nodeNameFor(hb.server_id) }}</span>
                <span class="role-badge" [class.role-primary]="hb.role === 'PRIMARY'">{{ hb.role }}</span>
                <span class="hb-time">{{ hb.heartbeat_time | date: 'medium' }}</span>
                <span class="hb-vip" [class.vip-yes]="hb.vip_assigned">{{ hb.vip_assigned ? 'holds VIP' : '' }}</span>
              </li>
            }
          </ul>
        }
      </section>

      @if (isAdmin()) {
        <section class="gdhcp-card">
          <h2>Manual failover</h2>
          <p class="help-note plain">
            Forces an immediate election toward the chosen server. The current primary releases the VIP and the
            target assigns it — expect a brief (2–5s) service interruption while ARP caches update.
          </p>
          <div class="failover-row">
            <mat-form-field appearance="outline" class="failover-select" subscriptSizing="dynamic">
              <mat-label>Target server</mat-label>
              <mat-select [(ngModel)]="failoverTarget" [disabled]="failoverEligible().length === 0">
                @for (s of failoverEligible(); track s.server_id) {
                  <mat-option [value]="s.server_id">{{ s.node_name }} ({{ s.role }})</mat-option>
                }
              </mat-select>
            </mat-form-field>
            <button mat-flat-button color="warn" [disabled]="!failoverTarget" (click)="confirmFailover(cluster)">
              <mat-icon>swap_horiz</mat-icon>
              Trigger failover
            </button>
          </div>
        </section>
      }
    } @else if (loading()) {
      <p class="hint">Loading cluster…</p>
    } @else {
      <app-empty-state icon="error_outline" title="Cluster not found" message="It may have been deleted." />
    }
  `,
  styles: [
    `
      .hint {
        color: var(--text-secondary);
        font-size: 13px;
      }

      .overview {
        display: flex;
        flex-direction: column;
        gap: 16px;
        margin-bottom: 16px;
      }

      .overview-row {
        display: flex;
        flex-wrap: wrap;
        gap: 24px;
      }

      .overview-field {
        display: flex;
        flex-direction: column;
        gap: 2px;
        min-width: 120px;
      }

      .field-label {
        font-size: 11px;
        color: var(--text-secondary);
        text-transform: uppercase;
        letter-spacing: 0.03em;
      }

      .field-value {
        display: flex;
        align-items: center;
        gap: 6px;
        font-size: 14px;
        color: var(--text-primary);
      }

      .description-row {
        display: flex;
        flex-direction: column;
        gap: 4px;
        border-top: 1px solid var(--divider-color);
        padding-top: 12px;
      }

      section.gdhcp-card {
        margin-bottom: 16px;
      }

      .section-header {
        display: flex;
        align-items: center;
        justify-content: space-between;
      }

      h2 {
        margin: 0 0 8px;
        font-size: 15px;
        font-weight: 600;
      }

      table {
        width: 100%;
        background: transparent;
      }

      .role-badge {
        display: inline-block;
        font-size: 11px;
        font-weight: 600;
        padding: 2px 6px;
        border-radius: 4px;
        background: var(--bg-secondary);
        color: var(--text-secondary);
      }

      .role-primary {
        background: var(--active-bg);
        color: var(--primary-color);
      }

      .vip-yes {
        color: var(--status-ok);
      }

      .vip-no {
        color: var(--text-disabled);
      }

      .help-note {
        display: flex;
        align-items: flex-start;
        gap: 8px;
        margin: 12px 0 0;
        font-size: 12px;
        color: var(--text-secondary);
      }

      .help-note.plain {
        margin-top: 0;
        margin-bottom: 12px;
      }

      .help-note mat-icon {
        font-size: 16px;
        width: 16px;
        height: 16px;
        flex: 0 0 auto;
        margin-top: 1px;
      }

      .heartbeat-list {
        list-style: none;
        margin: 0;
        padding: 0;
        display: flex;
        flex-direction: column;
        gap: 8px;
      }

      .heartbeat-list li {
        display: flex;
        align-items: center;
        gap: 10px;
        font-size: 12px;
        padding-left: 4px;
        border-left: 2px solid var(--divider-color);
        padding-bottom: 8px;
      }

      .hb-dot {
        width: 8px;
        height: 8px;
        border-radius: 50%;
        background: var(--text-disabled);
        margin-left: -5px;
        flex: 0 0 auto;
      }

      .hb-dot.hb-primary {
        background: var(--primary-color);
      }

      .hb-node {
        font-weight: 500;
        color: var(--text-primary);
      }

      .hb-time {
        color: var(--text-secondary);
        margin-left: auto;
      }

      .hb-vip {
        color: var(--status-ok);
        font-weight: 600;
      }

      .failover-row {
        display: flex;
        align-items: center;
        gap: 12px;
      }

      .failover-select {
        min-width: 220px;
      }
    `,
  ],
})
export class ClusterDetailComponent implements OnInit {
  private readonly api = inject(ApiService);
  private readonly auth = inject(AuthService);
  private readonly route = inject(ActivatedRoute);
  private readonly dialog = inject(MatDialog);
  private readonly confirmDialog = inject(ConfirmDialogService);
  private readonly snackBar = inject(MatSnackBar);
  private readonly destroyRef = inject(DestroyRef);

  readonly cluster = signal<Cluster | null>(null);
  readonly loading = signal(true);

  readonly health = signal<ClusterHealth | null>(null);
  readonly healthFetchedAt = signal(Date.now());
  readonly healthLoading = signal(true);
  private readonly nowTick = signal(Date.now());

  readonly heartbeats = signal<Heartbeat[]>([]);

  readonly serverColumns = ['node', 'role', 'heartbeat', 'vip', 'api', 'actions'];

  readonly isAdmin = () => this.auth.role() === 'admin';

  readonly state = computed<ClusterState>(() => this.health()?.cluster_state ?? 'UNKNOWN');
  readonly servers = computed<ClusterHealthServer[]>(() => this.health()?.servers ?? []);
  readonly primaryNodeName = computed(() => this.servers().find((s) => s.role === 'PRIMARY')?.node_name ?? null);
  readonly failoverEligible = computed(() => this.servers().filter((s) => s.role !== 'PRIMARY'));

  failoverTarget: string | null = null;

  private clusterId = '';

  ngOnInit(): void {
    this.clusterId = this.route.snapshot.paramMap.get('id') ?? '';
    if (!this.clusterId) return;

    this.loadCluster();
    this.loadHealth();
    this.loadHeartbeats();

    interval(TICK_MS)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => this.nowTick.set(Date.now()));

    interval(HEALTH_POLL_MS)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => this.loadHealth());
  }

  statusOf(state: ClusterState): EntityStatus {
    return statusFor(state);
  }

  liveAge(server: ClusterHealthServer): string {
    const elapsedSinceFetch = this.nowTick() - this.healthFetchedAt();
    return formatAge(server.heartbeat_age_ms + elapsedSinceFetch);
  }

  nodeNameFor(serverId: string): string {
    return this.servers().find((s) => s.server_id === serverId)?.node_name ?? serverId;
  }

  saveDescription(value: string | null): void {
    const cluster = this.cluster();
    if (!cluster) return;
    this.api.updateCluster(cluster.id, { description: value }).subscribe({
      next: (updated) => this.cluster.set(updated),
      error: (err: unknown) => this.snackBar.open(extractErrorMessage(err), 'Dismiss', { duration: 4000 }),
    });
  }

  openEdit(cluster: Cluster): void {
    this.dialog
      .open(EditTimersDialogComponent, { width: '520px', data: { cluster } })
      .afterClosed()
      .subscribe((updated: Cluster | undefined) => {
        if (updated) {
          this.cluster.set(updated);
          this.snackBar.open('Cluster updated', 'Dismiss', { duration: 3000 });
        }
      });
  }

  openRegister(clusterId: string): void {
    this.dialog
      .open(RegisterServerDialogComponent, { width: '420px', data: { clusterId } })
      .afterClosed()
      .subscribe((server: ClusterServer | undefined) => {
        if (server) {
          this.snackBar.open(`Server "${server.node_name}" registered`, 'Dismiss', { duration: 3000 });
          this.loadHealth();
        }
      });
  }

  deregister(clusterId: string, server: ClusterHealthServer): void {
    this.confirmDialog
      .confirm({
        title: 'Deregister server',
        message: `Remove "${server.node_name}" from this cluster? It will stop participating in elections until it re-registers.`,
        confirmText: 'Deregister',
        danger: true,
      })
      .subscribe((confirmed) => {
        if (!confirmed) return;
        this.api.deregisterClusterServer(clusterId, server.server_id).subscribe({
          next: () => {
            this.snackBar.open(`Server "${server.node_name}" deregistered`, 'Dismiss', { duration: 3000 });
            this.loadHealth();
          },
          error: (err: unknown) => this.snackBar.open(extractErrorMessage(err), 'Dismiss', { duration: 4000 }),
        });
      });
  }

  confirmFailover(cluster: Cluster): void {
    const target = this.failoverEligible().find((s) => s.server_id === this.failoverTarget);
    if (!target) return;

    this.confirmDialog
      .confirm({
        title: 'Manual failover',
        message: `Fail over "${cluster.name}" from ${this.primaryNodeName() ?? 'the current primary'} to ${target.node_name}? Expect a brief interruption while the VIP moves.`,
        confirmText: 'Fail over',
        danger: true,
      })
      .subscribe((confirmed) => {
        if (!confirmed) return;
        this.api.failoverCluster(cluster.id, { target_server_id: target.server_id }).subscribe({
          next: (res) => {
            this.snackBar.open(res.message, 'Dismiss', { duration: 4000 });
            this.failoverTarget = null;
            this.loadHealth();
          },
          error: (err: unknown) => this.snackBar.open(extractErrorMessage(err), 'Dismiss', { duration: 4000 }),
        });
      });
  }

  private loadCluster(): void {
    this.loading.set(true);
    this.api
      .getCluster(this.clusterId)
      .pipe(catchError(() => of(null)))
      .subscribe((cluster) => {
        this.cluster.set(cluster);
        this.loading.set(false);
      });
  }

  private loadHealth(): void {
    this.healthLoading.set(true);
    this.api
      .clusterHealth(this.clusterId)
      .pipe(catchError(() => of(null)))
      .subscribe((health) => {
        if (health) {
          this.health.set(health);
          this.healthFetchedAt.set(Date.now());
        }
        this.healthLoading.set(false);
      });
  }

  private loadHeartbeats(): void {
    this.api
      .clusterHeartbeats(this.clusterId)
      .pipe(catchError(() => of([])))
      .subscribe((heartbeats) => this.heartbeats.set(heartbeats));
  }
}
