import { ChangeDetectionStrategy, Component, DestroyRef, computed, effect, inject, input, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Router } from '@angular/router';
import { Observable, forkJoin, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { ApiService } from '../../shared/api.service';
import { IPAMPrefix, Tag, VLAN, VLANGroup, VRF } from '../../shared/models';
import { Breadcrumb, PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { DescriptionFieldComponent } from '../../shared/components/description-field/description-field.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { TagChipsComponent } from '../../shared/components/tag-chips/tag-chips.component';
import { PrefixTableComponent } from '../components/prefix-table.component';
import { EntityTagsService, PREFIX_ENTITY_TYPE, VLAN_ENTITY_TYPE } from '../data-access/entity-tags.service';
import { VlanFormDialogComponent, VlanFormDialogData } from '../dialogs/vlan-form-dialog.component';
import { addressCountForCidr } from '../util/cidr.util';
import { extractApiError } from '../util/api-error.util';
import { PREFIX_TYPE_META } from '../util/prefix-type.util';

const STATUS_HELP: Record<string, string> = {
  active: 'In service — assign subnets to it freely.',
  reserved: 'Held for future use; nothing should be assigned to it yet.',
  deprecated: 'Being retired — leave existing assignments alone, add no new ones.',
};

/** `/ipam/vlans/:id` — one VLAN and the prefixes assigned to it. */
@Component({
  selector: 'app-vlan-detail',
  standalone: true,
  imports: [
    MatButtonModule,
    MatIconModule,
    MatTooltipModule,
    DescriptionFieldComponent,
    EmptyStateComponent,
    PageHeaderComponent,
    TagChipsComponent,
    PrefixTableComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (loading()) {
      <div class="gdhcp-card skeleton-card">
        <div class="skeleton-line" style="width: 40%; height: 20px"></div>
        <div class="skeleton-line" style="width: 70%"></div>
        <div class="skeleton-line" style="width: 55%"></div>
      </div>
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
          <span class="value">
            <span class="status-chip" [class]="'status-' + v.status" [matTooltip]="statusHelp(v.status)">
              {{ v.status }}
            </span>
          </span>
        </div>
        <div class="header-row">
          <span class="label">Tags</span>
          <app-tag-chips
            [tags]="tags()"
            [editable]="true"
            (tagAdd)="addTag(v, $event)"
            (tagRemove)="removeTag(v, $event)"
          />
        </div>
        <div class="header-row">
          <span class="label">Description</span>
          <app-description-field [value]="v.description" (save)="saveDescription(v, $event)" />
        </div>
      </div>

      <h2 class="section-title">Prefixes on this VLAN</h2>
      <p class="section-subtitle">
        The IPAM blocks whose addresses live on this layer-2 segment. A VLAN can carry prefixes
        from more than one VRF, so each row names the routing instance it belongs to.
      </p>

      @if (loadingPrefixes()) {
        <div class="gdhcp-card skeleton-card">
          @for (i of skeletonRows; track i) {
            <div class="skeleton-line" style="width: 100%"></div>
          }
        </div>
      } @else if (prefixes().length === 0) {
        <app-empty-state
          icon="lan"
          title="No prefixes assigned"
          message="Assign this VLAN from a prefix's edit dialog to record which address block serves the segment."
        />
      } @else {
        <div class="stats">
          @for (stat of stats(); track stat.label) {
            <div class="gdhcp-card stat" [matTooltip]="stat.help">
              <span class="stat-value">{{ stat.value }}</span>
              <span class="stat-label">{{ stat.label }}</span>
            </div>
          }
        </div>

        <div class="gdhcp-card">
          <app-prefix-table
            [items]="prefixes()"
            [tagsByPrefixId]="tagsByPrefixId()"
            [vrfNamesById]="vrfNamesById()"
          />
        </div>
      }
      } @else {
        <app-empty-state
          icon="error_outline"
          title="VLAN not found"
          message="It may have been deleted, or the link may be out of date."
        />
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
        margin: 0 0 4px;
      }

      .section-subtitle {
        margin: 0 0 12px;
        font-size: 13px;
        color: var(--text-secondary);
        max-width: 80ch;
      }

      .stats {
        display: flex;
        flex-wrap: wrap;
        gap: 12px;
        margin-bottom: 12px;
      }

      .stat {
        display: flex;
        flex-direction: column;
        gap: 2px;
        min-width: 132px;
        padding: 12px 16px;
        cursor: help;
      }

      .stat-value {
        font-size: 20px;
        font-weight: 600;
        line-height: 1.2;
      }

      .stat-label {
        font-size: 12px;
        color: var(--text-secondary);
      }

      .status-chip {
        display: inline-block;
        font-size: 11px;
        font-weight: 600;
        text-transform: uppercase;
        padding: 2px 8px;
        border-radius: 10px;
        width: fit-content;
        cursor: help;
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

      /* .skeleton-line is not global — every page that shows a loading
         placeholder carries its own copy. Without it the "loading" card
         renders as an empty box, which is what this page used to do. */
      .skeleton-card {
        display: flex;
        flex-direction: column;
        gap: 12px;
      }

      .skeleton-line {
        height: 12px;
        border-radius: 4px;
        background: linear-gradient(90deg, var(--divider-color) 25%, var(--border-color) 50%, var(--divider-color) 75%);
        background-size: 200% 100%;
        animation: shimmer 1.4s ease-in-out infinite;
      }

      @keyframes shimmer {
        0% {
          background-position: 200% 0;
        }
        100% {
          background-position: -200% 0;
        }
      }
    `,
  ],
})
export class VlanDetailComponent {
  private readonly api = inject(ApiService);
  private readonly entityTags = inject(EntityTagsService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmDialog = inject(ConfirmDialogService);
  private readonly snackBar = inject(MatSnackBar);
  private readonly router = inject(Router);
  private readonly destroyRef = inject(DestroyRef);

  /** Bound from the `:id` route param via `withComponentInputBinding()`. */
  readonly id = input.required<string>();

  readonly skeletonRows = Array.from({ length: 4 }, (_, i) => i);

  readonly vlan = signal<VLAN | null | undefined>(undefined);
  readonly group = signal<VLANGroup | null>(null);
  readonly prefixes = signal<IPAMPrefix[]>([]);
  readonly tags = signal<Tag[]>([]);
  readonly tagsByPrefixId = signal<Record<string, Tag[]>>({});
  readonly vrfNamesById = signal<Record<string, string>>({});
  readonly loading = signal(true);
  readonly loadingPrefixes = signal(true);

  readonly breadcrumbs = computed<Breadcrumb[]>(() => [
    { label: 'IPAM', link: '/ipam' },
    { label: 'VLANs', link: '/ipam/vlans' },
    { label: this.vlan()?.name ?? '…' },
  ]);

  /** Roll-up of the prefixes on this segment, so the page answers "how big
   * is this VLAN?" without opening every prefix in turn. */
  readonly stats = computed(() => {
    const items = this.prefixes();
    const subnets = items.filter((p) => p.prefix_type === 'subnet').length;
    const vrfs = new Set(items.map((p) => p.vrf_id)).size;
    const addresses = items
      .filter((p) => p.prefix_type === 'subnet')
      .reduce((sum, p) => sum + (addressCountForCidr(p.prefix) ?? 0), 0);
    const measured = items.filter((p) => p.utilization_percentage !== null);
    const avgUtil = measured.length
      ? Math.round(measured.reduce((sum, p) => sum + (p.utilization_percentage ?? 0), 0) / measured.length)
      : null;

    return [
      {
        label: items.length === 1 ? 'Prefix' : 'Prefixes',
        value: String(items.length),
        help: 'IPAM blocks assigned to this VLAN, of every type.',
      },
      {
        label: subnets === 1 ? 'Subnet' : 'Subnets',
        value: String(subnets),
        help: PREFIX_TYPE_META.subnet.help,
      },
      {
        label: 'Addresses',
        value: addresses.toLocaleString(),
        help: 'Total address space across the subnets on this VLAN, network and broadcast included.',
      },
      {
        label: vrfs === 1 ? 'VRF' : 'VRFs',
        value: String(vrfs),
        help: 'Routing instances these prefixes belong to. More than one means the same segment is numbered in several VRFs.',
      },
      {
        label: 'Avg. DHCP use',
        value: avgUtil === null ? '—' : `${avgUtil}%`,
        help:
          avgUtil === null
            ? 'No prefix on this VLAN has a DHCP scope on or beneath it, so there is nothing to measure.'
            : 'Mean lease utilization across the prefixes here that have a DHCP scope.',
      },
    ];
  });

  // effect(), not ngOnInit: with withComponentInputBinding() the router
  // reuses this component instance when navigating between two VLANs, only
  // updating the `id` input — ngOnInit does not fire again. allowSignalWrites
  // is required because load() writes signals synchronously; without it
  // Angular throws NG0600 before the first request is ever made, which left
  // the page stuck on an (unstyled, so invisible) loading skeleton.
  constructor() {
    effect(() => this.load(this.id()), { allowSignalWrites: true });
  }

  private load(id: string): void {
    this.loading.set(true);
    this.loadingPrefixes.set(true);
    this.vlan.set(undefined);
    this.group.set(null);
    this.prefixes.set([]);
    this.tags.set([]);
    this.tagsByPrefixId.set({});
    this.vrfNamesById.set({});

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

    this.loadTags(id);

    this.api
      .listVLANPrefixes(id)
      .pipe(
        catchError(() => of([])),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((items) => {
        this.prefixes.set(items);
        this.loadingPrefixes.set(false);
        this.loadPrefixTags(items);
        this.loadVrfNames(items);
      });
  }

  private loadTags(id: string): void {
    this.entityTags
      .list(VLAN_ENTITY_TYPE, id)
      .pipe(
        catchError(() => of([])),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((tags) => this.tags.set(tags));
  }

  /** Per-prefix tags, one request each — there is no bulk "tags for many
   * entities" endpoint (see EntityTagsService). A VLAN's prefix list is
   * short and unpaginated server-side, so the fan-out stays small. */
  private loadPrefixTags(items: IPAMPrefix[]): void {
    if (!items.length) {
      this.tagsByPrefixId.set({});
      return;
    }
    const calls = items.reduce<Record<string, Observable<Tag[]>>>((acc, item) => {
      acc[item.id] = this.entityTags.list(PREFIX_ENTITY_TYPE, item.id).pipe(catchError(() => of([])));
      return acc;
    }, {});
    forkJoin(calls)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((result) => this.tagsByPrefixId.set(result));
  }

  /** `GET /vlans/{id}/prefixes` returns `vrf_id` but no VRF name, so the
   * table's VRF column needs one list call to resolve them. */
  private loadVrfNames(items: IPAMPrefix[]): void {
    if (!items.length) {
      this.vrfNamesById.set({});
      return;
    }
    this.api
      .listVRFs({ limit: 200 })
      .pipe(
        catchError(() => of({ items: [] as VRF[], total: 0, limit: 0, offset: 0 })),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((page) => {
        this.vrfNamesById.set(Object.fromEntries(page.items.map((vrf) => [vrf.id, vrf.name])));
      });
  }

  private loadGroup(groupId: string): void {
    this.api
      .listVLANGroups({ limit: 200 })
      .pipe(
        catchError(() => of({ items: [] as VLANGroup[], total: 0, limit: 0, offset: 0 })),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((page) => this.group.set(page.items.find((g) => g.id === groupId) ?? null));
  }

  statusHelp(status: string): string {
    return STATUS_HELP[status] ?? '';
  }

  saveDescription(vlan: VLAN, description: string | null): void {
    this.api
      .updateVLAN(vlan.id, {
        vid: vlan.vid,
        name: vlan.name,
        group_id: vlan.group_id,
        status: vlan.status,
        description,
      })
      .subscribe({
        next: (updated) => this.vlan.set(updated),
        error: (err) =>
          this.snackBar.open(extractApiError(err, 'Could not save the description.').message, 'Dismiss', {
            duration: 5000,
          }),
      });
  }

  addTag(vlan: VLAN, name: string): void {
    this.entityTags.addByName(VLAN_ENTITY_TYPE, vlan.id, name).subscribe({
      next: (tag) => this.tags.update((current) => (current.some((t) => t.id === tag.id) ? current : [...current, tag])),
      error: (err) =>
        this.snackBar.open(extractApiError(err, 'Could not add that tag.').message, 'Dismiss', { duration: 5000 }),
    });
  }

  removeTag(vlan: VLAN, tagId: string): void {
    this.entityTags.remove(tagId, VLAN_ENTITY_TYPE, vlan.id).subscribe({
      next: () => this.tags.update((current) => current.filter((t) => t.id !== tagId)),
      error: (err) =>
        this.snackBar.open(extractApiError(err, 'Could not remove that tag.').message, 'Dismiss', { duration: 5000 }),
    });
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
          error: (err) =>
            this.snackBar.open(extractApiError(err, 'Could not delete this VLAN.').message, 'Dismiss', {
              duration: 6000,
            }),
        });
      });
  }
}
