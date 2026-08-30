import { ChangeDetectionStrategy, Component, DestroyRef, OnInit, inject, input, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Router } from '@angular/router';
import { Observable, forkJoin, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { ApiService } from '../../shared/api.service';
import { IPAMPrefix, Tag, VRF } from '../../shared/models';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { DescriptionFieldComponent } from '../../shared/components/description-field/description-field.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { TagChipsComponent } from '../../shared/components/tag-chips/tag-chips.component';
import { PrefixTableComponent } from '../components/prefix-table.component';
import { EntityTagsService, PREFIX_ENTITY_TYPE } from '../data-access/entity-tags.service';
import { extractApiError } from '../util/api-error.util';
import { PrefixFormDialogComponent, PrefixFormDialogData } from '../dialogs/prefix-form-dialog.component';
import { VrfFormDialogComponent, VrfFormDialogData } from '../dialogs/vrf-form-dialog.component';

const PAGE_SIZE_OPTIONS = [10, 25, 50];

/**
 * `/ipam/vrfs/:id` — VRF detail: editable header (route distinguisher,
 * description, tags) plus a table of this VRF's top-level prefixes, with
 * "add prefix".
 *
 * "Top-level" = prefixes with `parent_id IS NULL` for this VRF.
 * `docs/api/endpoints.md`'s `/prefixes` list documents `limit, offset,
 * sort, search, vrf_id, type` only — no `parent_id` filter. This unit
 * assumes `GET /prefixes` additionally accepts `parent_id`, where the
 * literal value `"root"` means "no parent" and any other value is an exact
 * `parent_id` match (used again on the prefix detail page for the children
 * table). See notes_for_integration.
 */
@Component({
  selector: 'app-vrf-detail',
  standalone: true,
  imports: [
    MatButtonModule,
    MatIconModule,
    MatPaginatorModule,
    DescriptionFieldComponent,
    EmptyStateComponent,
    PageHeaderComponent,
    TagChipsComponent,
    PrefixTableComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (loadingVrf()) {
      <div class="header-skeleton gdhcp-card">
        <div class="skeleton-line" style="width: 30%; height: 24px"></div>
        <div class="skeleton-line" style="width: 60%"></div>
      </div>
    } @else {
    @if (vrf(); as vrf) {
      <app-page-header [title]="vrf.name" [breadcrumbs]="[{ label: 'IPAM', link: '/ipam' }, { label: vrf.name }]">
        <button mat-stroked-button (click)="openEditVrf(vrf)">
          <mat-icon>edit</mat-icon>
          Edit VRF
        </button>
        <button mat-stroked-button color="warn" (click)="confirmDeleteVrf(vrf)">
          <mat-icon>delete</mat-icon>
          Delete VRF
        </button>
        <button mat-flat-button color="primary" (click)="openAddPrefix(vrf)">
          <mat-icon>add</mat-icon>
          Add prefix
        </button>
      </app-page-header>

      <div class="gdhcp-card header-card">
        <div class="header-row">
          <span class="label">Route distinguisher</span>
          <span class="value gdhcp-mono">{{ vrf.route_distinguisher || 'Not set' }}</span>
        </div>
        @if (vrf.is_default) {
          <div class="header-row">
            <span class="default-badge">Default VRF</span>
            <span class="gdhcp-muted"
              >Prefixes and scopes created without an explicit VRF are placed here.</span
            >
          </div>
        }
        <div class="header-row">
          <span class="label">Description</span>
          <app-description-field
            [value]="vrf.description"
            (save)="saveDescription(vrf, $event)"
          />
        </div>
        <div class="header-row">
          <span class="label">Tags</span>
          <app-tag-chips [tags]="tags()" [editable]="true" (tagAdd)="addTag(vrf, $event)" (tagRemove)="removeTag(vrf, $event)" />
        </div>
      </div>

      <h2 class="section-title">Top-level prefixes</h2>
      <p class="section-subtitle">
        The prefixes rooted directly under this VRF. A prefix nests under another automatically
        when its CIDR falls inside it — this table shows only the ones with no parent.
      </p>

      @if (loadingPrefixes()) {
        <div class="table-skeleton gdhcp-card">
          @for (i of skeletonRows; track i) {
            <div class="skeleton-line" style="width: 100%"></div>
          }
        </div>
      } @else if (prefixes().length === 0) {
        <app-empty-state
          icon="account_tree"
          title="No top-level prefixes yet"
          message="Add an aggregate or subnet to start building this VRF's address hierarchy."
        >
          <button mat-flat-button color="primary" (click)="openAddPrefix(vrf)">
            <mat-icon>add</mat-icon>
            Add prefix
          </button>
        </app-empty-state>
      } @else {
        <div class="gdhcp-card">
          <app-prefix-table [items]="prefixes()" [tagsByPrefixId]="prefixTags()" />
        </div>

        <mat-paginator
          [length]="total()"
          [pageSize]="limit()"
          [pageSizeOptions]="pageSizeOptions"
          [pageIndex]="pageIndex()"
          (page)="onPage($event)"
          aria-label="Prefix pages"
        />
      }
    } @else {
      <app-empty-state icon="error_outline" title="VRF not found" message="It may have been deleted." />
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
      }

      .default-badge {
        font-size: 10px;
        text-transform: uppercase;
        letter-spacing: 0.04em;
        color: var(--text-secondary);
        border: 1px solid var(--border-color);
        border-radius: 8px;
        padding: 1px 6px;
        flex: 0 0 auto;
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
export class VrfDetailComponent implements OnInit {
  private readonly api = inject(ApiService);
  private readonly entityTags = inject(EntityTagsService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmDialog = inject(ConfirmDialogService);
  private readonly snackBar = inject(MatSnackBar);
  private readonly destroyRef = inject(DestroyRef);
  private readonly router = inject(Router);

  /** Bound from the `:id` route param via `withComponentInputBinding()`
   * (app.config.ts). */
  readonly id = input.required<string>();

  readonly skeletonRows = Array.from({ length: 5 }, (_, i) => i);
  readonly pageSizeOptions = PAGE_SIZE_OPTIONS;

  readonly vrf = signal<VRF | null | undefined>(undefined);
  readonly loadingVrf = signal(true);
  readonly tags = signal<Tag[]>([]);

  readonly prefixes = signal<IPAMPrefix[]>([]);
  readonly prefixTags = signal<Record<string, Tag[]>>({});
  readonly loadingPrefixes = signal(true);
  readonly total = signal(0);
  readonly limit = signal(PAGE_SIZE_OPTIONS[1]);
  readonly pageIndex = signal(0);

  // NOT in the constructor: with withComponentInputBinding(), the Router
  // assigns the route-bound `id` input signal after the component is
  // constructed but before ngOnInit runs. Reading `this.id()` synchronously
  // in the constructor throws NG0950 ("input is required but no value is
  // available yet") because the value isn't set yet at that point.
  ngOnInit(): void {
    this.loadVrf();
    this.loadPrefixes();
  }

  private loadVrf(): void {
    this.loadingVrf.set(true);
    this.api.getVRF(this.id()).subscribe({
      next: (vrf) => {
        this.vrf.set(vrf);
        this.loadingVrf.set(false);
      },
      error: () => {
        this.vrf.set(null);
        this.loadingVrf.set(false);
      },
    });
    this.entityTags
      .list('vrf', this.id())
      .pipe(
        catchError(() => of([])),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((tags) => this.tags.set(tags));
  }

  private loadPrefixes(): void {
    this.loadingPrefixes.set(true);
    this.api
      .listPrefixes({
        vrf_id: this.id(),
        parent_id: 'root',
        limit: this.limit(),
        offset: this.pageIndex() * this.limit(),
        sort: 'prefix',
      })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.prefixes.set(res.items);
          this.total.set(res.total);
          this.loadingPrefixes.set(false);
          this.loadPrefixTags(res.items);
        },
        error: () => {
          this.loadingPrefixes.set(false);
          this.snackBar.open('Could not load prefixes for this VRF.', 'Dismiss', { duration: 5000 });
        },
      });
  }

  private loadPrefixTags(items: IPAMPrefix[]): void {
    if (!items.length) {
      this.prefixTags.set({});
      return;
    }
    const calls = items.reduce<Record<string, Observable<Tag[]>>>((acc, item) => {
      acc[item.id] = this.entityTags.list(PREFIX_ENTITY_TYPE, item.id).pipe(catchError(() => of([])));
      return acc;
    }, {});
    forkJoin(calls)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((result) => this.prefixTags.set(result));
  }

  onPage(event: PageEvent): void {
    this.pageIndex.set(event.pageIndex);
    this.limit.set(event.pageSize);
    this.loadPrefixes();
  }

  saveDescription(vrf: VRF, description: string | null): void {
    this.api
      .updateVRF(vrf.id, { name: vrf.name, route_distinguisher: vrf.route_distinguisher, description })
      .subscribe({
        next: (updated) => this.vrf.set(updated),
        error: (err) => {
          const friendly = extractApiError(err, 'Could not save the description.');
          this.snackBar.open(friendly.message, 'Dismiss', { duration: 5000 });
        },
      });
  }

  addTag(vrf: VRF, name: string): void {
    this.entityTags.addByName('vrf', vrf.id, name).subscribe({
      next: (tag) => this.tags.update((current) => [...current, tag]),
      error: (err) => {
        const friendly = extractApiError(err, 'Could not add that tag.');
        this.snackBar.open(friendly.message, 'Dismiss', { duration: 5000 });
      },
    });
  }

  removeTag(vrf: VRF, tagId: string): void {
    this.entityTags.remove(tagId, 'vrf', vrf.id).subscribe({
      next: () => this.tags.update((current) => current.filter((t) => t.id !== tagId)),
      error: (err) => {
        const friendly = extractApiError(err, 'Could not remove that tag.');
        this.snackBar.open(friendly.message, 'Dismiss', { duration: 5000 });
      },
    });
  }

  openEditVrf(vrf: VRF): void {
    const ref = this.dialog.open<VrfFormDialogComponent, VrfFormDialogData, VRF | null>(VrfFormDialogComponent, {
      data: { mode: 'edit', vrf },
      width: '480px',
    });
    ref.afterClosed().subscribe((updated) => {
      if (!updated) return;
      this.vrf.set(updated);
      this.snackBar.open(`VRF "${updated.name}" saved.`, undefined, { duration: 3000 });
    });
  }

  confirmDeleteVrf(vrf: VRF): void {
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
            this.snackBar.open(`VRF "${vrf.name}" deleted.`, undefined, { duration: 3000 });
            void this.router.navigate(['/ipam']);
          },
          error: (err) => {
            const friendly = extractApiError(err, 'Could not delete this VRF.');
            this.snackBar.open(friendly.message, 'Dismiss', { duration: 6000 });
          },
        });
      });
  }

  openAddPrefix(vrf: VRF): void {
    const ref = this.dialog.open<PrefixFormDialogComponent, PrefixFormDialogData, IPAMPrefix | null>(
      PrefixFormDialogComponent,
      {
        data: { vrfId: vrf.id, vrfName: vrf.name, mode: 'create' },
        width: '520px',
      },
    );
    ref.afterClosed().subscribe((created) => {
      if (!created) return;
      this.snackBar.open(`Prefix ${created.prefix} created.`, undefined, { duration: 3000 });
      this.loadPrefixes();
    });
  }
}
