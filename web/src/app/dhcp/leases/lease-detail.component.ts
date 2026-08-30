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
import { AddressStatusChipComponent } from '../../shared/components/address-status/address-status.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { PinButtonComponent } from '../../shared/components/pin-indicator/pin-indicator.component';
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
    AddressStatusChipComponent,
    EmptyStateComponent,
    PageHeaderComponent,
    PinButtonComponent,
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
        <app-pin-button
          [state]="l.reservation_state"
          [reservation]="l.reservation"
          [disabled]="busy()"
          [label]="l.ip_address"
          (toggle)="togglePin(l)"
        />
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
          <dt>Status</dt>
          <dd><app-address-status-chip [status]="l.address_status" /></dd>

          <dt>DHCP binding</dt>
          <dd>
            <span class="gdhcp-state-chip" [class]="'state-' + l.binding_state">{{ l.binding_state }}</span>
            <span class="dhcp-binding-explain">The protocol-level binding state; released and expired both mean the address is free.</span>
          </dd>

          <!-- A reservation is the SAME thing the Status row already calls
               "Reserved" -- pinning is just the shortcut that creates one. So
               this row states the FACT (which address the reservation binds,
               and whether it is enabled) rather than repeating the status in a
               second vocabulary. -->
          <dt>Reservation</dt>
          <dd>
            @if (l.reservation; as res) {
              <span class="gdhcp-mono">{{ res.reserved_ip }}</span>
              @if (res.reserved_ip !== l.ip_address) {
                <span class="reservation-note">
                  — this client is reserved on a different address, so it is not guaranteed to keep
                  {{ l.ip_address }}.
                </span>
              } @else if (!res.is_enabled) {
                <span class="reservation-note">— disabled, so the DHCP engine ignores it.</span>
              }
              <a [routerLink]="['/dhcp/pools', l.pool_id]" [queryParams]="{ tab: 'reservations' }" class="reservation-link">
                View in pool
              </a>
            } @else {
              <span class="gdhcp-muted">None — this address is handed out dynamically.</span>
            }
          </dd>

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

      /* gdhcp-state-chip is a global utility class defined in styles.scss. */

      .reservation-note {
        margin-left: 8px;
        font-size: 12px;
        color: var(--text-secondary);
      }

      .dhcp-binding-explain {
        display: block;
        font-size: 12px;
        color: var(--text-secondary);
        margin-top: 4px;
      }

      .reservation-link {
        display: inline-block;
        margin-left: 12px;
        font-size: 12px;
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

  /** Real toggle over lease.reservation_state. Amendment A19: the pin button
   * now reads the lease's actual reservation state instead of always attempting
   * to create a new one, so pinning a client that is already pinned to this
   * address no longer produces a UNIQUE constraint error. */
  togglePin(lease: DHCPLease): void {
    const state = lease.reservation_state;

    if (state === 'address_reserved') {
      // Button is already disabled by PinButtonComponent; return defensively.
      return;
    }

    if (state === 'not_pinned') {
      // No reservation exists; show the form and POST a new one.
      this.pin(lease);
      return;
    }

    if (state === 'pinned' || state === 'pinned_disabled') {
      // Delete the existing reservation.
      // A disabled reservation is invisible to allocation (both
      // reservationForKeys and scanCandidateIPs filter on is_enabled), so say
      // plainly that removing it changes nothing about who gets the address.
      const message =
        state === 'pinned_disabled'
          ? `The reservation binding this client to ${lease.ip_address} is disabled, so the DHCP engine already ignores it. ` +
            `Removing it changes nothing about who gets this address.`
          : `${lease.ip_address} goes back to being handed out dynamically, so this client may be given a different ` +
            `address the next time it asks.`;
      this.confirm
        .confirm({ title: `Unpin ${lease.ip_address}?`, message, confirmText: 'Unpin' })
        .subscribe((ok) => {
        if (!ok || !lease.reservation) return;
        this.busy.set(true);
        this.api.deleteReservation(lease.reservation.id).subscribe({
          next: () => {
            this.busy.set(false);
            this.notify.success(`${lease.ip_address} unpinned.`);
            this.load();
          },
          error: (err) => {
            this.busy.set(false);
            this.notify.error(err, 'Could not remove the reservation.');
          },
        });
      });
      return;
    }

    if (state === 'pinned_elsewhere') {
      // Client is pinned to a different address; move it.
      // The case that used to fail on UNIQUE (pool_id, binding_key): a client
      // gets one reservation per pool, so the existing one moves rather than a
      // second one being created.
      const currentIP = lease.reservation?.reserved_ip ?? 'another address';
      this.confirm
        .confirm({
          title: `Move the pin to ${lease.ip_address}?`,
          message:
            `This client is already reserved on ${currentIP}. A client can hold only one reservation per pool, ` +
            `so that reservation moves here instead of a second one being created — ${currentIP} returns to the ` +
            `dynamic range.`,
          confirmText: 'Move pin',
        })
        .subscribe((ok) => {
        if (!ok || !lease.reservation) return;
        this.busy.set(true);
        this.api.updateReservation(lease.reservation.id, { reserved_ip: lease.ip_address }).subscribe({
          next: () => {
            this.busy.set(false);
            this.notify.success(`Pin moved to ${lease.ip_address}.`);
            this.load();
          },
          error: (err) => {
            this.busy.set(false);
            this.notify.error(err, 'Could not move the reservation.');
          },
        });
      });
      return;
    }
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
