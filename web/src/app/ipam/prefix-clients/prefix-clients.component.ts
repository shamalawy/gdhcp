import { ChangeDetectionStrategy, Component, DestroyRef, computed, effect, inject, input, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterLink } from '@angular/router';
import { Subject } from 'rxjs';
import { debounceTime, distinctUntilChanged } from 'rxjs/operators';

import { ApiService } from '../../shared/api.service';
import { AddressStatus, BindingType, DHCPClient, LeaseBindingState } from '../../shared/models';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { AddressStatusChipComponent } from '../../shared/components/address-status/address-status.component';
import { PinChipComponent, PinButtonComponent } from '../../shared/components/pin-indicator/pin-indicator.component';
import { NotifyService } from '../../dhcp/shared/notify.service';
import {
  ReservationFormDialogComponent,
  ReservationFormDialogData,
  ReservationFormResult,
} from '../../dhcp/shared/reservation-form-dialog.component';
import { clientIdToHex } from '../../dhcp/shared/ip-utils';

const PAGE_SIZE_OPTIONS = [10, 25, 50];

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

function formatBindingType(type: BindingType): string {
  switch (type) {
    case 'mac':
      return 'MAC';
    case 'client_id':
      return 'Client ID';
    case 'hostname':
      return 'Hostname';
    case 'circuit_id':
      return 'Circuit ID';
    case 'remote_id':
      return 'Remote ID';
    case 'vendor_class':
      return 'Vendor class';
    case 'user_class':
      return 'User class';
    default:
      return type;
  }
}

/**
 * IPAM prefix clients table — displays rolled-up DHCP clients (leases +
 * reservations) for a prefix and all its descendants. Supports pagination,
 * filtering (kind, state), sorting, and free-text search. Lease rows offer a
 * per-row "Reserve" action that opens the reservation dialog prefilled from
 * that client's identifiers.
 */
