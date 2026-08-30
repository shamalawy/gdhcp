import { ChangeDetectionStrategy, Component, DestroyRef, computed, inject, input, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterLink } from '@angular/router';
import { of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { ApiService } from '../../shared/api.service';
import { VLAN, VLANGroup, VLANStatus } from '../../shared/models';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { VlanFormDialogComponent, VlanFormDialogData } from '../dialogs/vlan-form-dialog.component';

/** `/ipam/vlans` — the VLAN inventory. VLANs record layer-2 segmentation;
 * VRFs (the other axis of this section) record layer-3 routing isolation. */
@Component({
  selector: 'app-vlans-list',
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatTableModule,
    MatTooltipModule,
    EmptyStateComponent,
    PageHeaderComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="VLANs" [breadcrumbs]="[{ label: 'IPAM', link: '/ipam' }, { label: 'VLANs' }]">
      <button mat-flat-button color="primary" (click)="openCreate()">
        <mat-icon>add</mat-icon>
        New VLAN
      </button>
    </app-page-header>

    <p class="intro gdhcp-muted">
      A VLAN is one 802.1Q broadcast domain (ID 1–4094). Assign one to an IPAM subnet to record
      which layer-2 segment that address block serves. Groups let the same ID exist in more than
      one domain.
    </p>

    <div class="filters">
      <mat-form-field appearance="outline" subscriptSizing="dynamic">
        <mat-icon matPrefix>search</mat-icon>
        <input matInput placeholder="Search by name or VLAN ID" [(ngModel)]="search" (ngModelChange)="reload()" />
      </mat-form-field>

      <mat-form-field appearance="outline" subscriptSizing="dynamic">
        <mat-label>Group</mat-label>
        <mat-select [(ngModel)]="groupFilter" (selectionChange)="reload()">
          <mat-option [value]="''">All groups</mat-option>
          <mat-option value="none">Ungrouped</mat-option>
          @for (group of groups(); track group.id) {
            <mat-option [value]="group.id">{{ group.name }}</mat-option>
          }
        </mat-select>
      </mat-form-field>

      <mat-form-field appearance="outline" subscriptSizing="dynamic">
        <mat-label>Status</mat-label>
        <mat-select [(ngModel)]="statusFilter" (selectionChange)="reload()">
          <mat-option [value]="''">Any status</mat-option>
          @for (status of statuses; track status) {
            <mat-option [value]="status">{{ status }}</mat-option>
          }
        </mat-select>
      </mat-form-field>
    </div>

    @if (loading()) {
      <div class="gdhcp-card"><div class="skeleton-line" style="width: 100%"></div></div>
    } @else if (vlans().length === 0) {
      <app-empty-state
        icon="lan"
        title="No VLANs yet"
        message="Create a VLAN to start recording which layer-2 segment each subnet serves."
      />
    } @else {
      <div class="gdhcp-card table-scroll">
        <table mat-table [dataSource]="vlans()" class="vlan-table">
          <ng-container matColumnDef="vid">
            <th mat-header-cell *matHeaderCellDef>VLAN ID</th>
            <td mat-cell *matCellDef="let row">
              <a [routerLink]="['/ipam', 'vlans', row.id]" class="gdhcp-mono vid-link">{{ row.vid }}</a>
            </td>
          </ng-container>

          <ng-container matColumnDef="name">
            <th mat-header-cell *matHeaderCellDef>Name</th>
            <td mat-cell *matCellDef="let row">{{ row.name }}</td>
          </ng-container>

          <ng-container matColumnDef="group">
            <th mat-header-cell *matHeaderCellDef>Group</th>
            <td mat-cell *matCellDef="let row">{{ row.group_name || '—' }}</td>
          </ng-container>

          <ng-container matColumnDef="status">
            <th mat-header-cell *matHeaderCellDef>Status</th>
            <td mat-cell *matCellDef="let row">
              <span class="status-chip" [class]="'status-' + row.status">{{ row.status }}</span>
            </td>
          </ng-container>

          <ng-container matColumnDef="prefixes">
            <th mat-header-cell *matHeaderCellDef>Prefixes</th>
            <td mat-cell *matCellDef="let row">{{ row.prefix_count }}</td>
          </ng-container>

          <ng-container matColumnDef="description">
            <th mat-header-cell *matHeaderCellDef>Description</th>
            <td mat-cell *matCellDef="let row">
              <span class="description-cell" [matTooltip]="row.description || ''">{{ row.description || '—' }}</span>
            </td>
          </ng-container>

          <tr mat-header-row *matHeaderRowDef="columns"></tr>
          <tr mat-row *matRowDef="let row; columns: columns" class="clickable-row" [routerLink]="['/ipam', 'vlans', row.id]"></tr>
        </table>
      </div>
    }
  `,
  styles: [
    `
      .intro {
        font-size: 13px;
        max-width: 70ch;
        margin: 0 0 16px;
      }

      .filters {
        display: flex;
        flex-wrap: wrap;
        gap: 12px;
        margin-bottom: 16px;
      }

      .table-scroll {
        overflow-x: auto;
      }

      .vlan-table {
        width: 100%;
        background: transparent;
      }

      .vid-link {
        font-weight: 600;
        text-decoration: none;
      }

      .vid-link:hover {
        text-decoration: underline;
      }

      .clickable-row {
        cursor: pointer;
      }

      .clickable-row:hover {
        background: var(--hover-bg);
      }

      .status-chip {
        display: inline-block;
        font-size: 11px;
        font-weight: 600;
        text-transform: uppercase;
        padding: 2px 8px;
        border-radius: 10px;
      }

      .status-active {
        background: color-mix(in srgb, var(--status-ok) 20%, transparent);
        color: var(--status-ok);
      }

      .status-reserved {
        background: color-mix(in srgb, var(--status-warn) 20%, transparent);
        color: var(--status-warn);
      }

      .status-deprecated {
        background: var(--bg-highlight);
        color: var(--text-secondary);
      }

      .description-cell {
        display: inline-block;
        max-width: 320px;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        vertical-align: bottom;
      }
    `,
  ],
})
export class VlansListComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly destroyRef = inject(DestroyRef);

  readonly columns = ['vid', 'name', 'group', 'status', 'prefixes', 'description'];
  readonly statuses: VLANStatus[] = ['active', 'reserved', 'deprecated'];

  readonly vlans = signal<VLAN[]>([]);
  readonly groups = signal<VLANGroup[]>([]);
  readonly loading = signal(true);

  search = '';
  groupFilter = '';
  statusFilter = '';

  /** `?group=` from the query string (withComponentInputBinding). Global
   * search links a VLAN-group hit to this list pre-filtered to that group,
   * since groups have no detail page of their own. */
  readonly group = input<string>('');

  constructor() {
    // Applied once, before the first load, so the initial request already
    // carries the filter instead of firing twice.
    const initialGroup = this.group();
    if (initialGroup) this.groupFilter = initialGroup;

    this.api
      .listVLANGroups({ limit: 200 })
      .pipe(
        catchError(() => of({ items: [], total: 0, limit: 0, offset: 0 })),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((page) => this.groups.set(page.items));
    this.reload();
  }

  reload(): void {
    this.loading.set(true);
    this.api
      .listVLANs({
        search: this.search.trim() || undefined,
        group_id: this.groupFilter || undefined,
        status: this.statusFilter || undefined,
        limit: 200,
      })
      .pipe(
        catchError(() => of({ items: [], total: 0, limit: 0, offset: 0 })),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((page) => {
        this.vlans.set(page.items);
        this.loading.set(false);
      });
  }

  openCreate(): void {
    const data: VlanFormDialogData = { mode: 'create', groups: this.groups() };
    this.dialog
      .open(VlanFormDialogComponent, { data, width: '520px' })
      .afterClosed()
      .subscribe((created) => {
        if (created) this.reload();
      });
  }
}
