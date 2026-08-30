import { ChangeDetectionStrategy, Component, effect, inject, input, signal } from '@angular/core';
import { DatePipe } from '@angular/common';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Router, RouterLink } from '@angular/router';

import { ApiService } from '../../shared/api.service';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { DHCPLease, DHCPPool, DHCPScope, LeaseAction } from '../../shared/models';
import { clientIdToHex } from '../shared/ip-utils';
import { NotifyService } from '../shared/notify.service';
import {
  ReservationFormDialogComponent,
  ReservationFormDialogData,
  ReservationFormResult,
} from '../shared/reservation-form-dialog.component';

/**
 * `/dhcp/leases/:id` — one lease in full, and where global search sends a
 * lease hit. It carries the same actions as a row in the leases table (pin,
 * force renew, release, abandon, delete) so arriving here from a search is
 * not a dead end.
 */
@Component({
  selector: 'app-lease-detail',
  standalone: true,
  imports: [
    DatePipe,
    RouterLink,
    EmptyStateComponent,
    PageHeaderComponent,
    MatButtonModule,
    MatIconModule,
    MatMenuModule,
    MatProgressBarModule,
    MatTooltipModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (loading()) {
      <mat-progress-bar mode="indeterminate" />
    }

    @if (!loading() && !lease()) {
      <app-empty-state
        icon="search_off"
        title="Lease not found"
        message="This lease no longer exists — it may have been deleted or swept by the reaper."
      />
    }

    @if (lease(); as l) {
      <app-page-header
        [title]="l.ip_address"
        [breadcrumbs]="[{ label: 'DHCP', link: '/dhcp' }, { label: 'Leases', link: '/dhcp/leases' }, { label: l.ip_address }]"
      >
        <button mat-stroked-button (click)="pin(l)" [disabled]="busy()">
          <mat-icon>push_pin</mat-icon>
          Pin as reservation
        </button>
        <button mat-icon-button [matMenuTriggerFor]="menu" [disabled]="busy()" aria-label="Lease actions">
          <mat-icon>more_vert</mat-icon>
        </button>
        <mat-menu #menu="matMenu">
          <button mat-menu-item (click)="act('renew', l)">
            <mat-icon>autorenew</mat-icon>
            <span>Force renew</span>
          </button>
          <button mat-menu-item (click)="act('release', l)">
            <mat-icon>lock_open</mat-icon>
            <span>Release</span>
          </button>
          <button mat-menu-item (click)="act('abandon', l)">
            <mat-icon>block</mat-icon>
            <span>Abandon</span>
          </button>
          <button mat-menu-item (click)="act('delete', l)">
            <mat-icon>delete</mat-icon>
            <span>Delete</span>
          </button>
        </mat-menu>
      </app-page-header>

      <div class="gdhcp-card">
        <dl class="facts">
          <dt>State</dt>
          <dd><span class="chip" [class]="'state-' + l.binding_state">{{ l.binding_state }}</span></dd>

          <dt>IP address</dt>
          <dd class="gdhcp-mono">{{ l.ip_address }}</dd>

          <dt>Client MAC</dt>
          <dd class="gdhcp-mono">{{ l.client_mac || '—' }}</dd>

          <dt>Client ID</dt>
          <dd class="gdhcp-mono wrap">{{ clientId(l) }}</dd>

          <dt>Hostname</dt>
          <dd>{{ l.client_hostname || '—' }}</dd>

          <dt>Pool</dt>
          <dd>
            @if (pool(); as p) {
              <a [routerLink]="['/dhcp/pools', p.id]">{{ p.name }}</a>
              <span class="muted">({{ p.range_start }} – {{ p.range_end }})</span>
            } @else {
              <span class="gdhcp-mono">{{ l.pool_id }}</span>
            }
          </dd>

          <dt>Scope</dt>
          <dd>
            @if (scope(); as s) {
              <a [routerLink]="['/dhcp/scopes', s.id]">{{ s.name }}</a>
            } @else {
              —
            }
          </dd>

          <dt>Lease start</dt>
          <dd>{{ l.lease_start | date: 'medium' }}</dd>

          <dt>Lease end</dt>
          <dd>{{ l.lease_end | date: 'medium' }}</dd>

          <dt>Renewal (T1)</dt>
          <dd>{{ l.renewal_time ? (l.renewal_time | date: 'medium') : '—' }}</dd>

          <dt>Rebinding (T2)</dt>
          <dd>{{ l.rebinding_time ? (l.rebinding_time | date: 'medium') : '—' }}</dd>

          <dt>Bound by server</dt>
          <dd class="gdhcp-mono">{{ l.binding_server_id || '—' }}</dd>

          <dt>Last updated</dt>
          <dd>{{ l.updated_at | date: 'medium' }}</dd>
        </dl>
      </div>
    }
  `,
  styles: [
    `
      .facts {
        display: grid;
        grid-template-columns: 180px 1fr;
        gap: 8px 16px;
        margin: 0;
        font-size: 13px;
      }

      dt {
        color: var(--text-secondary);
        font-size: 12px;
        text-transform: uppercase;
        letter-spacing: 0.03em;
      }

      dd {
        margin: 0;
      }

      dd.wrap {
        overflow-wrap: anywhere;
      }

      .muted {
        color: var(--text-secondary);
        margin-left: 8px;
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
    `,
  ],
})
export class LeaseDetailComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);
  private readonly dialog = inject(MatDialog);
  private readonly confirm = inject(ConfirmDialogService);
  private readonly router = inject(Router);

  /** Bound from the `:id` route parameter by withComponentInputBinding(). */
  readonly id = input.required<string>();

  readonly loading = signal(true);
  readonly busy = signal(false);
  readonly lease = signal<DHCPLease | null>(null);
  readonly pool = signal<DHCPPool | null>(null);
  readonly scope = signal<DHCPScope | null>(null);

  clientId(lease: DHCPLease): string {
    return lease.client_id ? clientIdToHex(lease.client_id) : '—';
  }

  constructor() {
    effect(
      () => {
        this.id();
        this.load();
      },
      { allowSignalWrites: true },
    );
  }

  load(): void {
    this.loading.set(true);
    this.api.getLease(this.id()).subscribe({
      next: (lease) => {
        this.lease.set(lease);
        this.loading.set(false);
        this.loadContext(lease);
      },
      error: (err) => {
        this.lease.set(null);
        this.loading.set(false);
        // A 404 is an ordinary outcome here (the lease was deleted or
        // swept), and the empty state already says so — only surface
        // anything else as a toast.
        if ((err as { status?: number })?.status !== 404) {
          this.notify.error(err, 'Could not load the lease.');
        }
      },
    });
  }

  /** The pool and scope a lease belongs to, for the cross-links and for the
   * range "Pin" validates the reservation against. Failures are silent: the
   * page degrades to showing the raw pool ID. */
  private loadContext(lease: DHCPLease): void {
    this.api.getPool(lease.pool_id).subscribe({
      next: (pool) => {
        this.pool.set(pool);
        this.api.getScope(pool.scope_id).subscribe({
          next: (scope) => this.scope.set(scope),
          error: () => this.scope.set(null),
        });
      },
      error: () => {
        this.pool.set(null);
        this.scope.set(null);
      },
    });
  }

  pin(lease: DHCPLease): void {
    const pool = this.pool();
    const dialogData: ReservationFormDialogData = {
      poolRangeStart: pool?.range_start,
      poolRangeEnd: pool?.range_end,
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
        this.api.createReservation(lease.pool_id, result).subscribe({
          next: () => {
            this.notify.success(`${lease.ip_address} pinned as a reservation.`);
            this.load();
          },
          error: (err) => this.notify.error(err, 'Could not create the reservation.'),
        });
      });
  }

  act(action: LeaseAction, lease: DHCPLease): void {
    const danger = action === 'delete' || action === 'abandon';
    if (!danger) {
      this.dispatch(action, lease);
      return;
    }
    this.confirm
      .confirm({
        title: action === 'delete' ? `Delete lease ${lease.ip_address}?` : `Abandon ${lease.ip_address}?`,
        message:
          action === 'delete'
            ? 'This removes the lease record outright and cannot be undone.'
            : 'The address is marked as in use on the wire and will not be handed out again until the reaper sweeps it.',
        confirmText: action === 'delete' ? 'Delete' : 'Abandon',
        danger: true,
      })
      .subscribe((ok) => {
        if (ok) this.dispatch(action, lease);
      });
  }

  private dispatch(action: LeaseAction, lease: DHCPLease): void {
    this.busy.set(true);
    const done = (message: string) => {
      this.busy.set(false);
      this.notify.success(message);
    };
    const fail = (fallback: string) => (err: unknown) => {
      this.busy.set(false);
      this.notify.error(err, fallback);
    };

    switch (action) {
      case 'release':
        this.api.releaseLease(lease.id).subscribe({
          next: () => {
            done('Lease released.');
            this.load();
          },
          error: fail('Could not release the lease.'),
        });
        return;
      case 'abandon':
        this.api.abandonLease(lease.id).subscribe({
          next: () => {
            done('Lease abandoned.');
            this.load();
          },
          error: fail('Could not abandon the lease.'),
        });
        return;
      case 'renew':
        this.api.renewLease(lease.id).subscribe({
          next: () => {
            done('Lease renewed.');
            this.load();
          },
          error: fail('Could not renew the lease.'),
        });
        return;
      case 'delete':
        this.api.deleteLease(lease.id).subscribe({
          next: () => {
            done('Lease deleted.');
            // The record this page is about is gone, so stay off it.
            void this.router.navigate(['/dhcp/leases']);
          },
          error: fail('Could not delete the lease.'),
        });
        return;
    }
  }
}
