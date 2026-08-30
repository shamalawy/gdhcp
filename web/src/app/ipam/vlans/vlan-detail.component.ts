import { ChangeDetectionStrategy, Component, DestroyRef, computed, effect, inject, input, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Router, RouterLink } from '@angular/router';
import { of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { ApiService } from '../../shared/api.service';
import { IPAMPrefix, Tag, VLAN, VLANGroup } from '../../shared/models';
import { Breadcrumb, PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PrefixTableComponent } from '../components/prefix-table.component';
import { VlanFormDialogComponent, VlanFormDialogData } from '../dialogs/vlan-form-dialog.component';
import { extractApiError } from '../util/api-error.util';

/** `/ipam/vlans/:id` — one VLAN and the prefixes assigned to it. */
@Component({
  selector: 'app-vlan-detail',
  standalone: true,
  imports: [
    RouterLink,
    MatButtonModule,
    MatIconModule,
    MatTooltipModule,
    EmptyStateComponent,
    PageHeaderComponent,
    PrefixTableComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (loading()) {
      <div class="gdhcp-card"><div class="skeleton-line" style="width: 60%"></div></div>
    } @else {
      @if (vlan(); as v) {
      <app-page-header [title]="'VLAN ' + v.vid + ' — ' + v.name" [breadcrumbs]="breadcrumbs()">
        <button mat-stroked-button (click)="openEdit(v)">
          <mat-icon>edit</mat-icon>
          Edit
        </button>
        <button mat-stroked-button color="warn" (click)="confirmDelete(v)">
          <mat-icon>delete</mat-icon>
          Delete
        </button>
      </app-page-header>

      <div class="gdhcp-card header-card">
        <div class="header-row">
          <span class="label">VLAN ID</span>
          <span class="value gdhcp-mono">{{ v.vid }}</span>
        </div>
        <div class="header-row">
          <span class="label">Name</span>
          <span class="value">{{ v.name }}</span>
        </div>
        <div class="header-row">
          <span class="label">Group</span>
          <span class="value">
            @if (v.group_name) {
              {{ v.group_name }}
              @if (group(); as g) {
                <span class="gdhcp-muted">(IDs {{ g.min_vid }}–{{ g.max_vid }})</span>
              }
            } @else {
              <span class="gdhcp-muted">Ungrouped — this VLAN ID is unique across the whole estate.</span>
            }
          </span>
        </div>
        <div class="header-row">
          <span class="label">Status</span>
          <span class="status-chip" [class]="'status-' + v.status">{{ v.status }}</span>
        </div>
        <div class="header-row">
          <span class="label">Description</span>
          <span class="value">{{ v.description || '—' }}</span>
        </div>
      </div>

      <h2 class="section-title">Prefixes on this VLAN</h2>
      @if (prefixes().length === 0) {
        <app-empty-state
          icon="lan"
          title="No prefixes assigned"
          message="Assign this VLAN from a prefix's edit dialog to record which address block serves the segment."
        />
      } @else {
        <div class="gdhcp-card">
          <app-prefix-table [items]="prefixes()" [tagsByPrefixId]="tagsByPrefixId()" />
        </div>
      }
      } @else {
        <p class="gdhcp-muted">VLAN not found.</p>
      }
    }
  `,
  styles: [
    `
      .header-card {
        display: flex;
        flex-direction: column;
        gap: 12px;
        margin-bottom: 24px;
      }

      .header-row {
        display: flex;
        align-items: flex-start;
        gap: 12px;
      }

      .label {
        flex: 0 0 100px;
        color: var(--text-secondary);
        font-size: 13px;
        padding-top: 2px;
      }

      .value {
        flex: 1;
        min-width: 0;
        overflow-wrap: anywhere;
      }

      .section-title {
        font-size: 15px;
        font-weight: 600;
        margin: 0 0 12px;
      }

      .status-chip {
        display: inline-block;
        font-size: 11px;
        font-weight: 600;
        text-transform: uppercase;
        padding: 2px 8px;
        border-radius: 10px;
        width: fit-content;
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
    `,
  ],
})
export class VlanDetailComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmDialog = inject(ConfirmDialogService);
  private readonly snackBar = inject(MatSnackBar);
  private readonly router = inject(Router);
  private readonly destroyRef = inject(DestroyRef);

  /** Bound from the `:id` route param via `withComponentInputBinding()`. */
  readonly id = input.required<string>();

  readonly vlan = signal<VLAN | null | undefined>(undefined);
  readonly group = signal<VLANGroup | null>(null);
  readonly prefixes = signal<IPAMPrefix[]>([]);
  readonly tagsByPrefixId = signal<Record<string, Tag[]>>({});
  readonly loading = signal(true);

  readonly breadcrumbs = computed<Breadcrumb[]>(() => [
    { label: 'IPAM', link: '/ipam' },
    { label: 'VLANs', link: '/ipam/vlans' },
    { label: this.vlan()?.name ?? '…' },
  ]);

  constructor() {
    effect(() => this.load(this.id()));
  }

  private load(id: string): void {
    this.loading.set(true);
    this.vlan.set(undefined);
    this.group.set(null);
    this.prefixes.set([]);

    this.api
      .getVLAN(id)
      .pipe(
        catchError(() => of(null)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((vlan) => {
        this.vlan.set(vlan);
        this.loading.set(false);
        if (vlan?.group_id) this.loadGroup(vlan.group_id);
      });

    this.api
      .listVLANPrefixes(id)
      .pipe(
        catchError(() => of([])),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((items) => this.prefixes.set(items));
  }

  /** No GET /vlan-groups/{id} wrapper is needed — the list is small and is
   * already the source the edit dialog uses. */
  private loadGroup(groupId: string): void {
    this.api
      .listVLANGroups({ limit: 200 })
      .pipe(
        catchError(() => of({ items: [] as VLANGroup[], total: 0, limit: 0, offset: 0 })),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((page) => this.group.set(page.items.find((g) => g.id === groupId) ?? null));
  }

  openEdit(vlan: VLAN): void {
    this.api.listVLANGroups({ limit: 200 }).subscribe((page) => {
      const data: VlanFormDialogData = { mode: 'edit', vlan, groups: page.items };
      this.dialog
        .open(VlanFormDialogComponent, { data, width: '520px' })
        .afterClosed()
        .subscribe((updated) => {
          if (updated) this.load(vlan.id);
        });
    });
  }

  confirmDelete(vlan: VLAN): void {
    const count = this.prefixes().length;
    this.confirmDialog
      .confirm({
        title: `Delete VLAN ${vlan.vid}?`,
        message:
          count > 0
            ? `${count} prefix${count === 1 ? '' : 'es'} will be unassigned from this VLAN. The prefixes themselves are not deleted.`
            : 'This VLAN has no prefixes assigned.',
        confirmText: 'Delete',
        danger: true,
      })
      .subscribe((confirmed) => {
        if (!confirmed) return;
        this.api.deleteVLAN(vlan.id).subscribe({
          next: () => void this.router.navigate(['/ipam', 'vlans']),
          error: (err) => this.snackBar.open(extractApiError(err, 'Could not delete this VLAN.').message, 'Dismiss', { duration: 6000 }),
        });
      });
  }
}
