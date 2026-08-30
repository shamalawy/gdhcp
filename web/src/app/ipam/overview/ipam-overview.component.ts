import { ChangeDetectionStrategy, Component, DestroyRef, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatMenuModule } from '@angular/material/menu';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterLink } from '@angular/router';
import { Subject, forkJoin, of } from 'rxjs';
import { catchError, debounceTime, distinctUntilChanged, map } from 'rxjs/operators';

import { ApiService } from '../../shared/api.service';
import { Tag, VRF } from '../../shared/models';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { TagChipsComponent } from '../../shared/components/tag-chips/tag-chips.component';
import { UtilizationBarComponent } from '../../shared/components/utilization-bar/utilization-bar.component';
import { EntityTagsService } from '../data-access/entity-tags.service';
import { extractApiError } from '../util/api-error.util';
import { VrfFormDialogComponent, VrfFormDialogData } from '../dialogs/vrf-form-dialog.component';

interface VrfSummary {
  prefixCount: number | null;
  utilizationPct: number | null;
  tags: Tag[];
  loaded: boolean;
}

const PAGE_SIZE_OPTIONS = [12, 24, 48];

/** `/ipam` — VRF overview: cards for every VRF plus create/edit/delete. The
 * spine of IPAM navigation (per docs/design/frontend.md, "IPAM as the
 * spine") starts here. */