@Component({
  selector: 'app-prefix-clients',
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatPaginatorModule,
    MatProgressBarModule,
    MatSelectModule,
    MatTooltipModule,
    EmptyStateComponent,
    AddressStatusChipComponent,
    PinChipComponent,
    PinButtonComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="controls-row">
      <div class="filters">
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Status</mat-label>
          <mat-select [(ngModel)]="statusFilter" (ngModelChange)="onStatusChange($event)">
            <mat-option value="">All</mat-option>
            <mat-option value="free">Free</mat-option>
            <mat-option value="assigned">Assigned</mat-option>
            <mat-option value="reserved">Reserved</mat-option>
            <mat-option value="abandoned">Abandoned</mat-option>
          </mat-select>
        </mat-form-field>

        <mat-form-field appearance="outline" subscriptSizing="dynamic" class="search-field">
          <mat-label>Search IP, MAC or hostname</mat-label>
          <input matInput [(ngModel)]="searchText" (ngModelChange)="onSearchChange($event)" />
          @if (searchText) {
            <button mat-icon-button matSuffix (click)="clearSearch()">
              <mat-icon>close</mat-icon>
            </button>
          }
        </mat-form-field>
      </div>
    </div>

    @if (loading()) {
      <mat-progress-bar mode="indeterminate" />
    }

    @if (!loading() && clients().length === 0) {
      <app-empty-state icon="dns" title="No DHCP clients" message="This prefix and its children have no active clients or reservations yet." />
    } @else if (clients().length > 0) {
      <div class="table-wrap">
        <table class="gdhcp-table">
          <thead>
            <tr>
              <th>IP</th>
              <th>Status</th>
              <th>MAC</th>
              <th>Client ID</th>
              <th>Hostname</th>
              <th>Expires in</th>
              <th>Scope</th>
              <th>Pinned</th>
              <th class="actions"></th>
            </tr>
          </thead>
          <tbody>
            @for (client of clients(); track client.id) {
              <tr>
                <td class="gdhcp-mono">{{ client.ip_address }}</td>
                <td>
                  <app-address-status-chip [status]="client.address_status" />
                </td>
                <td class="gdhcp-mono">{{ client.client_mac || '—' }}</td>
                <td class="gdhcp-mono">{{ formatClientId(client.client_id) }}</td>
                <td>{{ client.client_hostname || '—' }}</td>
                <td>
                  @if (client.kind === 'lease' && client.binding_state === 'active' && client.lease_end) {
                    {{ expiresIn(client) }}
                  } @else {
                    <span class="gdhcp-muted">—</span>
                  }
                </td>
                <td>
                  <a [routerLink]="['/dhcp', 'scopes', client.scope_id]">{{ client.scope_name }}</a>
                </td>
                <td>
                  <app-pin-chip [state]="client.reservation_state" [reservation]="client.reservation" />
                </td>
                <td class="actions" (click)="$event.stopPropagation()">
                  <app-pin-button
                    [state]="client.reservation_state"
                    [reservation]="client.reservation"
                    [disabled]="busy()"
                    [label]="client.ip_address"
                    (toggle)="togglePin(client)"
                  />
                </td>
              </tr>
            }
          </tbody>
        </table>
      </div>

      <mat-paginator
        [length]="total()"
        [pageSize]="limit()"
        [pageSizeOptions]="pageSizeOptions"
        [pageIndex]="pageIndex()"
        (page)="onPage($event)"
        aria-label="Client pages"
      />
    }
  `,
  styles: [
    `
      .controls-row {
        display: flex;
        align-items: flex-end;
        gap: 12px;
        margin-bottom: 16px;
        flex-wrap: wrap;
      }

      .filters {
        display: flex;
        align-items: flex-end;
        gap: 12px;
        flex-wrap: wrap;
      }

      .search-field {
        flex: 1;
        min-width: 180px;
      }

      .actions {
        text-align: right;
        white-space: nowrap;
      }
    `,
  ],
})
export class PrefixClientsComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly notify = inject(NotifyService);
  private readonly confirm = inject(ConfirmDialogService);
  private readonly destroyRef = inject(DestroyRef);

  readonly prefixId = input.required<string>();

  readonly pageSizeOptions = PAGE_SIZE_OPTIONS;

  readonly loading = signal(true);
  readonly busy = signal(false);
  readonly clients = signal<DHCPClient[]>([]);
  readonly total = signal(0);
  readonly limit = signal(PAGE_SIZE_OPTIONS[1]);
  readonly pageIndex = signal(0);

  statusFilter: AddressStatus | '' = '';
  searchText = '';

  private readonly searchText$ = new Subject<string>();

  private readonly nowSignal = signal(Date.now());

  readonly computedParams = computed(() => {
    const params: Record<string, string | number> = {
      limit: this.limit(),
      offset: this.pageIndex() * this.limit(),
    };
    if (this.statusFilter) {
      params['status'] = this.statusFilter;
    }
    if (this.searchText) {
      params['q'] = this.searchText;
    }
    return params;
  });

  constructor() {
    // Load on input change
    effect(
      () => {
        this.prefixId();
        this.load();
      },
      { allowSignalWrites: true },
    );

    // Debounce search
    this.searchText$.pipe(debounceTime(300), distinctUntilChanged(), takeUntilDestroyed(this.destroyRef)).subscribe(() => {
      this.pageIndex.set(0);
      this.load();
    });

    // Update time display for "expires in" every 30 seconds
    const timer = setInterval(() => this.nowSignal.set(Date.now()), 30000);
    this.destroyRef.onDestroy(() => clearInterval(timer));
  }

  load(): void {
    this.loading.set(true);
    this.api
      .listPrefixClients(this.prefixId(), this.computedParams() as any)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          this.clients.set(page.items);
          this.total.set(page.total);
          this.loading.set(false);
        },
        error: (err) => {
          this.loading.set(false);
          this.notify.error(err, 'Could not load clients.');
        },
      });
  }

  onStatusChange(value: AddressStatus | ''): void {
    this.statusFilter = value;
    this.pageIndex.set(0);
    this.load();
  }

  onSearchChange(value: string): void {
    this.searchText$.next(value);
  }

  clearSearch(): void {
    this.searchText = '';
    this.searchText$.next('');
  }

  onPage(event: PageEvent): void {
    this.pageIndex.set(event.pageIndex);
    this.limit.set(event.pageSize);
    this.load();
  }

  expiresIn(client: DHCPClient): string {
    return client.lease_end ? formatExpiresIn(client.lease_end, this.nowSignal()) : '—';
  }

  /** client_id arrives base64 (Go []byte); operators read it as hex. */
  formatClientId(clientId: string | null | undefined): string {
    return clientId ? clientIdToHex(clientId) : '—';
  }

  openReserve(client: DHCPClient): void {
    const clientIdForPrefill = client.client_id ? clientIdToHex(client.client_id) : undefined;

    const dialogData: ReservationFormDialogData = {
      ip: client.ip_address,
      mac: client.client_mac ?? undefined,
      clientId: clientIdForPrefill,
      hostname: client.client_hostname ?? undefined,
    };

    this.dialog
      .open<ReservationFormDialogComponent, ReservationFormDialogData, ReservationFormResult | undefined>(
        ReservationFormDialogComponent,
        { data: dialogData, width: '420px' },
      )
      .afterClosed()
      .subscribe((result) => {
        if (!result) return;
        this.api.createReservation(client.pool_id, result).subscribe({
          next: () => {
            this.notify.success('Reservation created.');
            this.load();
          },
          error: (err) => this.notify.error(err, 'Could not create reservation.'),
        });
      });
  }

  /** Amendment A19: toggle reservation state based on the current state.
   * not_pinned: open dialog and POST a new reservation. pinned/pinned_disabled:
   * confirm then delete. pinned_elsewhere: confirm then update with new IP.
   * address_reserved: no-op (button disabled). */
  togglePin(client: DHCPClient): void {
    const state = client.reservation_state;

    if (state === 'address_reserved') {
      // Button is already disabled by PinButtonComponent; return defensively.
      return;
    }

    if (state === 'not_pinned') {
      // No reservation exists; show the form and POST a new one.
      this.openReserve(client);
      return;
    }

    if (state === 'pinned' || state === 'pinned_disabled') {
      // Delete the existing reservation.
      // A disabled reservation is invisible to allocation (both
      // reservationForKeys and scanCandidateIPs filter on is_enabled), so say
      // plainly that removing it changes nothing about who gets the address.
      const message =
        state === 'pinned_disabled'
          ? `The reservation binding this client to ${client.ip_address} is disabled, so the DHCP engine already ignores it. ` +
            `Removing it changes nothing about who gets this address.`
          : `${client.ip_address} goes back to being handed out dynamically, so this client may be given a different ` +
            `address the next time it asks.`;
      this.confirm
        .confirm({ title: `Unpin ${client.ip_address}?`, message, confirmText: 'Unpin' })
        .subscribe((ok) => {
        if (!ok || !client.reservation) return;
        this.busy.set(true);
        this.api.deleteReservation(client.reservation.id).subscribe({
          next: () => {
            this.busy.set(false);
            this.notify.success(`${client.ip_address} unpinned.`);
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
      const currentIP = client.reservation?.reserved_ip ?? 'another address';
      this.confirm
        .confirm({
          title: `Move the pin to ${client.ip_address}?`,
          message:
            `This client is already reserved on ${currentIP}. A client can hold only one reservation per pool, ` +
            `so that reservation moves here instead of a second one being created — ${currentIP} returns to the ` +
            `dynamic range.`,
          confirmText: 'Move pin',
        })
        .subscribe((ok) => {
        if (!ok || !client.reservation) return;
        this.busy.set(true);
        this.api.updateReservation(client.reservation.id, { reserved_ip: client.ip_address }).subscribe({
          next: () => {
            this.busy.set(false);
            this.notify.success(`Pin moved to ${client.ip_address}.`);
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
}
