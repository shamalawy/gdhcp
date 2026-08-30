import { ChangeDetectionStrategy, Component, effect, inject, input, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { Router } from '@angular/router';
import { forkJoin, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { UtilizationBarComponent } from '../../shared/components/utilization-bar/utilization-bar.component';
import { ApiService } from '../../shared/api.service';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { DHCPPool, DHCPScope } from '../../shared/models';
import { NotifyService } from '../shared/notify.service';
import { PoolFormDialogComponent, PoolFormResult } from '../shared/pool-form-dialog.component';

interface PoolRow {
  pool: DHCPPool;
  utilization: { used: number; total: number } | null;
}

@Component({
  selector: 'app-scope-pools-tab',
  standalone: true,
  imports: [EmptyStateComponent, UtilizationBarComponent, MatButtonModule, MatIconModule, MatProgressBarModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="header-row">
      <button mat-flat-button color="primary" (click)="openCreate()">
        <mat-icon>add</mat-icon> Create pool
      </button>
    </div>

    @if (loading()) {
      <mat-progress-bar mode="indeterminate" />
    }

    @if (!loading() && rows().length === 0) {
      <app-empty-state icon="storage" title="No pools yet" message="Create a pool to define the address range this scope leases from.">
        <button mat-stroked-button (click)="openCreate()">Create pool</button>
      </app-empty-state>
    } @else if (rows().length > 0) {
      <div class="table-wrap">
        <table class="gdhcp-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Range</th>
              <th>Priority</th>
              <th>Enabled</th>
              <th>Utilization</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            @for (row of rows(); track row.pool.id) {
              <tr class="clickable" (click)="open(row.pool.id)">
                <td>{{ row.pool.name }}</td>
                <td class="gdhcp-mono">{{ row.pool.range_start }} – {{ row.pool.range_end }}</td>
                <td>{{ row.pool.priority }}</td>
                <td>{{ row.pool.is_enabled ? 'Yes' : 'No' }}</td>
                <td class="util-cell">
                  @if (row.utilization) {
                    <app-utilization-bar [used]="row.utilization.used" [total]="row.utilization.total" [compact]="true" />
                  } @else {
                    <span class="gdhcp-muted">—</span>
                  }
                </td>
                <td class="actions" (click)="$event.stopPropagation()">
                  <button mat-icon-button (click)="openEdit(row.pool)" [attr.aria-label]="'Edit ' + row.pool.name">
                    <mat-icon>edit</mat-icon>
                  </button>
                  <button mat-icon-button (click)="remove(row.pool)" [attr.aria-label]="'Delete ' + row.pool.name">
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
      tr.clickable {
        cursor: pointer;
      }
      tr.clickable:hover {
        background: var(--hover-bg);
      }
      .util-cell {
        min-width: 140px;
      }
    `,
  ],
})
export class ScopePoolsTabComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmSvc = inject(ConfirmDialogService);
  private readonly notify = inject(NotifyService);
  private readonly router = inject(Router);

  readonly scope = input.required<DHCPScope>();

  readonly loading = signal(true);
  readonly rows = signal<PoolRow[]>([]);

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
    this.api.listPools(this.scope().id, { limit: 500 }).subscribe({
      next: (page) => this.hydrate(page.items),
      error: (err) => {
        this.loading.set(false);
        this.notify.error(err, 'Could not load pools.');
      },
    });
  }

  private hydrate(pools: DHCPPool[]): void {
    if (pools.length === 0) {
      this.rows.set([]);
      this.loading.set(false);
      return;
    }
    forkJoin(pools.map((p) => this.api.poolUtilization(p.id).pipe(catchError(() => of(null))))).subscribe((results) => {
      this.rows.set(
        pools.map((pool, i) => ({
          pool,
          // Amendment A20: use server's authoritative `available` field; do not recompute as
          // total - active - reserved, which double-counts leased reservations.
          utilization: results[i] ? { used: results[i]!.total - results[i]!.available, total: results[i]!.total } : null,
        })),
      );
      this.loading.set(false);
    });
  }

  open(poolId: string): void {
    this.router.navigate(['/dhcp/pools', poolId]);
  }

  openCreate(): void {
    this.dialog
      .open<PoolFormDialogComponent, { scopeNetwork: string }, PoolFormResult | undefined>(PoolFormDialogComponent, {
        data: { scopeNetwork: this.scope().network },
        width: '440px',
      })
      .afterClosed()
      .subscribe((result) => {
        if (!result) return;
        this.api.createPool(this.scope().id, result).subscribe({
          next: () => {
            this.notify.success('Pool created.');
            this.load();
          },
          error: (err) => this.notify.error(err, 'Could not create pool.'),
        });
      });
  }

  openEdit(pool: DHCPPool): void {
    this.dialog
      .open<PoolFormDialogComponent, { scopeNetwork: string; pool: DHCPPool }, PoolFormResult | undefined>(
        PoolFormDialogComponent,
        { data: { scopeNetwork: this.scope().network, pool }, width: '440px' },
      )
      .afterClosed()
      .subscribe((result) => {
        if (!result) return;
        this.api.updatePool(pool.id, result).subscribe({
          next: () => {
            this.notify.success('Pool updated.');
            this.load();
          },
          error: (err) => this.notify.error(err, 'Could not update pool.'),
        });
      });
  }

  remove(pool: DHCPPool): void {
    this.confirmSvc
      .confirm({
        title: 'Delete pool',
        message: `Delete pool "${pool.name}"? This also deletes its reservations and lease history.`,
        danger: true,
      })
      .subscribe((ok) => {
        if (!ok) return;
        this.api.deletePool(pool.id).subscribe({
          next: () => {
            this.notify.success('Pool deleted.');
            this.load();
          },
          error: (err) => this.notify.error(err, 'Could not delete pool.'),
        });
      });
  }
}
