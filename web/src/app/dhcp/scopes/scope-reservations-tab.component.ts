import { ChangeDetectionStrategy, Component, Inject, effect, inject, input, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogModule, MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatListModule, MatSelectionListChange } from '@angular/material/list';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { forkJoin, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { ApiService } from '../../shared/api.service';
import { BindingType, DHCPPool, DHCPReservation, DHCPScope } from '../../shared/models';
import { NotifyService } from '../shared/notify.service';
import {
  ReservationFormDialogComponent,
  ReservationFormDialogData,
  ReservationFormResult,
} from '../shared/reservation-form-dialog.component';

interface ReservationRow {
  reservation: DHCPReservation;
  pool: DHCPPool | null;
}

/** Parse a prefixed binding key into type and value. */
function parseBindingKey(key: string): { type: BindingType; value: string } | null {
  const colonIdx = key.indexOf(':');
  if (colonIdx < 0) return null;
  const type = key.substring(0, colonIdx);
  const value = key.substring(colonIdx + 1);
  const types: BindingType[] = ['mac', 'client_id', 'hostname', 'circuit_id', 'remote_id', 'vendor_class', 'user_class'];
  if (types.includes(type as BindingType)) {
    return { type: type as BindingType, value };
  }
  return null;
}

@Component({
  selector: 'app-scope-reservations-tab',
  standalone: true,
  imports: [FormsModule, EmptyStateComponent, MatButtonModule, MatIconModule, MatProgressBarModule, MatSlideToggleModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="header-row">
      <button mat-flat-button color="primary" (click)="openCreate()">
        <mat-icon>add</mat-icon> Add reservation
      </button>
    </div>

    @if (loading()) {
      <mat-progress-bar mode="indeterminate" />
    }

    @if (!loading() && rows().length === 0) {
      <app-empty-state icon="push_pin" title="No reservations" message="Reserve an IP for a client that should always get the same address.">
        <button mat-stroked-button (click)="openCreate()">Add reservation</button>
      </app-empty-state>
    } @else if (rows().length > 0) {
      <div class="table-wrap">
        <table class="gdhcp-table">
          <thead>
            <tr>
              <th>IP</th>
              <th>Pool</th>
              <th>Binding type</th>
              <th>Identifier</th>
              <th>Hostname</th>
              <th>Description</th>
              <th>Enabled</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            @for (row of rows(); track row.reservation.id) {
              <tr>
                <td class="gdhcp-mono">{{ row.reservation.reserved_ip }}</td>
                <td>{{ row.pool?.name || '—' }}</td>
                <td class="binding-type">{{ formatBindingType(row.reservation.binding_type) }}</td>
                <td class="gdhcp-mono">{{ extractBindingValue(row.reservation.binding_key) }}</td>
                <td>{{ row.reservation.hostname || '—' }}</td>
                <td class="gdhcp-muted">{{ row.reservation.description || '—' }}</td>
                <td>
                  <mat-slide-toggle
                    [ngModel]="row.reservation.is_enabled"
                    (ngModelChange)="toggleEnabled(row.reservation, $event)"
                    [attr.aria-label]="'Enable reservation ' + row.reservation.reserved_ip"
                  />
                </td>
                <td class="actions">
                  <button mat-icon-button (click)="openEdit(row.reservation)" [attr.aria-label]="'Edit reservation ' + row.reservation.reserved_ip">
                    <mat-icon>edit</mat-icon>
                  </button>
                  <button mat-icon-button (click)="remove(row.reservation)" [attr.aria-label]="'Delete reservation ' + row.reservation.reserved_ip">
                    <mat-icon>delete</mat-icon>
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
        margin-bottom: 12px;
      }
      .binding-type {
        font-size: 12px;
        text-transform: capitalize;
        color: var(--text-secondary);
      }
    `,
  ],
})
export class ScopeReservationsTabComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmSvc = inject(ConfirmDialogService);
  private readonly notify = inject(NotifyService);

  readonly scope = input.required<DHCPScope>();

  readonly loading = signal(true);
  readonly rows = signal<ReservationRow[]>([]);

  constructor() {
    effect(
      () => {
        this.scope();
        this.load();
      },
      { allowSignalWrites: true },
    );
  }

  load(): void {
    this.loading.set(true);
    const scope = this.scope();

    // Fetch all pools in this scope
    this.api.listPools(scope.id, { limit: 500 }).subscribe({
      next: (poolsPage) => {
        if (poolsPage.items.length === 0) {
          this.rows.set([]);
          this.loading.set(false);
          return;
        }

        // For each pool, fetch its reservations
        forkJoin(
          poolsPage.items.map((pool) =>
            this.api.listReservations(pool.id, { limit: 500 }).pipe(
              catchError(() => of({ items: [] } as any)),
            ),
          ),
        ).subscribe((results) => {
          const allRows: ReservationRow[] = [];
          poolsPage.items.forEach((pool, i) => {
            const reservations = results[i]?.items ?? [];
            reservations.forEach((res: DHCPReservation) => {
              allRows.push({ reservation: res, pool });
            });
          });
          this.rows.set(allRows);
          this.loading.set(false);
        });
      },
      error: (err) => {
        this.loading.set(false);
        this.notify.error(err, 'Could not load reservations.');
      },
    });
  }

  openCreate(): void {
    const scope = this.scope();
    const pools = new Map<string, DHCPPool>();

    // Build a pool lookup from current rows
    this.rows().forEach((row) => {
      if (row.pool) {
        pools.set(row.pool.id, row.pool);
      }
    });

    // If no pools are currently visible, fetch them
    if (pools.size === 0) {
      this.api.listPools(scope.id, { limit: 500 }).subscribe({
        next: (poolsPage) => {
          poolsPage.items.forEach((pool) => pools.set(pool.id, pool));
          this.openCreateWithPools(pools);
        },
        error: (err) => this.notify.error(err, 'Could not load pools.'),
      });
    } else {
      this.openCreateWithPools(pools);
    }
  }

  private openCreateWithPools(poolsMap: Map<string, DHCPPool>): void {
    const poolsArray = Array.from(poolsMap.values());

    if (poolsArray.length === 0) {
      this.notify.error(null, 'No pools available. Create a pool first.');
      return;
    }

    if (poolsArray.length === 1) {
      // Only one pool — open dialog for that pool
      this.openCreateForPool(poolsArray[0]);
    } else {
      // Multiple pools — ask which one
      this.dialog
        .open<any, { pools: DHCPPool[] }, DHCPPool | undefined>(PoolSelectorDialogComponent, {
          data: { pools: poolsArray },
          width: '360px',
        })
        .afterClosed()
        .subscribe((pool) => {
          if (pool) {
            this.openCreateForPool(pool);
          }
        });
    }
  }

  private openCreateForPool(pool: DHCPPool): void {
    this.dialog
      .open<ReservationFormDialogComponent, ReservationFormDialogData, ReservationFormResult | undefined>(
        ReservationFormDialogComponent,
        { data: { poolRangeStart: pool.range_start, poolRangeEnd: pool.range_end }, width: '420px' },
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

  openEdit(res: DHCPReservation): void {
    // Find the pool for this reservation
    const row = this.rows().find((r) => r.reservation.id === res.id);
    if (!row || !row.pool) {
      this.notify.error(null, 'Could not find pool for this reservation.');
      return;
    }

    const pool = row.pool;
    this.dialog
      .open<ReservationFormDialogComponent, ReservationFormDialogData, ReservationFormResult | undefined>(
        ReservationFormDialogComponent,
        { data: { poolRangeStart: pool.range_start, poolRangeEnd: pool.range_end, reservation: res }, width: '420px' },
      )
      .afterClosed()
      .subscribe((result) => {
        if (!result) return;
        this.api
          .updateReservation(res.id, {
            hostname: result.hostname,
            description: result.description,
            // Send the type alongside the value: the server canonicalises the
            // raw value against it, and relying on the stored type implicitly
            // would silently mis-encode if the two ever drifted.
            binding_type: result.binding_type,
            binding_key: result.binding_key,
          })
          .subscribe({
            next: () => {
              this.notify.success('Reservation updated.');
              this.load();
            },
            error: (err) => this.notify.error(err, 'Could not update reservation.'),
          });
      });
  }

  toggleEnabled(res: DHCPReservation, enabled: boolean): void {
    // Omit hostname and description: the server treats absent keys as "leave alone",
    // so this minimal request preserves those fields (amendment A19). Do not expand
    // this to { is_enabled, hostname, description } — that would wipe them on a
    // toggle-only update.
    this.api.updateReservation(res.id, { is_enabled: enabled }).subscribe({
      next: () => this.load(),
      error: (err) => this.notify.error(err, 'Could not update reservation.'),
    });
  }

  remove(res: DHCPReservation): void {
    this.confirmSvc
      .confirm({
        title: 'Delete reservation',
        message: `Delete the reservation for ${res.reserved_ip}?`,
        danger: true,
      })
      .subscribe((ok) => {
        if (!ok) return;
        this.api.deleteReservation(res.id).subscribe({
          next: () => {
            this.notify.success('Reservation deleted.');
            this.load();
          },
          error: (err) => this.notify.error(err, 'Could not delete reservation.'),
        });
      });
  }

  formatBindingType(type: BindingType): string {
    switch (type) {
      case 'mac':
        return 'MAC address';
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

  extractBindingValue(bindingKey: string): string {
    const parsed = parseBindingKey(bindingKey);
    if (parsed) {
      return parsed.value;
    }
    return bindingKey;
  }
}

// Simple pool selector dialog for when there are multiple pools
@Component({
  selector: 'app-pool-selector-dialog',
  standalone: true,
  imports: [MatDialogModule, MatListModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>Select pool</h2>
    <mat-dialog-content>
      <p>Which pool should this reservation belong to?</p>
      <mat-selection-list (selectionChange)="onSelect($event)">
        @for (pool of data.pools; track pool.id) {
          <mat-list-option [value]="pool">
            <strong>{{ pool.name }}</strong>
            <span class="gdhcp-muted" style="margin-left: 8px">{{ pool.range_start }}–{{ pool.range_end }}</span>
          </mat-list-option>
        }
      </mat-selection-list>
    </mat-dialog-content>
  `,
  styles: [
    `
      mat-list-option {
        height: 48px;
      }
    `,
  ],
})
export class PoolSelectorDialogComponent {
  constructor(
    private ref: MatDialogRef<PoolSelectorDialogComponent, DHCPPool | undefined>,
    @Inject(MAT_DIALOG_DATA) readonly data: { pools: DHCPPool[] },
  ) {}

  onSelect(event: MatSelectionListChange): void {
    const selected = event.options.find((opt) => opt.selected);
    if (selected) {
      this.ref.close(selected.value);
    }
  }
}
