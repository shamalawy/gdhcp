import { ChangeDetectionStrategy, Component, computed, effect, inject, input, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { Router, RouterLink } from '@angular/router';
import { EMPTY, Subject } from 'rxjs';
import { catchError, debounceTime, distinctUntilChanged, switchMap } from 'rxjs/operators';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';

import { ApiService } from '../../shared/api.service';
import { extractApiError } from '../util/api-error.util';
import { SearchEntityType, SearchResult } from '../../shared/models';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';

interface EntityTypeMeta {
  readonly label: string;
  readonly icon: string;
}

const ENTITY_TYPE_META: Readonly<Record<SearchEntityType, EntityTypeMeta>> = {
  vrf: { label: 'VRFs', icon: 'lan' },
  prefix: { label: 'Prefixes', icon: 'account_tree' },
  vlan: { label: 'VLANs', icon: 'settings_ethernet' },
  vlan_group: { label: 'VLAN groups', icon: 'workspaces' },
  scope: { label: 'DHCP scopes', icon: 'dns' },
  pool: { label: 'Pools', icon: 'water_drop' },
  reservation: { label: 'Reservations', icon: 'bookmark' },
  lease: { label: 'Leases', icon: 'schedule' },
  template: { label: 'Templates', icon: 'description' },
  option: { label: 'DHCP options', icon: 'list_alt' },
  cluster: { label: 'Clusters', icon: 'hub' },
};

const ENTITY_TYPE_ORDER: readonly SearchEntityType[] = [
  'vrf',
  'prefix',
  'vlan',
  'vlan_group',
  'scope',
  'pool',
  'reservation',
  'lease',
  'template',
  'option',
  'cluster',
];

interface ResultGroup {
  type: SearchEntityType | string;
  meta: EntityTypeMeta;
  results: SearchResult[];
}

const FALLBACK_META: EntityTypeMeta = { label: 'Other', icon: 'search' };

/**
 * `/ipam/search?q=` — full search results page, grouped by entity type with
 * icons and links. Also the target for the shell's Ctrl+K overlay "see all
 * results" (see notes_for_integration — wiring that link is layout.md's
 * shell.component.ts, outside this unit's ownership).
 */
@Component({
  selector: 'app-ipam-search',
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    EmptyStateComponent,
    PageHeaderComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="Search"></app-page-header>

    <mat-form-field appearance="outline" class="search-field">
      <mat-icon matPrefix>search</mat-icon>
      <input
        matInput
        placeholder="Search prefixes, VLANs, scopes, pools, reservations, leases, templates, options, clusters…"
        [(ngModel)]="queryText"
        (ngModelChange)="onQueryChange($event)"
      />
    </mat-form-field>

    @if (errorMessage()) {
      <app-empty-state icon="error_outline" title="Search failed" [message]="errorMessage()!" />
    } @else if (!queryText.trim()) {
      <app-empty-state icon="search" title="Search gdhcp" message="Type at least one character to search." />
    } @else if (loading()) {
      <div class="skeleton-list">
        @for (i of skeletonRows; track i) {
          <div class="skeleton-line" style="width: 100%"></div>
        }
      </div>
    } @else if (results().length === 0) {
      <app-empty-state
        icon="search_off"
        [title]="noResultsTitle()"
        message="Try a different name, CIDR, IP address, or MAC address."
      />
    } @else {
      @for (group of groups(); track group.type) {
        <section class="result-group">
          <h2 class="group-title">
            <mat-icon>{{ group.meta.icon }}</mat-icon>
            {{ group.meta.label }}
            <span class="count">{{ group.results.length }}</span>
          </h2>
          <div class="gdhcp-card group-card">
            @for (result of group.results; track result.entity_id) {
              <a [routerLink]="result.url" class="result-row">
                <span class="title">{{ result.title }}</span>
                @if (result.subtitle) {
                  <span class="subtitle">{{ result.subtitle }}</span>
                }
              </a>
            }
          </div>
        </section>
      }
    }
  `,
  styles: [
    `
      .search-field {
        width: 100%;
        max-width: 560px;
        margin-bottom: 20px;
      }

      .result-group {
        margin-bottom: 20px;
      }

      .group-title {
        display: flex;
        align-items: center;
        gap: 8px;
        font-size: 13px;
        font-weight: 600;
        text-transform: uppercase;
        letter-spacing: 0.04em;
        color: var(--text-secondary);
        margin: 0 0 8px;
      }

      .group-title mat-icon {
        font-size: 18px;
        width: 18px;
        height: 18px;
      }

      .count {
        font-weight: 400;
        text-transform: none;
        letter-spacing: normal;
      }

      .group-card {
        display: flex;
        flex-direction: column;
        padding: 4px;
      }

      .result-row {
        display: flex;
        flex-direction: column;
        gap: 2px;
        padding: 10px 12px;
        border-radius: 4px;
        text-decoration: none;
        color: var(--text-primary);
      }

      .result-row:hover {
        background: var(--hover-bg);
      }

      .result-row .title {
        font-size: 14px;
      }

      .result-row .subtitle {
        font-size: 12px;
        color: var(--text-secondary);
      }

      .skeleton-list {
        display: flex;
        flex-direction: column;
        gap: 10px;
      }

      .skeleton-line {
        height: 14px;
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
export class IpamSearchComponent {
  private readonly api = inject(ApiService);
  private readonly router = inject(Router);

  readonly skeletonRows = Array.from({ length: 6 }, (_, i) => i);

  /** Bound from the `?q=` query param via `withComponentInputBinding()`. */
  readonly q = input<string>('');

  queryText = '';
  readonly loading = signal(false);
  readonly results = signal<SearchResult[]>([]);
  readonly lastQuery = signal('');
  readonly errorMessage = signal<string | null>(null);

  private readonly query$ = new Subject<string>();

  readonly groups = computed<ResultGroup[]>(() => {
    const byType = new Map<string, SearchResult[]>();
    // GET /search answers `null` rather than `[]` for a no-match query;
    // iterating that threw and surfaced as a generic failure.
    for (const result of this.results() ?? []) {
      const bucket = byType.get(result.type) ?? [];
      bucket.push(result);
      byType.set(result.type, bucket);
    }
    const ordered: ResultGroup[] = [];
    for (const type of ENTITY_TYPE_ORDER) {
      const bucket = byType.get(type);
      if (bucket?.length) {
        ordered.push({ type, meta: ENTITY_TYPE_META[type], results: bucket });
        byType.delete(type);
      }
    }
    for (const [type, bucket] of byType) {
      ordered.push({ type, meta: FALLBACK_META, results: bucket });
    }
    return ordered;
  });

  readonly noResultsTitle = computed(() => `No results for "${this.lastQuery()}"`);

  constructor() {
    effect(() => {
      const initial = this.q();
      if (initial && !this.queryText) {
        this.queryText = initial;
        this.query$.next(initial);
      }
    });

    this.query$
      .pipe(
        debounceTime(250),
        distinctUntilChanged(),
        switchMap((query) => {
          const trimmed = query.trim();
          if (!trimmed) {
            this.errorMessage.set(null);
            return EMPTY;
          }
          this.loading.set(true);
          this.errorMessage.set(null);
          // Caught inside switchMap so one failed request doesn't tear down
          // the subscription and leave the page permanently unsearchable.
          return this.api.search(trimmed, undefined, 200).pipe(
            catchError((err: unknown) => {
              this.loading.set(false);
              this.results.set([]);
              this.errorMessage.set(extractApiError(err, 'Search failed. Please try again.').message);
              return EMPTY;
            }),
          );
        }),
        takeUntilDestroyed(),
      )
      .subscribe((results) => {
        this.loading.set(false);
        this.results.set(results ?? []);
        this.lastQuery.set(this.queryText.trim());
      });
  }

  onQueryChange(value: string): void {
    if (!value.trim()) {
      this.results.set([]);
      this.loading.set(false);
    }
    this.query$.next(value);
    void this.router.navigate([], {
      queryParams: { q: value.trim() || null },
      queryParamsHandling: 'merge',
      replaceUrl: true,
    });
  }
}
