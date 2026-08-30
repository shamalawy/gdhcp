import { ChangeDetectionStrategy, Component, DestroyRef, effect, inject, input, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';

import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { ApiService } from '../../shared/api.service';
import { SseService } from '../../shared/sse.service';
import { clientIdToHex } from '../shared/ip-utils';
import { DHCPLease, DHCPPool, LeaseBindingState } from '../../shared/models';
import { NotifyService } from '../shared/notify.service';
import {
  ReservationFormDialogComponent,
  ReservationFormDialogData,
  ReservationFormResult,
} from '../shared/reservation-form-dialog.component';

type StateFilter = LeaseBindingState | 'all';

function formatExpiresIn(leaseEnd: string, now: number): string {
  const end = new Date(leaseEnd).getTime();
  const diffMs = end - now;
  if (diffMs <= 0) return 'expired';
  const mins = Math.floor(diffMs / 60000);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ${mins % 60}m`;
  const days = Math.floor(hours / 24);
  return `${days}d ${hours % 24}h`;
}

/** Live leases table — subscribes to the `leases` SSE channel (A12) and
 * refreshes when the churn event reports a change for this pool's scope
 * (the event payload is an array of per-*scope* churn — `{scope_id,
 * active, total, pct}` — not a single lease row, so matching on scope_id
 * and doing a targeted re-fetch is simpler and just as cheap as trying to
 * patch one row from that). Falls back gracefully to the static list if
 * SSE never connects (A12 permits a polling fallback; the periodic
 * "expires in" re-render already keeps the countdowns live either way). */
@Component({
  selector: 'app-pool-leases-tab',
  standalone: true,
  imports: [EmptyStateComponent, MatButtonModule, MatButtonToggleModule, MatIconModule, MatProgressBarModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="header-row">
      <mat-button-toggle-group [value]="stateFilter()" (change)="onFilterChange($event.value)">
        <mat-button-toggle value="all">All</mat-button-toggle>
        <mat-button-toggle value="active">Active</mat-button-toggle>
        <mat-button-toggle value="expired">Expired</mat-button-toggle>
        <mat-button-toggle value="released">Released</mat-button-toggle>
        <mat-button-toggle value="abandoned">Abandoned</mat-button-toggle>
      </mat-button-toggle-group>
      <span class="live-indicator" [class.live]="sse.connected()">
        <mat-icon>{{ sse.connected() ? 'sensors' : 'sensors_off' }}</mat-icon>
        {{ sse.connected() ? 'Live' : 'Polling' }}
      </span>
    </div>

    @if (loading()) {
      <mat-progress-bar mode="indeterminate" />
    }

    @if (!loading() && leases().length === 0) {
      <app-empty-state icon="schedule" title="No leases" message="No leases match this filter yet." />
    } @else if (leases().length > 0) {
      <div class="table-wrap">
        <table class="gdhcp-table">
          <thead>
            <tr>
              <th>IP</th>
              <th>MAC</th>
              <th>Client ID</th>
              <th>Hostname</th>
              <th>State</th>
              <th>Expires in</th>
              <th>Server</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            @for (lease of leases(); track lease.id) {
              <tr>
                <td class="gdhcp-mono">{{ lease.ip_address }}</td>
                <td class="gdhcp-mono">{{ lease.client_mac || '—' }}</td>
                <td class="gdhcp-mono">{{ formatClientId(lease.client_id) }}</td>
                <td>{{ lease.client_hostname || '—' }}</td>
                <td><span class="chip" [class]="'state-' + lease.binding_state">{{ lease.binding_state }}</span></td>
                <td>{{ lease.binding_state === 'active' ? expiresIn(lease) : '—' }}</td>
                <td class="gdhcp-mono">{{ lease.binding_server_id || '—' }}</td>
                <td class="actions" (click)="$event.stopPropagation()">
                  <button mat-icon-button (click)="openReserve(lease)" [attr.aria-label]="'Reserve ' + lease.ip_address">
                    <mat-icon>bookmark_add</mat-icon>
                  </button>
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
      .header-row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        margin-bottom: 12px;
      }
      .live-indicator {
        display: flex;
        align-items: center;
        gap: 4px;
        font-size: 12px;
        color: var(--text-secondary);
      }
      .live-indicator mat-icon {
        font-size: 16px;
        width: 16px;
        height: 16px;
      }
      .live-indicator.live {
        color: var(--status-ok);
      }
      .table-wrap {
        overflow-x: auto;
      }
      .gdhcp-table {
        width: 100%;
        border-collapse: collapse;
        font-size: 13px;
      }
      .gdhcp-table th {
        text-align: left;
        font-size: 11px;
        text-transform: uppercase;
        letter-spacing: 0.03em;
        color: var(--text-secondary);
        border-bottom: 1px solid var(--border-color);
        padding: 8px;
      }
      .gdhcp-table td {
        padding: 8px;
        border-bottom: 1px solid var(--divider-color);
      }
      .chip {
        display: inline-block;
        font-size: 11px;
        padding: 1px 8px;
        border-radius: 10px;
        text-transform: capitalize;
      }
      .chip.state-active {
        background: var(--util-ok);
        color: #fff;
      }
      .chip.state-expired {
        background: var(--util-track);
        color: var(--text-secondary);
      }
      .chip.state-released {
        background: var(--bg-highlight);
        color: var(--primary-color);
      }
      .chip.state-abandoned {
        background: var(--status-error);
        color: #fff;
      }
      .actions {
        text-align: right;
        white-space: nowrap;
      }
    `,
  ],
})
export class PoolLeasesTabComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);
  private readonly dialog = inject(MatDialog);
  private readonly destroyRef = inject(DestroyRef);
  readonly sse = inject(SseService);

  readonly pool = input.required<DHCPPool>();

  readonly loading = signal(true);
  readonly leases = signal<DHCPLease[]>([]);
  readonly stateFilter = signal<StateFilter>('all');

  private readonly nowSignal = signal(Date.now());

  constructor() {
    this.sse.connect();

    effect(
      () => {
        this.pool();
        this.stateFilter();
        this.load();
      },
      { allowSignalWrites: true },
    );

    this.sse.message$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((event) => {
      if (event.type === 'leases' && event.data.some((c) => c.scope_id === this.pool().scope_id)) {
        this.load();
      }
    });

    const timer = setInterval(() => this.nowSignal.set(Date.now()), 30000);
    this.destroyRef.onDestroy(() => clearInterval(timer));
  }

  load(): void {
    this.loading.set(true);
    const state = this.stateFilter();
    this.api
      .listPoolLeases(this.pool().id, { limit: 500, ...(state !== 'all' ? { state } : {}) })
      .subscribe({
        next: (page) => {
          this.leases.set(page.items);
          this.loading.set(false);
        },
        error: (err) => {
          this.loading.set(false);
          this.notify.error(err, 'Could not load leases.');
        },
      });
  }

  onFilterChange(value: StateFilter): void {
    this.stateFilter.set(value);
  }

  expiresIn(lease: DHCPLease): string {
    return formatExpiresIn(lease.lease_end, this.nowSignal());
  }

  formatClientId(clientId: string | null): string {
    if (!clientId) return '—';
    // clientId is base64-encoded; decode and show as hex
    try {
      const binary = atob(clientId);
      let hex = '';
      for (let i = 0; i < binary.length; i++) {
        hex += binary.charCodeAt(i).toString(16).padStart(2, '0');
      }
      return hex;
    } catch {
      return clientId;
    }
  }

  openReserve(lease: DHCPLease): void {
    const pool = this.pool();
    const dialogData: ReservationFormDialogData = {
      poolRangeStart: pool.range_start,
      poolRangeEnd: pool.range_end,
      ip: lease.ip_address,
      mac: lease.client_mac ?? undefined,
      clientId: lease.client_id ? clientIdToHex(lease.client_id) : undefined,
      hostname: lease.client_hostname ?? undefined,
    };

    this.dialog
      .open<ReservationFormDialogComponent, ReservationFormDialogData, ReservationFormResult | undefined>(
        ReservationFormDialogComponent,
        { data: dialogData, width: '420px' },
      )
      .afterClosed()
      .subscribe((result) => {
        if (!result) return;
        this.api.createReservation(pool.id, result).subscribe({
          next: () => {
            this.notify.success('Reservation created.');
            this.load();
          },
          error: (err) => this.notify.error(err, 'Could not create reservation.'),
        });
      });
  }
}