@Component({
  selector: 'app-ipam-overview',
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatMenuModule,
    MatPaginatorModule,
    MatTooltipModule,
    EmptyStateComponent,
    PageHeaderComponent,
    TagChipsComponent,
    UtilizationBarComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="IPAM — VRFs">
      <button mat-flat-button color="primary" (click)="openCreate()">
        <mat-icon>add</mat-icon>
        New VRF
      </button>
    </app-page-header>

    <div class="explainer gdhcp-card">
      <mat-icon>info</mat-icon>
      <p>
        A <strong>VRF</strong> (Virtual Routing and Forwarding instance) is an isolated IP address
        space. The same prefix — e.g. <span class="gdhcp-mono">10.0.0.0/8</span> — can exist
        independently in more than one VRF without conflicting, which is how gdhcp keeps
        overlapping customer, site, or environment networks apart. Every VRF below is a
        self-contained root for its own prefix hierarchy, DHCP scopes, and pools.
      </p>
      <p>
        VRFs isolate <strong>layer 3</strong>. For <strong>layer 2</strong>, see
        <a routerLink="/ipam/vlans">VLANs</a> — the 802.1Q segments individual subnets are
        assigned to.
      </p>
    </div>

    <mat-form-field appearance="outline" class="search-field" subscriptSizing="dynamic">
      <mat-icon matPrefix>search</mat-icon>
      <input matInput placeholder="Search VRFs…" [(ngModel)]="searchText" (ngModelChange)="onSearchChange($event)" />
    </mat-form-field>

    @if (loading()) {
      <div class="grid">
        @for (i of skeletonRows; track i) {
          <div class="gdhcp-card vrf-card skeleton-card">
            <div class="skeleton-line" style="width: 60%"></div>
            <div class="skeleton-line" style="width: 40%"></div>
            <div class="skeleton-line" style="width: 90%"></div>
            <div class="skeleton-line" style="width: 100%; height: 8px"></div>
          </div>
        }
      </div>
    } @else if (vrfs().length === 0) {
      <app-empty-state
        icon="lan"
        title="No VRFs yet"
        message="Create your first VRF to start building an IPAM prefix hierarchy."
      >
        <button mat-flat-button color="primary" (click)="openCreate()">
          <mat-icon>add</mat-icon>
          New VRF
        </button>
      </app-empty-state>
    } @else {
      <div class="grid">
        @for (vrf of vrfs(); track vrf.id) {
          <div class="gdhcp-card vrf-card">
            <div class="card-top">
              <a [routerLink]="['/ipam', 'vrfs', vrf.id]" class="vrf-name">{{ vrf.name }}</a>
              @if (vrf.is_default) {
                <span class="default-badge" matTooltip="Default VRF for prefixes/scopes without an explicit VRF">
                  default
                </span>
              }
              <span class="spacer"></span>
              <button mat-icon-button [matMenuTriggerFor]="menu" [attr.aria-label]="'Actions for ' + vrf.name">
                <mat-icon>more_vert</mat-icon>
              </button>
              <mat-menu #menu="matMenu">
                <button mat-menu-item (click)="openEdit(vrf)">
                  <mat-icon>edit</mat-icon>
                  <span>Edit</span>
                </button>
                <button mat-menu-item (click)="confirmDelete(vrf)">
                  <mat-icon>delete</mat-icon>
                  <span>Delete</span>
                </button>
              </mat-menu>
            </div>

            <p class="rd gdhcp-mono">{{ vrf.route_distinguisher || 'No route distinguisher' }}</p>

            @if (vrf.description) {
              <p class="description">{{ vrf.description }}</p>
            }

            <div class="stats-row">
              <span class="prefix-count">
                @if (summaryFor(vrf.id).loaded) {
                  {{ summaryFor(vrf.id).prefixCount }}
                  {{ summaryFor(vrf.id).prefixCount === 1 ? 'prefix' : 'prefixes' }}
                } @else {
                  <span class="skeleton-line inline" style="width: 60px"></span>
                }
              </span>
            </div>

            @if (summaryFor(vrf.id).utilizationPct !== null) {
              <app-utilization-bar [used]="summaryFor(vrf.id).utilizationPct!" [total]="100" [compact]="true" />
            }

            <app-tag-chips [tags]="summaryFor(vrf.id).tags" [editable]="false" />
          </div>
        }
      </div>

      <mat-paginator
        [length]="total()"
        [pageSize]="limit()"
        [pageSizeOptions]="pageSizeOptions"
        [pageIndex]="pageIndex()"
        (page)="onPage($event)"
        aria-label="VRF pages"
      />
    }
  `,
  styles: [
    `
      .explainer {
        display: flex;
        gap: 12px;
        align-items: flex-start;
        margin-bottom: 16px;
        color: var(--text-secondary);
        font-size: 13px;
      }

      .explainer mat-icon {
        color: var(--primary-color);
        flex: 0 0 auto;
      }

      .explainer p {
        margin: 0;
      }

      .search-field {
        width: 320px;
        max-width: 100%;
        margin-bottom: 16px;
      }

      .grid {
        display: grid;
        grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
        gap: 16px;
      }

      .vrf-card {
        display: flex;
        flex-direction: column;
        gap: 8px;
      }

      .card-top {
        display: flex;
        align-items: center;
        gap: 8px;
      }

      .vrf-name {
        font-size: 16px;
        font-weight: 600;
        text-decoration: none;
        color: var(--text-primary);
      }

      .vrf-name:hover {
        text-decoration: underline;
      }

      .default-badge {
        font-size: 10px;
        text-transform: uppercase;
        letter-spacing: 0.04em;
        color: var(--text-secondary);
        border: 1px solid var(--border-color);
        border-radius: 8px;
        padding: 1px 6px;
      }

      .spacer {
        flex: 1;
      }

      .rd {
        margin: 0;
        font-size: 12px;
        color: var(--text-secondary);
      }

      .description {
        margin: 0;
        font-size: 13px;
        color: var(--text-secondary);
        display: -webkit-box;
        -webkit-line-clamp: 2;
        -webkit-box-orient: vertical;
        overflow: hidden;
      }

      .stats-row {
        font-size: 12px;
        color: var(--text-secondary);
      }

      .skeleton-card {
        gap: 10px;
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
export class IpamOverviewComponent {
  private readonly api = inject(ApiService);
  private readonly entityTags = inject(EntityTagsService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmDialog = inject(ConfirmDialogService);
  private readonly snackBar = inject(MatSnackBar);
  private readonly destroyRef = inject(DestroyRef);

  readonly skeletonRows = Array.from({ length: 6 }, (_, i) => i);
  readonly pageSizeOptions = PAGE_SIZE_OPTIONS;

  readonly vrfs = signal<VRF[]>([]);
  readonly summaries = signal<Record<string, VrfSummary>>({});
  readonly loading = signal(true);
  readonly total = signal(0);
  readonly limit = signal(PAGE_SIZE_OPTIONS[1]);
  readonly pageIndex = signal(0);

  private static readonly EMPTY_SUMMARY: VrfSummary = {
    prefixCount: null,
    utilizationPct: null,
    tags: [],
    loaded: false,
  };

  searchText = '';
  private readonly search$ = new Subject<string>();

  summaryFor(vrfId: string): VrfSummary {
    return this.summaries()[vrfId] ?? IpamOverviewComponent.EMPTY_SUMMARY;
  }

  constructor() {
    this.loadVrfs();

    this.search$
      .pipe(debounceTime(300), distinctUntilChanged(), takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.pageIndex.set(0);
        this.loadVrfs();
      });
  }

  onSearchChange(value: string): void {
    this.search$.next(value);
  }

  onPage(event: PageEvent): void {
    this.pageIndex.set(event.pageIndex);
    this.limit.set(event.pageSize);
    this.loadVrfs();
  }

  private loadVrfs(): void {
    this.loading.set(true);
    this.api
      .listVRFs({
        limit: this.limit(),
        offset: this.pageIndex() * this.limit(),
        search: this.searchText.trim() || undefined,
        sort: 'name',
      })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.vrfs.set(res.items);
          this.total.set(res.total);
          this.loading.set(false);
          this.loadSummaries(res.items);
        },
        error: () => {
          this.loading.set(false);
          this.snackBar.open('Could not load VRFs.', 'Dismiss', { duration: 5000 });
        },
      });
  }

  private loadSummaries(vrfs: VRF[]): void {
    for (const vrf of vrfs) {
      forkJoin({
        prefixCount: this.api.listPrefixes({ vrf_id: vrf.id, limit: 1 }).pipe(
          map((res) => res.total),
          catchError(() => of(null)),
        ),
        // Top-level tree nodes already carry a server-rolled-up
        // utilization_percentage per prefix — averaging across them is an
        // approximation of whole-VRF utilization (no dedicated
        // VRF-utilization endpoint exists). See notes_for_integration.
        topNodes: this.api.ipamTree({ vrf_id: vrf.id, depth: 1 }).pipe(catchError(() => of([]))),
        tags: this.entityTags.list('vrf', vrf.id).pipe(catchError(() => of([]))),
      })
        .pipe(takeUntilDestroyed(this.destroyRef))
        .subscribe(({ prefixCount, topNodes, tags }) => {
          // A VRF with zero top-level prefixes comes back as JSON `null`,
          // not `[]` (a nil Go slice) — see IpamTreeNode's doc comment.
          const utilValues = (topNodes ?? [])
            .map((n) => n.prefix.utilization_percentage)
            .filter((v): v is number => v !== null);
          const utilizationPct = utilValues.length
            ? utilValues.reduce((sum, v) => sum + v, 0) / utilValues.length
            : null;
          this.summaries.update((current) => ({
            ...current,
            [vrf.id]: { prefixCount, utilizationPct, tags, loaded: true },
          }));
        });
    }
  }

  openCreate(): void {
    const ref = this.dialog.open<VrfFormDialogComponent, VrfFormDialogData, VRF | null>(VrfFormDialogComponent, {
      data: { mode: 'create' },
      width: '480px',
    });
    ref.afterClosed().subscribe((created) => {
      if (!created) return;
      this.snackBar.open(`VRF "${created.name}" created.`, undefined, { duration: 3000 });
      this.loadVrfs();
    });
  }

  openEdit(vrf: VRF): void {
    const ref = this.dialog.open<VrfFormDialogComponent, VrfFormDialogData, VRF | null>(VrfFormDialogComponent, {
      data: { mode: 'edit', vrf },
      width: '480px',
    });
    ref.afterClosed().subscribe((updated) => {
      if (!updated) return;
      this.vrfs.update((items) => items.map((v) => (v.id === updated.id ? updated : v)));
      this.snackBar.open(`VRF "${updated.name}" saved.`, undefined, { duration: 3000 });
    });
  }

  confirmDelete(vrf: VRF): void {
    this.confirmDialog
      .confirm({
        title: 'Delete VRF',
        message: `Delete "${vrf.name}"? This is only possible if it has no prefixes or scopes.`,
        confirmText: 'Delete',
        danger: true,
      })
      .subscribe((confirmed) => {
        if (!confirmed) return;
        this.api.deleteVRF(vrf.id).subscribe({
          next: () => {
            this.vrfs.update((items) => items.filter((v) => v.id !== vrf.id));
            this.total.update((t) => Math.max(0, t - 1));
            this.snackBar.open(`VRF "${vrf.name}" deleted.`, undefined, { duration: 3000 });
          },
          error: (err) => {
            const friendly = extractApiError(err, 'Could not delete this VRF.');
            this.snackBar.open(friendly.message, 'Dismiss', { duration: 6000 });
          },
        });
      });
  }
}
