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
import { BindingType, DHCPClient, LeaseBindingState } from '../../shared/models';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
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
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="controls-row">
      <div class="filters">
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Kind</mat-label>
          <mat-select [(ngModel)]="kindFilter" (ngModelChange)="onKindChange($event)">
            <mat-option value="">All</mat-option>
            <mat-option value="lease">Leases</mat-option>
            <mat-option value="reservation">Reservations</mat-option>
          </mat-select>
        </mat-form-field>

        @if (kindFilter === 'lease') {
          <mat-form-field appearance="outline" subscriptSizing="dynamic">
            <mat-label>State</mat-label>
            <mat-select [(ngModel)]="stateFilter" (ngModelChange)="onStateChange($event)">
              <mat-option value="">All</mat-option>
              <mat-option value="active">Active</mat-option>
              <mat-option value="expired">Expired</mat-option>
              <mat-option value="released">Released</mat-option>
              <mat-option value="abandoned">Abandoned</mat-option>
            </mat-select>
          </mat-form-field>
        }

        <mat-form-field appearance="outline" subscriptSizing="dynamic" class="search-field">
          <mat-label>Search IP or MAC</mat-label>
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
              <th>Kind</th>
              <th>MAC</th>
              <th>Client ID</th>
              <th>Hostname</th>
              <th>State</th>
              <th>Expires in</th>
              <th>Scope</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            @for (client of clients(); track client.ip_address) {
              <tr>
                <td class="gdhcp-mono">{{ client.ip_address }}</td>
                <td>
                  <span class="kind-chip" [class]="'kind-' + client.kind">
                    {{ client.kind === 'lease' ? 'Lease' : 'Reservation' }}
                  </span>
                </td>
                <td class="gdhcp-mono">{{ client.client_mac || '—' }}</td>
                <td class="gdhcp-mono">{{ formatClientId(client.client_id) }}</td>
                <td>{{ client.client_hostname || '—' }}</td>
                <td>
                  @if (client.binding_state) {
                    <span class="chip" [class]="'state-' + client.binding_state">{{ client.binding_state }}</span>
                  } @else {
                    <span class="gdhcp-muted">—</span>
                  }
                </td>
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
                <td class="actions" (click)="$event.stopPropagation()">
                  @if (client.kind === 'lease') {
                    <button
                      mat-icon-button
                      (click)="openReserve(client)"
                      [attr.aria-label]="'Reserve ' + client.ip_address"
                      matTooltip="Reserve this IP"
                      matTooltipPosition="before"
                    >
                      <mat-icon>bookmark_add</mat-icon>
                    </button>
                  }
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

      .kind-chip {
        display: inline-block;
        font-size: 11px;
        padding: 1px 8px;
        border-radius: 10px;
        text-transform: capitalize;
        font-weight: 500;
      }

      .kind-lease {
        background: color-mix(in srgb, var(--util-ok) 20%, transparent);
        color: var(--status-ok);
      }

      .kind-reservation {
        background: color-mix(in srgb, var(--primary-color) 20%, transparent);
        color: var(--primary-color);
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
export class PrefixClientsComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly notify = inject(NotifyService);
  private readonly destroyRef = inject(DestroyRef);

  readonly prefixId = input.required<string>();

  readonly pageSizeOptions = PAGE_SIZE_OPTIONS;

  readonly loading = signal(true);
  readonly clients = signal<DHCPClient[]>([]);
  readonly total = signal(0);
  readonly limit = signal(PAGE_SIZE_OPTIONS[1]);
  readonly pageIndex = signal(0);

  kindFilter = '';
  stateFilter = '';
  searchText = '';

  private readonly searchText$ = new Subject<string>();

  private readonly nowSignal = signal(Date.now());

  readonly computedParams = computed(() => {
    const params: Record<string, string | number> = {
      limit: this.limit(),
      offset: this.pageIndex() * this.limit(),
    };
    if (this.kindFilter) {
      params['kind'] = this.kindFilter;
    }
    if (this.stateFilter) {
      params['state'] = this.stateFilter;
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

  onKindChange(value: string): void {
    this.kindFilter = value;
    this.stateFilter = '';
    this.pageIndex.set(0);
    this.load();
  }

  onStateChange(value: string): void {
    this.stateFilter = value;
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
}
