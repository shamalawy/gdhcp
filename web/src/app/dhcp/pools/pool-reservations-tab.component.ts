import { ChangeDetectionStrategy, Component, effect, inject, input, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';

import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { ApiService } from '../../shared/api.service';
import { BindingType, DHCPPool, DHCPReservation } from '../../shared/models';
import { NotifyService } from '../shared/notify.service';
import {
  ReservationFormDialogComponent,
  ReservationFormDialogData,
  ReservationFormResult,
} from '../shared/reservation-form-dialog.component';

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

/** The reservation home per product rule 5 — there is no global
 * reservations view; reservations are only ever managed here, per pool. */
@Component({
  selector: 'app-pool-reservations-tab',
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

    @if (!loading() && reservations().length === 0) {
      <app-empty-state icon="bookmark" title="No reservations" message="Reserve an IP for a client that should always get the same address.">
        <button mat-stroked-button (click)="openCreate()">Add reservation</button>
      </app-empty-state>
    } @else if (reservations().length > 0) {
      <div class="table-wrap">
        <table class="gdhcp-table">
          <thead>
            <tr>
              <th>IP</th>
              <th>Binding type</th>
              <th>Identifier</th>
              <th>Hostname</th>
              <th>Description</th>
              <th>Enabled</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            @for (res of reservations(); track res.id) {
              <tr>
                <td class="gdhcp-mono">{{ res.reserved_ip }}</td>
                <td class="binding-type">{{ formatBindingType(res.binding_type) }}</td>
                <td class="gdhcp-mono">{{ extractBindingValue(res.binding_key) }}</td>
                <td>{{ res.hostname || '—' }}</td>
                <td class="gdhcp-muted">{{ res.description || '—' }}</td>
                <td>
                  <mat-slide-toggle
                    [ngModel]="res.is_enabled"
                    (ngModelChange)="toggleEnabled(res, $event)"
                    [attr.aria-label]="'Enable reservation ' + res.reserved_ip"
                  />
                </td>
                <td class="actions">
                  <button mat-icon-button (click)="openEdit(res)" [attr.aria-label]="'Edit reservation ' + res.reserved_ip">
                    <mat-icon>edit</mat-icon>
                  </button>
                  <button mat-icon-button (click)="remove(res)" [attr.aria-label]="'Delete reservation ' + res.reserved_ip">
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
      .binding-type {
        font-size: 12px;
        text-transform: capitalize;
        color: var(--text-secondary);
      }
      .actions {
        text-align: right;
        white-space: nowrap;
      }
    `,
  ],
})
export class PoolReservationsTabComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmSvc = inject(ConfirmDialogService);
  private readonly notify = inject(NotifyService);

  readonly pool = input.required<DHCPPool>();

  readonly loading = signal(true);
  readonly reservations = signal<DHCPReservation[]>([]);

  constructor() {
    effect(
      () => {
        this.pool();
        this.load();
      },
      { allowSignalWrites: true },
    );
  }

  load(): void {
    this.loading.set(true);
    this.api.listReservations(this.pool().id, { limit: 500 }).subscribe({
      next: (page) => {
        this.reservations.set(page.items);
        this.loading.set(false);
      },
      error: (err) => {
        this.loading.set(false);
        this.notify.error(err, 'Could not load reservations.');
      },
    });
  }

  openCreate(): void {
    const pool = this.pool();
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
    const pool = this.pool();
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
