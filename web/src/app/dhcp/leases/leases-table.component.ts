import {
  ChangeDetectionStrategy,
  Component,
  DestroyRef,
  computed,
  effect,
  inject,
  input,
  signal,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatMenuModule } from '@angular/material/menu';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterLink } from '@angular/router';
import { Subject, forkJoin, of } from 'rxjs';
import { catchError, debounceTime, distinctUntilChanged, map, switchMap } from 'rxjs/operators';

import { ApiService } from '../../shared/api.service';
import { SseService } from '../../shared/sse.service';
import { AddressStatusChipComponent } from '../../shared/components/address-status/address-status.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PinChipComponent, PinButtonComponent } from '../../shared/components/pin-indicator/pin-indicator.component';
import { DHCPLease, DHCPPool, DHCPScope, LeaseAction, AddressStatus } from '../../shared/models';
import { clientIdToHex } from '../shared/ip-utils';
import { NotifyService } from '../shared/notify.service';
import {
  ReservationFormDialogComponent,
  ReservationFormDialogData,
  ReservationFormResult,
} from '../shared/reservation-form-dialog.component';

type StatusFilter = AddressStatus | 'all';

const PAGE_SIZE_OPTIONS = [25, 50, 100, 250];

/** How a bulk action is described in its confirmation prompt and toast. */
const ACTION_LABELS: Readonly<Record<LeaseAction, { verb: string; past: string; danger: boolean }>> = {
  release: { verb: 'Release', past: 'released', danger: false },
  abandon: { verb: 'Abandon', past: 'abandoned', danger: true },
  renew: { verb: 'Force renew', past: 'renewed', danger: false },
  delete: { verb: 'Delete', past: 'deleted', danger: true },
};

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

/**
 * The leases table, shared by the standalone `/dhcp/leases` page and the
 * pool detail page's Leases tab (`[poolId]` set) so both get the same
 * search, selection and actions rather than the tab keeping a reduced copy.
 *
 * Everything the table can do to a lease it can do to a selection of them:
 * the row menu and the selection bar both go through `runAction`, which
 * calls `POST /leases/bulk` and reports partial results honestly ("8
 * released, 2 failed") instead of collapsing them into one success or one
 * error.
 *
 * "Pin" is the reservation shortcut: it turns the address the client
 * currently holds into a `dhcp_reservations` row keyed on that client, so
 * the same client keeps the same IP. Pinning is per-pool (a reservation
 * belongs to a pool), which is why the pool index below is loaded up front
 * — a lease alone doesn't carry the range its reservation must fall in.
 *
 * Live updates come from the `leases` SSE channel (A12); the table refetches
 * on churn rather than patching rows, since the event payload is per-scope
 * churn counts, not lease rows.
 */
