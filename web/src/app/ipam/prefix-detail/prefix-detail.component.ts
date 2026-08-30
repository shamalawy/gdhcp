import { ChangeDetectionStrategy, Component, DestroyRef, computed, effect, inject, input, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Router, RouterLink } from '@angular/router';
import { Observable, forkJoin, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { ApiService } from '../../shared/api.service';
import { DHCPScope, IPAMPrefix, PrefixUtilization, Tag, VRF } from '../../shared/models';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { DescriptionFieldComponent } from '../../shared/components/description-field/description-field.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { Breadcrumb, PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { TagChipsComponent } from '../../shared/components/tag-chips/tag-chips.component';
import { UtilizationBarComponent } from '../../shared/components/utilization-bar/utilization-bar.component';
import { PrefixTableComponent } from '../components/prefix-table.component';
import { PrefixClientsComponent } from '../prefix-clients/prefix-clients.component';
import { EntityTagsService, PREFIX_ENTITY_TYPE } from '../data-access/entity-tags.service';
import { IpamExtraService, NextAvailableSubnetResponse } from '../data-access/ipam-extra.service';
import { addressCountForPrefixLen, parseCidr } from '../util/cidr.util';
import { extractApiError } from '../util/api-error.util';
import { PREFIX_TYPE_META } from '../util/prefix-type.util';
import { PrefixFormDialogComponent, PrefixFormDialogData } from '../dialogs/prefix-form-dialog.component';

const PAGE_SIZE_OPTIONS = [10, 25, 50];
const SUBNET_SIZE_CHOICES = [21, 22, 23, 24, 25, 26, 27, 28, 29, 30];

/**
 * `/ipam/prefixes/:id` — prefix detail: editable header (tags, description),
 * a utilization panel (counts + history sparkline), a next-available-subnet
 * helper, and a children table with "add child prefix" / "create DHCP scope
 * from this subnet".
 *
 * See notes_for_integration for the two endpoints this page depends on that
 * aren't in docs/api/endpoints.md's binding table
 * (`/ipam/next-available-subnet`, `/prefixes/{id}/utilization`) and the
 * assumed `parent_id` filter on `GET /prefixes` used for the children table
 * (same assumption as vrf-detail.component.ts).
 */
@Component({
  selector: 'app-prefix-detail',
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatPaginatorModule,
    MatProgressSpinnerModule,
    MatSelectModule,
    MatTooltipModule,
    DescriptionFieldComponent,
    EmptyStateComponent,
    PageHeaderComponent,
    TagChipsComponent,
    UtilizationBarComponent,
    PrefixTableComponent,
    PrefixClientsComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (loadingPrefix()) {
      <div class="header-skeleton gdhcp-card">
        <div class="skeleton-line" style="width: 30%; height: 24px"></div>
        <div class="skeleton-line" style="width: 60%"></div>
      </div>
    } @else {
    @if (prefix(); as prefix) {
      <app-page-header [title]="prefix.prefix" [breadcrumbs]="breadcrumbs()">
        <button mat-stroked-button (click)="openEdit(prefix)">
          <mat-icon>edit</mat-icon>
          Edit
        </button>
        <button mat-stroked-button color="warn" (click)="confirmDelete(prefix)">
          <mat-icon>delete</mat-icon>
          Delete
        </button>
        <button mat-stroked-button (click)="openAddChild(prefix)">
          <mat-icon>add</mat-icon>
          Add child prefix
        </button>
        @if (scope(); as scope) {
          <a mat-flat-button color="primary" [routerLink]="['/dhcp', 'scopes', scope.id]">
            <mat-icon>dns</mat-icon>
            Open DHCP scope
          </a>
        } @else {
          <span
            [matTooltip]="
              prefix.prefix_type === 'subnet'
                ? ''
                : 'Only subnet-type prefixes can host a DHCP scope. Change the type, or add a subnet child, first.'
            "
          >
            <button mat-flat-button color="primary" [disabled]="prefix.prefix_type !== 'subnet'" (click)="createScope(prefix)">
              <mat-icon>dns</mat-icon>
              Create DHCP scope
            </button>
          </span>
        }
      </app-page-header>

      <div class="gdhcp-card header-card">
        <div class="header-row">
          <span class="label">Name</span>
          <span class="value">{{ prefix.name || '—' }}</span>
        </div>
        <div class="header-row">
          <span class="label">Type</span>
          <span class="type-chip" [class]="typeMeta().chipClass" [matTooltip]="typeMeta().help">
            {{ typeMeta().label }}
          </span>
        </div>
        <div class="header-row">
          <span class="label">VRF</span>
          @if (vrf(); as vrf) {
            <a [routerLink]="['/ipam', 'vrfs', vrf.id]">{{ vrf.name }}</a>
          } @else {
            <span class="skeleton-line inline" style="width: 100px"></span>
          }
        </div>
        <div class="header-row">
          <span class="label">VLAN</span>
          @if (prefix.vlan_id) {
            <a class="value" [routerLink]="['/ipam', 'vlans', prefix.vlan_id]">
              VLAN {{ prefix.vlan_vid }}@if (prefix.vlan_name) { — {{ prefix.vlan_name }} }
            </a>
          } @else {
            <span class="value gdhcp-muted">Not assigned</span>
          }
        </div>
        <div class="header-row">
          <span class="label">DHCP scope</span>
          @if (scope(); as scope) {
            <a class="value" [routerLink]="['/dhcp', 'scopes', scope.id]">{{ scope.name }}</a>
          } @else if (prefix.prefix_type === 'subnet') {
            <span class="value gdhcp-muted">None — this subnet hands out no leases.</span>
          } @else {
            <span class="value gdhcp-muted">Scopes live on subnets, not {{ typeMeta().label.toLowerCase() }}s.</span>
          }
        </div>
        <div class="header-row">
          <span class="label">Description</span>
          <app-description-field [value]="prefix.description" (save)="saveDescription(prefix, $event)" />
        </div>
        <div class="header-row">
          <span class="label">Tags</span>
          <app-tag-chips
            [tags]="tags()"
            [editable]="true"
            (tagAdd)="addTag(prefix, $event)"
            (tagRemove)="removeTag(prefix, $event)"
          />
        </div>
      </div>

      <div class="panels">
        <div class="gdhcp-card util-panel">
          <h2 class="section-title">Utilization</h2>
          @if (utilizationLoading()) {
            <div class="skeleton-line" style="width: 100%"></div>
          } @else if (utilization()) {
            @let util = utilization()!;
            @if (prefix.prefix_type === 'subnet') {
              @if (util.scope_count === 0) {
                <!-- No scope means no pool, so the DHCP view is all zeroes.
                     Report the block's own capacity instead of "0 / 0". -->
                <app-utilization-bar [used]="0" [total]="usableHosts(util)" />
                <p class="gdhcp-muted small">
                  No DHCP scope manages this subnet — {{ usableHosts(util) }} usable addresses, none tracked.
                </p>
              } @else {
                <app-utilization-bar [used]="util.active" [total]="util.total" [sparkline]="historySparkline()" />
                <div class="util-counts">
                  <span><strong>{{ util.active }}</strong> used</span>
                  <span><strong>{{ util.reserved }}</strong> reserved</span>
                  <span><strong>{{ util.available }}</strong> free</span>
                  <span class="gdhcp-muted"><strong>{{ util.total }}</strong> total</span>
                </div>
              }
            } @else {
              <div class="util-block">
                <p class="util-block-label">
                  Address space allocated
                  <span class="gdhcp-muted">({{ util.child_prefix_count }} child prefix{{ util.child_prefix_count === 1 ? '' : 'es' }})</span>
                </p>
                <app-utilization-bar [used]="util.space_allocated" [total]="util.space_total" />
              </div>
              @if (util.scope_count > 0) {
                <div class="util-block">
                  <p class="util-block-label">
                    DHCP utilization across {{ util.scope_count }} scope{{ util.scope_count === 1 ? '' : 's' }} beneath this prefix
                  </p>
                  <app-utilization-bar [used]="util.active" [total]="util.total" [sparkline]="historySparkline()" />
                  <div class="util-counts">
                    <span><strong>{{ util.active }}</strong> used</span>
                    <span><strong>{{ util.reserved }}</strong> reserved</span>
                    <span><strong>{{ util.available }}</strong> free</span>
                    <span class="gdhcp-muted"><strong>{{ util.total }}</strong> total</span>
                  </div>
                </div>
              } @else {
                <p class="gdhcp-muted small">No DHCP scope exists anywhere beneath this prefix yet.</p>
              }
            }
          }
        </div>

        @if (prefix.prefix_type !== 'subnet') {
          <div class="gdhcp-card next-available-panel">
            <h2 class="section-title">Find next available subnet</h2>
            <p class="section-subtitle">
              Suggests an unused CIDR block of the chosen size inside {{ prefix.prefix }}.
            </p>
            <div class="size-row">
              <mat-form-field appearance="outline" subscriptSizing="dynamic">
                <mat-label>Size</mat-label>
                <mat-select [(ngModel)]="nextAvailableSize">
                  @for (size of sizeChoices(); track size) {
                    <mat-option [value]="size">/{{ size }} ({{ addressCount(size) }} addresses)</mat-option>
                  }
                </mat-select>
              </mat-form-field>
              <button mat-stroked-button [disabled]="findingNextAvailable()" (click)="findNextAvailable(prefix)">
                @if (findingNextAvailable()) {
                  <mat-spinner diameter="18" />
                } @else {
                  Find
                }
              </button>
            </div>

            @if (nextAvailableError()) {
              <p class="error-text">{{ nextAvailableError() }}</p>
            }
            @if (nextAvailableResult(); as result) {
              <div class="suggestion-row">
                <span class="gdhcp-mono">{{ result.subnet }}</span>
                @if (result.available) {
                  <button mat-flat-button color="primary" (click)="useSuggestion(prefix, result)">Use this CIDR</button>
                } @else {
                  <span class="gdhcp-muted">No space available at this size.</span>
                }
              </div>
            }
          </div>
        }
      </div>

      <h2 class="section-title">DHCP clients</h2>
      <p class="section-subtitle">Leases and reservations in this prefix and its descendants.</p>
      <div class="gdhcp-card">
        <app-prefix-clients [prefixId]="prefix.id" />
      </div>

      <h2 class="section-title">Children</h2>
      <p class="section-subtitle">Prefixes nested directly under {{ prefix.prefix }}.</p>

      @if (loadingChildren()) {
        <div class="table-skeleton gdhcp-card">
          @for (i of skeletonRows; track i) {
            <div class="skeleton-line" style="width: 100%"></div>
          }
        </div>
      } @else if (children().length === 0) {
        <app-empty-state icon="account_tree" title="No child prefixes" message="This prefix has no children yet.">
          <button mat-flat-button color="primary" (click)="openAddChild(prefix)">
            <mat-icon>add</mat-icon>
            Add child prefix
          </button>
        </app-empty-state>
      } @else {
        <div class="gdhcp-card">
          <app-prefix-table [items]="children()" [tagsByPrefixId]="childTags()" />
        </div>

        <mat-paginator
          [length]="childTotal()"
          [pageSize]="childLimit()"
          [pageSizeOptions]="pageSizeOptions"
          [pageIndex]="childPageIndex()"
          (page)="onChildPage($event)"
          aria-label="Child prefix pages"
        />
      }
    } @else {
      <app-empty-state icon="error_outline" title="Prefix not found" message="It may have been deleted." />
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

      .type-chip {
        display: inline-block;
        font-size: 11px;
        font-weight: 600;
        letter-spacing: 0.02em;
        text-transform: uppercase;
        padding: 2px 8px;
        border-radius: 10px;
        cursor: help;
        width: fit-content;
      }

      .chip-aggregate {
        background: var(--bg-highlight);
        color: var(--primary-color);
      }
      .chip-container {
        background: color-mix(in srgb, var(--accent-color) 20%, transparent);
        color: var(--accent-color);
      }
      .chip-subnet {
        background: color-mix(in srgb, var(--status-ok) 20%, transparent);
        color: var(--status-ok);
      }

      .panels {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
        gap: 16px;
        margin-bottom: 24px;
      }

      .util-counts {
        display: flex;
        gap: 16px;
        margin-top: 12px;
        font-size: 13px;
        flex-wrap: wrap;
      }

      .util-block + .util-block {
        margin-top: 20px;
      }

      .util-block-label {
        margin: 0 0 8px;
        font-size: 13px;
        font-weight: 600;
      }

      .util-block-label .gdhcp-muted {
        font-weight: 400;
      }

      .small {
        font-size: 12px;
      }

      .size-row {
        display: flex;
        align-items: center;
        gap: 12px;
        margin-top: 8px;
      }

      .suggestion-row {
        display: flex;
        align-items: center;
        gap: 12px;
        margin-top: 12px;
      }

      .error-text {
        color: var(--status-error);
        font-size: 12px;
        margin-top: 8px;
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
      }

      .header-skeleton,
      .table-skeleton {
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

      .skeleton-line.inline {
        display: inline-block;
        vertical-align: middle;
        height: 10px;
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
export class PrefixDetailComponent {
  private readonly api = inject(ApiService);
  private readonly entityTags = inject(EntityTagsService);
  private readonly ipamExtra = inject(IpamExtraService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmDialog = inject(ConfirmDialogService);
  private readonly snackBar = inject(MatSnackBar);
  private readonly destroyRef = inject(DestroyRef);
  private readonly router = inject(Router);

  /** Bound from the `:id` route param via `withComponentInputBinding()`. */
  readonly id = input.required<string>();

  readonly skeletonRows = Array.from({ length: 5 }, (_, i) => i);
  readonly pageSizeOptions = PAGE_SIZE_OPTIONS;

  readonly prefix = signal<IPAMPrefix | null | undefined>(undefined);
  readonly loadingPrefix = signal(true);
  readonly vrf = signal<VRF | null>(null);
  readonly tags = signal<Tag[]>([]);

  /** The DHCP scope carved from this prefix, if one exists. Backs the
   * cross-link into /dhcp; `dhcp_scopes.ipam_prefix_id` has always been a
   * NOT NULL FK, but no page rendered the relationship in either direction. */
  readonly scope = signal<DHCPScope | null>(null);

  readonly utilization = signal<PrefixUtilization | null>(null);
  readonly utilizationLoading = signal(true);
  readonly historyPoints = signal<number[]>([]);
  readonly historySparkline = computed(() => (this.historyPoints().length > 1 ? this.historyPoints() : null));

  readonly children = signal<IPAMPrefix[]>([]);
  readonly childTags = signal<Record<string, Tag[]>>({});
  readonly loadingChildren = signal(true);
  readonly childTotal = signal(0);
  readonly childLimit = signal(PAGE_SIZE_OPTIONS[1]);
  readonly childPageIndex = signal(0);

  nextAvailableSize = SUBNET_SIZE_CHOICES[3]; // /24 default
  readonly findingNextAvailable = signal(false);
  readonly nextAvailableResult = signal<NextAvailableSubnetResponse | null>(null);
  readonly nextAvailableError = signal<string | null>(null);

  readonly breadcrumbs = computed<Breadcrumb[]>(() => {
    const prefix = this.prefix();
    const vrf = this.vrf();
    const crumbs: Breadcrumb[] = [{ label: 'IPAM', link: '/ipam' }];
    if (vrf) crumbs.push({ label: vrf.name, link: `/ipam/vrfs/${vrf.id}` });
    if (prefix) crumbs.push({ label: prefix.name || prefix.prefix });
    return crumbs;
  });

  readonly sizeChoices = computed(() => {
    const prefix = this.prefix();
    const parsed = prefix ? parseCidr(prefix.prefix) : null;
    const minSize = parsed ? parsed.prefixLen + 1 : SUBNET_SIZE_CHOICES[0];
    return SUBNET_SIZE_CHOICES.filter((size) => size >= minSize);
  });

  // effect() (not ngOnInit): with withComponentInputBinding(), the router
  // reuses this component instance across `/ipam/prefixes/:id` navigations
  // between prefixes (e.g. clicking a child-prefix link), only updating the
  // `id` input signal — ngOnInit does not fire again, so a load() called
  // only from ngOnInit would leave the whole page frozen on the previous
  // prefix's data. Reacting to `this.id()` in an effect re-runs load() on
  // every id change, including the first.
  constructor() {
    effect(
      () => {
        const id = this.id();
        this.load(id);
      },
      { allowSignalWrites: true },
    );
  }

  private load(id: string): void {
    // Reset paginator + reactive state so nothing from the previous prefix
    // is visible while the new prefix's data is in flight.
    this.prefix.set(undefined);
    this.vrf.set(null);
    this.tags.set([]);
    this.scope.set(null);
    this.utilization.set(null);
    this.historyPoints.set([]);
    this.children.set([]);
    this.childTags.set({});
    this.childTotal.set(0);
    this.childPageIndex.set(0);
    this.nextAvailableResult.set(null);
    this.nextAvailableError.set(null);

    this.loadingPrefix.set(true);
    this.utilizationLoading.set(true);
    this.loadingChildren.set(true);
    this.api.getPrefix(id).subscribe({
      next: (prefix) => {
        this.prefix.set(prefix);
        this.loadingPrefix.set(false);
        this.api.getVRF(prefix.vrf_id).subscribe({
          next: (vrf) => this.vrf.set(vrf),
          error: () => this.vrf.set(null),
        });
        this.loadChildren(prefix);
        this.loadUtilization(prefix);
        this.loadScope(prefix);
      },
      error: () => {
        this.prefix.set(null);
        this.loadingPrefix.set(false);
        this.utilizationLoading.set(false);
        this.loadingChildren.set(false);
      },
    });

    this.entityTags
      .list(PREFIX_ENTITY_TYPE, id)
      .pipe(
        catchError(() => of([])),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((tags) => this.tags.set(tags));
  }

  private loadScope(prefix: IPAMPrefix): void {
    if (prefix.prefix_type !== 'subnet') {
      this.scope.set(null);
      return;
    }
    this.api
      .listScopes({ ipam_prefix_id: prefix.id, limit: 1 })
      .pipe(
        catchError(() => of(null)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((page) => this.scope.set(page?.items[0] ?? null));
  }

  private loadUtilization(prefix: IPAMPrefix): void {
    this.utilizationLoading.set(true);
    this.ipamExtra
      .prefixUtilization(prefix.id)
      .pipe(
        catchError(() => of(null)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((summary) => {
        this.utilization.set(summary);
        this.utilizationLoading.set(false);
      });

    this.ipamExtra
      .prefixUtilizationHistory(prefix.id, 14)
      .pipe(
        catchError(() => of([])),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((points) => this.historyPoints.set(points.map((p) => p.utilization_percentage)));
  }

  private loadChildren(prefix: IPAMPrefix): void {
    this.loadingChildren.set(true);
    this.api
      .listPrefixes({
        vrf_id: prefix.vrf_id,
        parent_id: prefix.id,
        limit: this.childLimit(),
        offset: this.childPageIndex() * this.childLimit(),
        sort: 'prefix',
      })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.children.set(res.items);
          this.childTotal.set(res.total);
          this.loadingChildren.set(false);
          this.loadChildTags(res.items);
        },
        error: () => {
          this.loadingChildren.set(false);
          this.snackBar.open('Could not load child prefixes.', 'Dismiss', { duration: 5000 });
        },
      });
  }

  private loadChildTags(items: IPAMPrefix[]): void {
    if (!items.length) {
      this.childTags.set({});
      return;
    }
    const calls = items.reduce<Record<string, Observable<Tag[]>>>((acc, item) => {
      acc[item.id] = this.entityTags.list(PREFIX_ENTITY_TYPE, item.id).pipe(catchError(() => of([])));
      return acc;
    }, {});
    forkJoin(calls)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((result) => this.childTags.set(result));
  }

  onChildPage(event: PageEvent): void {
    this.childPageIndex.set(event.pageIndex);
    this.childLimit.set(event.pageSize);
    const current = this.prefix();
    if (current) this.loadChildren(current);
  }

  typeMeta() {
    const prefix = this.prefix();
    return prefix ? PREFIX_TYPE_META[prefix.prefix_type] : PREFIX_TYPE_META.subnet;
  }

  addressCount(size: number): number {
    return addressCountForPrefixLen(size);
  }

  /** Usable host addresses in this block: its size less network and
   * broadcast, which /31 and /32 don't have (RFC 3021). */
  usableHosts(util: PrefixUtilization): number {
    return util.space_total > 2 ? util.space_total - 2 : util.space_total;
  }

  saveDescription(prefix: IPAMPrefix, description: string | null): void {
    this.api.updatePrefix(prefix.id, { name: prefix.name, description, type: prefix.prefix_type }).subscribe({
      next: (updated) => this.prefix.set(updated),
      error: (err) => {
        const friendly = extractApiError(err, 'Could not save the description.');
        this.snackBar.open(friendly.message, 'Dismiss', { duration: 5000 });
      },
    });
  }

  addTag(prefix: IPAMPrefix, name: string): void {
    this.entityTags.addByName(PREFIX_ENTITY_TYPE, prefix.id, name).subscribe({
      next: (tag) => this.tags.update((current) => [...current, tag]),
      error: (err) => {
        const friendly = extractApiError(err, 'Could not add that tag.');
        this.snackBar.open(friendly.message, 'Dismiss', { duration: 5000 });
      },
    });
  }

  removeTag(prefix: IPAMPrefix, tagId: string): void {
    this.entityTags.remove(tagId, PREFIX_ENTITY_TYPE, prefix.id).subscribe({
      next: () => this.tags.update((current) => current.filter((t) => t.id !== tagId)),
      error: (err) => {
        const friendly = extractApiError(err, 'Could not remove that tag.');
        this.snackBar.open(friendly.message, 'Dismiss', { duration: 5000 });
      },
    });
  }

  openEdit(prefix: IPAMPrefix): void {
    const vrf = this.vrf();
    const ref = this.dialog.open<PrefixFormDialogComponent, PrefixFormDialogData, IPAMPrefix | null>(
      PrefixFormDialogComponent,
      {
        data: { vrfId: prefix.vrf_id, vrfName: vrf?.name ?? '', mode: 'edit', prefix },
        width: '520px',
      },
    );
    ref.afterClosed().subscribe((updated) => {
      if (!updated) return;
      this.prefix.set(updated);
      this.snackBar.open(`Prefix ${updated.prefix} saved.`, undefined, { duration: 3000 });
    });
  }

  openAddChild(prefix: IPAMPrefix): void {
    const vrf = this.vrf();
    const ref = this.dialog.open<PrefixFormDialogComponent, PrefixFormDialogData, IPAMPrefix | null>(
      PrefixFormDialogComponent,
      {
        data: { vrfId: prefix.vrf_id, vrfName: vrf?.name ?? '', mode: 'create', parentPrefix: prefix },
        width: '520px',
      },
    );
    ref.afterClosed().subscribe((created) => {
      if (!created) return;
      this.snackBar.open(`Prefix ${created.prefix} created.`, undefined, { duration: 3000 });
      this.loadChildren(prefix);
    });
  }

  confirmDelete(prefix: IPAMPrefix): void {
    this.confirmDialog
      .confirm({
        title: 'Delete prefix',
        message: `Delete ${prefix.prefix}? This is only possible if it has no child prefixes.`,
        confirmText: 'Delete',
        danger: true,
      })
      .subscribe((confirmed) => {
        if (!confirmed) return;
        this.api.deletePrefix(prefix.id).subscribe({
          next: () => {
            this.snackBar.open(`Prefix ${prefix.prefix} deleted.`, undefined, { duration: 3000 });
            void this.router.navigate(['/ipam', 'vrfs', prefix.vrf_id]);
          },
          error: (err) => {
            const friendly = extractApiError(err, 'Could not delete this prefix.');
            this.snackBar.open(friendly.message, 'Dismiss', { duration: 6000 });
          },
        });
      });
  }

  createScope(prefix: IPAMPrefix): void {
    if (prefix.prefix_type !== 'subnet') return;
    void this.router.navigate(['/dhcp/scopes/new'], { queryParams: { prefix_id: prefix.id } });
  }

  findNextAvailable(prefix: IPAMPrefix): void {
    this.findingNextAvailable.set(true);
    this.nextAvailableError.set(null);
    this.nextAvailableResult.set(null);
    this.ipamExtra.nextAvailableSubnet(prefix.id, this.nextAvailableSize).subscribe({
      next: (result) => {
        this.findingNextAvailable.set(false);
        this.nextAvailableResult.set(result);
      },
      error: (err) => {
        this.findingNextAvailable.set(false);
        const friendly = extractApiError(err, 'Could not find a next available subnet.');
        this.nextAvailableError.set(friendly.message);
      },
    });
  }

  useSuggestion(prefix: IPAMPrefix, suggestion: NextAvailableSubnetResponse): void {
    const vrf = this.vrf();
    const ref = this.dialog.open<PrefixFormDialogComponent, PrefixFormDialogData, IPAMPrefix | null>(
      PrefixFormDialogComponent,
      {
        data: {
          vrfId: prefix.vrf_id,
          vrfName: vrf?.name ?? '',
          mode: 'create',
          parentPrefix: prefix,
          initialCidr: suggestion.subnet,
        },
        width: '520px',
      },
    );
    ref.afterClosed().subscribe((created) => {
      if (!created) return;
      this.snackBar.open(`Prefix ${created.prefix} created.`, undefined, { duration: 3000 });
      this.loadChildren(prefix);
    });
  }
}