@Component({
  selector: 'app-leases-table',
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    AddressStatusChipComponent,
    EmptyStateComponent,
    PinChipComponent,
    PinButtonComponent,
    MatButtonModule,
    MatCheckboxModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatMenuModule,
    MatPaginatorModule,
    MatProgressBarModule,
    MatSelectModule,
    MatTooltipModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="filters">
      <mat-form-field appearance="outline" subscriptSizing="dynamic" class="search-field">
        <mat-icon matPrefix>search</mat-icon>
        <input
          matInput
          placeholder="Search IP, MAC or hostname"
          [ngModel]="searchText()"
          (ngModelChange)="onSearchChange($event)"
        />
        @if (searchText()) {
          <button matSuffix mat-icon-button aria-label="Clear search" (click)="onSearchChange('')">
            <mat-icon>close</mat-icon>
          </button>
        }
      </mat-form-field>

      <mat-form-field appearance="outline" subscriptSizing="dynamic" class="state-field">
        <mat-label>Status</mat-label>
        <mat-select [ngModel]="statusFilter()" (ngModelChange)="onStatusChange($event)">
          <mat-option value="all">All</mat-option>
          <mat-option value="free">Free</mat-option>
          <mat-option value="assigned">Assigned</mat-option>
          <mat-option value="reserved">Reserved</mat-option>
          <mat-option value="abandoned">Abandoned</mat-option>
        </mat-select>
      </mat-form-field>

      @if (!poolId()) {
        <mat-form-field appearance="outline" subscriptSizing="dynamic" class="scope-field">
          <mat-label>Scope</mat-label>
          <mat-select [ngModel]="scopeFilter()" (ngModelChange)="onScopeChange($event)">
            <mat-option value="">All scopes</mat-option>
            @for (scope of scopes(); track scope.id) {
              <mat-option [value]="scope.id">{{ scope.name }}</mat-option>
            }
          </mat-select>
        </mat-form-field>

        <mat-form-field appearance="outline" subscriptSizing="dynamic" class="pool-field">
          <mat-label>Pool</mat-label>
          <mat-select [ngModel]="poolFilter()" (ngModelChange)="onPoolChange($event)">
            <mat-option value="">All pools</mat-option>
            @for (pool of selectablePools(); track pool.id) {
              <mat-option [value]="pool.id">{{ pool.name }}</mat-option>
            }
          </mat-select>
        </mat-form-field>
      }

      <span class="spacer"></span>

      <span class="live-indicator" [class.live]="sse.connected()">
        <mat-icon>{{ sse.connected() ? 'sensors' : 'sensors_off' }}</mat-icon>
        {{ sse.connected() ? 'Live' : 'Polling' }}
      </span>

      <button mat-icon-button (click)="load()" matTooltip="Refresh" aria-label="Refresh leases">
        <mat-icon>refresh</mat-icon>
      </button>
    </div>

    @if (selectedCount() > 0) {
      <div class="selection-bar">
        <span class="count">{{ selectedCount() }} selected</span>
        <button mat-stroked-button (click)="pinSelected()" [disabled]="busy()">
          <mat-icon>push_pin</mat-icon>
          Pin as reservations
        </button>
        <button mat-stroked-button (click)="runAction('renew')" [disabled]="busy()">
          <mat-icon>autorenew</mat-icon>
          Force renew
        </button>
        <button mat-stroked-button (click)="runAction('release')" [disabled]="busy()">
          <mat-icon>lock_open</mat-icon>
          Release
        </button>
        <button mat-stroked-button (click)="runAction('abandon')" [disabled]="busy()">
          <mat-icon>block</mat-icon>
          Abandon
        </button>
        <button mat-stroked-button color="warn" (click)="runAction('delete')" [disabled]="busy()">
          <mat-icon>delete</mat-icon>
          Delete
        </button>
        <span class="spacer"></span>
        <button mat-button (click)="clearSelection()">Clear</button>
      </div>
    }

    @if (loading() || busy()) {
      <mat-progress-bar mode="indeterminate" />
    }

    @if (!loading() && leases().length === 0) {
      <app-empty-state
        icon="schedule"
        [title]="searchText() ? 'No matching leases' : 'No leases'"
        [message]="
          searchText()
            ? 'No lease matches that IP, MAC or hostname with the current filters.'
            : 'No leases match this filter yet.'
        "
      />
    } @else if (leases().length > 0) {
      <div class="table-wrap">
        <table class="gdhcp-table">
          <thead>
            <tr>
              <th class="checkbox-cell">
                <mat-checkbox
                  [checked]="allOnPageSelected()"
                  [indeterminate]="someOnPageSelected()"
                  (change)="toggleAllOnPage($event.checked)"
                  aria-label="Select all leases on this page"
                />
              </th>
              <th>IP</th>
              <th>MAC</th>
              <th>Client ID</th>
              <th>Hostname</th>
              @if (!poolId()) {
                <th>Pool</th>
              }
              <th>Status</th>
              <th>Expires in</th>
              <th>Pinned</th>
              <th class="actions"></th>
            </tr>
          </thead>
          <tbody>
            @for (lease of leases(); track lease.id) {
              <tr [class.selected]="isSelected(lease.id)">
                <td class="checkbox-cell">
                  <mat-checkbox
                    [checked]="isSelected(lease.id)"
                    (change)="toggleOne(lease.id, $event.checked)"
                    [attr.aria-label]="'Select ' + lease.ip_address"
                  />
                </td>
                <td class="gdhcp-mono">
                  <a [routerLink]="['/dhcp/leases', lease.id]">{{ lease.ip_address }}</a>
                </td>
                <td class="gdhcp-mono">{{ lease.client_mac || '—' }}</td>
                <td class="gdhcp-mono client-id">{{ formatClientId(lease.client_id) }}</td>
                <td>{{ lease.client_hostname || '—' }}</td>
                @if (!poolId()) {
                  <td>{{ poolLabel(lease.pool_id) }}</td>
                }
                <td>
                  <app-address-status-chip [status]="lease.address_status" />
                </td>
                <td>{{ lease.address_status === 'assigned' ? expiresIn(lease) : '—' }}</td>
                <td>
                  <app-pin-chip [state]="lease.reservation_state" [reservation]="lease.reservation" />
                </td>
                <td class="actions">
                  <app-pin-button
                    [state]="lease.reservation_state"
                    [reservation]="lease.reservation"
                    [disabled]="busy()"
                    [label]="lease.ip_address"
                    (toggle)="togglePin(lease)"
                  />
                  <button
                    mat-icon-button
                    [matMenuTriggerFor]="rowMenu"
                    [disabled]="busy()"
                    [attr.aria-label]="'Actions for ' + lease.ip_address"
                  >
                    <mat-icon>more_vert</mat-icon>
                  </button>
                  <mat-menu #rowMenu="matMenu">
                    <a mat-menu-item [routerLink]="['/dhcp/leases', lease.id]">
                      <mat-icon>open_in_new</mat-icon>
                      <span>Open lease</span>
                    </a>
                    <button mat-menu-item (click)="runAction('renew', lease)">
                      <mat-icon>autorenew</mat-icon>
                      <span>Force renew</span>
                    </button>
                    <button mat-menu-item (click)="runAction('release', lease)">
                      <mat-icon>lock_open</mat-icon>
                      <span>Release</span>
                    </button>
                    <button mat-menu-item (click)="runAction('abandon', lease)">
                      <mat-icon>block</mat-icon>
                      <span>Abandon</span>
                    </button>
                    <button mat-menu-item (click)="runAction('delete', lease)">
                      <mat-icon>delete</mat-icon>
                      <span>Delete</span>
                    </button>
                  </mat-menu>
                </td>
              </tr>
            }
          </tbody>
        </table>
      </div>

      <mat-paginator
        [length]="total()"
        [pageSize]="pageSize()"
        [pageIndex]="pageIndex()"
        [pageSizeOptions]="pageSizeOptions"
        (page)="onPage($event)"
        aria-label="Select page of leases"
      />
    }
  `,
  styles: [
    `
      .filters {
        display: flex;
        align-items: center;
        gap: 12px;
        flex-wrap: wrap;
        margin-bottom: 12px;
      }

      .search-field {
        flex: 1 1 260px;
        max-width: 360px;
      }

      .state-field,
      .scope-field,
      .pool-field {
        width: 170px;
      }

      .spacer {
        flex: 1;
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

      .selection-bar {
        display: flex;
        align-items: center;
        gap: 8px;
        flex-wrap: wrap;
        padding: 8px 12px;
        margin-bottom: 12px;
        border: 1px solid var(--border-color);
        border-radius: 6px;
        background: var(--bg-highlight);
      }

      .selection-bar .count {
        font-weight: 600;
        margin-right: 4px;
      }

      .table-wrap {
        overflow-x: auto;
      }

      .checkbox-cell {
        width: 40px;
        padding-right: 0;
      }

      /* Client IDs are long opaque hex; keep them from stretching the table
         out of the viewport on their own. */
      .client-id {
        max-width: 160px;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }

      /* gdhcp-mono and chip are global utility classes defined in styles.scss. */

      .actions {
        text-align: right;
        white-space: nowrap;
      }
    `,
  ],
})
export class LeasesTableComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);
  private readonly dialog = inject(MatDialog);
  private readonly confirm = inject(ConfirmDialogService);
  private readonly destroyRef = inject(DestroyRef);
  readonly sse = inject(SseService);

  /** Set by the pool detail tab to pin the table to one pool; the scope and
   * pool filters and the Pool column are dropped in that mode. */
  readonly poolId = input<string | undefined>(undefined);

  /** The pool, when the caller already has it. Saves the pool index lookup
   * that "Pin" needs for the reservation's allowed range. */
  readonly pool = input<DHCPPool | undefined>(undefined);

  readonly pageSizeOptions = PAGE_SIZE_OPTIONS;

  readonly loading = signal(true);
  readonly busy = signal(false);
  readonly leases = signal<DHCPLease[]>([]);
  readonly total = signal(0);
  readonly pageIndex = signal(0);
  readonly pageSize = signal(50);

  readonly searchText = signal('');
  private readonly appliedSearch = signal('');
  readonly statusFilter = signal<StatusFilter>('all');
  readonly scopeFilter = signal('');
  readonly poolFilter = signal('');

  readonly scopes = signal<DHCPScope[]>([]);
  readonly pools = signal<DHCPPool[]>([]);
  readonly selectedIds = signal<ReadonlySet<string>>(new Set());

  /** Pools narrowed to the chosen scope, so the two selects stay coherent. */
  readonly selectablePools = computed(() => {
    const scopeId = this.scopeFilter();
    const pools = this.pools();
    return scopeId ? pools.filter((p) => p.scope_id === scopeId) : pools;
  });

  readonly selectedCount = computed(() => this.selectedIds().size);

  readonly allOnPageSelected = computed(() => {
    const rows = this.leases();
    if (rows.length === 0) return false;
    const selected = this.selectedIds();
    return rows.every((l) => selected.has(l.id));
  });

  readonly someOnPageSelected = computed(() => {
    const selected = this.selectedIds();
    const rows = this.leases();
    return rows.some((l) => selected.has(l.id)) && !this.allOnPageSelected();
  });

  private readonly nowSignal = signal(Date.now());
  private readonly poolsById = computed(() => new Map(this.pools().map((p) => [p.id, p])));
  private readonly scopesById = computed(() => new Map(this.scopes().map((s) => [s.id, s])));
  private readonly search$ = new Subject<string>();

  constructor() {
    this.sse.connect();
    this.loadPoolIndex();

    effect(
      () => {
        // Re-read every input the query depends on so a change to any of
        // them refetches.
        this.poolId();
        this.appliedSearch();
        this.statusFilter();
        this.scopeFilter();
        this.poolFilter();
        this.pageIndex();
        this.pageSize();
        this.load();
      },
      { allowSignalWrites: true },
    );

    this.search$
      .pipe(debounceTime(250), distinctUntilChanged(), takeUntilDestroyed(this.destroyRef))
      .subscribe((value) => {
        this.pageIndex.set(0);
        this.appliedSearch.set(value.trim());
      });

    this.sse.message$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((event) => {
      if (event.type !== 'leases') return;
      // In pool mode only this pool's scope matters; on the global page any
      // churn can change what's on screen.
      const poolId = this.poolId();
      if (!poolId) {
        this.load();
        return;
      }
      const scopeId = this.poolsById().get(poolId)?.scope_id;
      if (!scopeId || event.data.some((c) => c.scope_id === scopeId)) {
        this.load();
      }
    });

    const timer = setInterval(() => this.nowSignal.set(Date.now()), 30000);
    this.destroyRef.onDestroy(() => clearInterval(timer));
  }

  /** Scopes and their pools, loaded once so rows can name the pool a lease
   * belongs to and "Pin" can validate against the pool's range. There is no
   * global pools endpoint — pools hang off their scope — hence the fan-out. */
  private loadPoolIndex(): void {
    this.api
      .listScopes({ limit: 500 })
      .pipe(
        switchMap((page) => {
          this.scopes.set(page.items);
          if (page.items.length === 0) return of([] as DHCPPool[][]);
          return forkJoin(
            page.items.map((scope) => this.api.listPools(scope.id, { limit: 500 }).pipe(map((pools) => pools.items))),
          );
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: (poolGroups) => this.pools.set(poolGroups.flat()),
        // Non-fatal: without the index the table still lists leases, it
        // just shows raw pool IDs and asks the server for the pool when a
        // lease is pinned.
        error: () => this.pools.set([]),
      });
  }

  load(): void {
    this.loading.set(true);
    const status = this.statusFilter();
    const search = this.appliedSearch();
    const params = {
      limit: this.pageSize(),
      offset: this.pageIndex() * this.pageSize(),
      ...(status !== 'all' ? { status } : {}),
      ...(search ? { search } : {}),
    };

    const poolId = this.poolId();
    const request = poolId
      ? this.api.listPoolLeases(poolId, params)
      : this.api.listLeases({
          ...params,
          ...(this.scopeFilter() ? { scope_id: this.scopeFilter() } : {}),
          ...(this.poolFilter() ? { pool_id: this.poolFilter() } : {}),
        });

    request.subscribe({
      next: (page) => {
        this.leases.set(page.items ?? []);
        this.total.set(page.total ?? 0);
        this.pruneSelection();
        this.loading.set(false);
      },
      error: (err) => {
        this.loading.set(false);
        this.notify.error(err, 'Could not load leases.');
      },
    });
  }

  // -- Filters ---------------------------------------------------------

  onSearchChange(value: string): void {
    this.searchText.set(value);
    this.search$.next(value);
  }

  onStatusChange(value: StatusFilter): void {
    this.pageIndex.set(0);
    this.statusFilter.set(value);
  }

  onScopeChange(value: string): void {
    this.pageIndex.set(0);
    this.scopeFilter.set(value);
    // A pool from the previous scope would silently return nothing.
    if (this.poolFilter() && this.poolsById().get(this.poolFilter())?.scope_id !== value) {
      this.poolFilter.set('');
    }
  }

  onPoolChange(value: string): void {
    this.pageIndex.set(0);
    this.poolFilter.set(value);
  }

  onPage(event: PageEvent): void {
    this.pageSize.set(event.pageSize);
    this.pageIndex.set(event.pageIndex);
  }

  // -- Selection -------------------------------------------------------

  isSelected(id: string): boolean {
    return this.selectedIds().has(id);
  }

  toggleOne(id: string, checked: boolean): void {
    const next = new Set(this.selectedIds());
    if (checked) next.add(id);
    else next.delete(id);
    this.selectedIds.set(next);
  }

  toggleAllOnPage(checked: boolean): void {
    const next = new Set(this.selectedIds());
    for (const lease of this.leases()) {
      if (checked) next.add(lease.id);
      else next.delete(lease.id);
    }
    this.selectedIds.set(next);
  }

  clearSelection(): void {
    this.selectedIds.set(new Set());
  }

  /** Drop selected IDs that are no longer on screen. Selection is meant to
   * be what the operator can currently see and act on; keeping ids from a
   * page they have navigated away from makes the count lie. */
  private pruneSelection(): void {
    const visible = new Set(this.leases().map((l) => l.id));
    const next = new Set([...this.selectedIds()].filter((id) => visible.has(id)));
    if (next.size !== this.selectedIds().size) this.selectedIds.set(next);
  }

  // -- Display helpers -------------------------------------------------

  expiresIn(lease: DHCPLease): string {
    return formatExpiresIn(lease.lease_end, this.nowSignal());
  }

  formatClientId(clientId: string | null): string {
    return clientId ? clientIdToHex(clientId) : '—';
  }

  poolLabel(poolId: string): string {
    const pool = this.poolsById().get(poolId);
    if (!pool) return poolId.slice(0, 8);
    const scope = this.scopesById().get(pool.scope_id);
    return scope ? `${scope.name} / ${pool.name}` : pool.name;
  }

  // -- Actions ---------------------------------------------------------

  /** Real toggle over lease.reservation_state. amendment A19: the pin button
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
      this.pinLease(lease);
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

  /** Turn a lease into a reservation, prefilled from the client currently
   * holding the address. */
  pinLease(lease: DHCPLease): void {
    const pool = this.pool() ?? this.poolsById().get(lease.pool_id);
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

  /** Bulk pin: create a reservation for each selected lease, binding its
   * current address to the client holding it. Skips leases that are already
   * pinned (to any address) or whose address is reserved for another client.
   * The per-lease dialog would mean N dialogs, so this confirms once and
   * reports the tally. Amendment A19. */
  pinSelected(): void {
    const selected = this.selectedLeases();
    if (selected.length === 0) return;

    this.confirm
      .confirm({
        title: `Pin ${selected.length} lease${selected.length === 1 ? '' : 's'}?`,
        message:
          `A reservation will be created for each selected unpinned lease, binding its current address to the ` +
          `client holding it. Leases with no MAC and no client ID, or that are already pinned, are skipped.`,
        confirmText: 'Pin',
      })
      .subscribe((ok) => {
        if (!ok) return;
        this.pinAll(selected);
      });
  }

  private pinAll(leases: DHCPLease[]): void {
    // Filter step 1: leases that identify a client (have MAC or client ID).
    const withClient = leases.filter((l) => l.client_mac || l.client_id);
    const noClient = leases.length - withClient.length;

    // Filter step 2: leases that are not already pinned or blocked.
    // Skip 'pinned', 'pinned_disabled', 'pinned_elsewhere', 'address_reserved';
    // only 'not_pinned' can be pinned again. Amendment A19.
    const pinnable = withClient.filter((l) => l.reservation_state === 'not_pinned');
    const alreadyPinned = withClient.length - pinnable.length;

    if (pinnable.length === 0) {
      if (noClient > 0 && alreadyPinned > 0) {
        this.notify.error(
          null,
          `None of the selected leases can be pinned: ${noClient} have no client identity, ` +
            `and ${alreadyPinned} are already pinned or reserved.`,
        );
      } else if (noClient > 0) {
        this.notify.error(null, 'None of the selected leases identify a client to pin to.');
      } else {
        this.notify.error(null, 'All selected leases are already pinned or reserved.');
      }
      return;
    }

    this.busy.set(true);
    forkJoin(
      pinnable.map((lease) =>
        this.api
          .createReservation(lease.pool_id, {
            reserved_ip: lease.ip_address,
            binding_type: lease.client_mac ? 'mac' : 'client_id',
            binding_key: lease.client_mac ?? clientIdToHex(lease.client_id ?? ''),
            hostname: lease.client_hostname,
            description: null,
          })
          // A failure here is per-lease, not fatal to the batch: an address
          // that already has a reservation must not stop the other 30, and
          // forkJoin would abort the whole set on the first error.
          .pipe(
            map(() => true),
            catchError(() => of(false)),
          ),
      ),
    ).subscribe({
      next: (results) => {
        this.busy.set(false);
        const ok = results.filter(Boolean).length;
        const failed = results.length - ok;
        this.reportOutcome('pinned', ok, failed, noClient + alreadyPinned);
        this.clearSelection();
        this.load();
      },
      error: (err) => {
        this.busy.set(false);
        this.notify.error(err, 'Could not pin the selected leases.');
      },
    });
  }

  /** Apply one action to a single lease, or to the whole selection when no
   * lease is given. Destructive actions confirm first. */
  runAction(action: LeaseAction, lease?: DHCPLease): void {
    const targets = lease ? [lease] : this.selectedLeases();
    if (targets.length === 0) return;
    const meta = ACTION_LABELS[action];

    const subject =
      targets.length === 1
        ? targets[0].ip_address
        : `${targets.length} lease${targets.length === 1 ? '' : 's'}`;

    if (!meta.danger && targets.length === 1) {
      this.dispatch(action, targets);
      return;
    }

    this.confirm
      .confirm({
        title: `${meta.verb} ${subject}?`,
        message: this.actionWarning(action, targets.length),
        confirmText: meta.verb,
        danger: meta.danger,
      })
      .subscribe((ok) => {
        if (ok) this.dispatch(action, targets);
      });
  }

  private actionWarning(action: LeaseAction, count: number): string {
    const these = count === 1 ? 'this lease' : `these ${count} leases`;
    switch (action) {
      case 'release':
        return `Releasing ${these} returns the address${count === 1 ? '' : 'es'} to the pool; a client still using it will keep the address until it next renews.`;
      case 'abandon':
        return `Abandoning ${these} marks the address${count === 1 ? '' : 'es'} as in use on the wire. They will not be handed out again until the reaper sweeps them.`;
      case 'renew':
        return `Force renewing ${these} extends the lease and reactivates any that had lapsed.`;
      case 'delete':
        return `Deleting ${these} removes the lease record${count === 1 ? '' : 's'} outright. This cannot be undone.`;
    }
  }

  private dispatch(action: LeaseAction, targets: DHCPLease[]): void {
    this.busy.set(true);
    this.api
      .bulkLeaseAction({ action, lease_ids: targets.map((l) => l.id) })
      .subscribe({
        next: (response) => {
          this.busy.set(false);
          this.reportOutcome(ACTION_LABELS[action].past, response.succeeded, response.failed, 0, response.results);
          this.clearSelection();
          this.load();
        },
        error: (err) => {
          this.busy.set(false);
          this.notify.error(err, `Could not ${ACTION_LABELS[action].verb.toLowerCase()} the selected leases.`);
        },
      });
  }

  /** One honest sentence covering successes, failures and skips, rather
   * than a blanket "done" that hides the leases that didn't move. */
  private reportOutcome(
    past: string,
    succeeded: number,
    failed: number,
    skipped: number,
    results?: { ok: boolean; error?: string }[],
  ): void {
    const parts = [`${succeeded} ${past}`];
    if (failed > 0) parts.push(`${failed} failed`);
    if (skipped > 0) parts.push(`${skipped} skipped`);
    const summary = parts.join(', ') + '.';

    if (failed > 0) {
      const firstError = results?.find((r) => !r.ok)?.error;
      this.notify.error(null, firstError ? `${summary} First failure: ${firstError}` : summary);
    } else {
      this.notify.success(summary);
    }
  }

  private selectedLeases(): DHCPLease[] {
    const selected = this.selectedIds();
    return this.leases().filter((l) => selected.has(l.id));
  }
}
