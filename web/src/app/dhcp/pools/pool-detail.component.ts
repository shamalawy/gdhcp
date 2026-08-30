import { ChangeDetectionStrategy, Component, computed, effect, inject, input, signal } from '@angular/core';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatTabsModule } from '@angular/material/tabs';
import { forkJoin, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { ApiService } from '../../shared/api.service';
import { DHCPPool, DHCPScope, OptionValue } from '../../shared/models';
import { AttachedTemplate, fetchAttachedTemplates } from '../shared/template-attachment';
import { OptionLayer } from '../shared/effective-options.component';
import { NotifyService } from '../shared/notify.service';
import { PoolOverviewTabComponent } from './pool-overview-tab.component';
import { PoolReservationsTabComponent } from './pool-reservations-tab.component';
import { PoolLeasesTabComponent } from './pool-leases-tab.component';
import { PoolOptionsTabComponent } from './pool-options-tab.component';

/** Pool detail shell — the reservation home (product rule 5: no global
 * reservations view exists, reservations are managed per-pool only). Loads
 * the pool, its owning scope (for breadcrumb + inherited option layers),
 * and the scope's attached templates so the Options tab can show the full
 * effective view: templates → scope → pool. */
@Component({
  selector: 'app-pool-detail',
  standalone: true,
  imports: [
    PageHeaderComponent,
    MatProgressBarModule,
    MatTabsModule,
    PoolOverviewTabComponent,
    PoolReservationsTabComponent,
    PoolLeasesTabComponent,
    PoolOptionsTabComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (loading()) {
      <mat-progress-bar mode="indeterminate" />
    } @else {
      @if (pool(); as pool) {
        <app-page-header
          [title]="pool.name"
          [breadcrumbs]="
            scope()
              ? [
                  { label: 'Scopes', link: '/dhcp/scopes' },
                  { label: scope()!.name, link: '/dhcp/scopes/' + scope()!.id },
                  { label: pool.name },
                ]
              : [{ label: 'Scopes', link: '/dhcp/scopes' }, { label: pool.name }]
          "
        >
          <span class="subtitle gdhcp-mono">{{ pool.range_start }} – {{ pool.range_end }}</span>
        </app-page-header>

        <mat-tab-group animationDuration="150ms" [selectedIndex]="selectedTabIndex()">
          <mat-tab label="Overview">
            <ng-template matTabContent>
              <app-pool-overview-tab [pool]="pool" (updated)="onPoolUpdated($event)" />
            </ng-template>
          </mat-tab>
          <mat-tab label="Reservations">
            <ng-template matTabContent>
              <app-pool-reservations-tab [pool]="pool" />
            </ng-template>
          </mat-tab>
          <mat-tab label="Leases">
            <ng-template matTabContent>
              <app-pool-leases-tab [pool]="pool" />
            </ng-template>
          </mat-tab>
          <mat-tab label="Options">
            <ng-template matTabContent>
              <app-pool-options-tab [pool]="pool" [inheritedLayers]="inheritedLayers()" />
            </ng-template>
          </mat-tab>
        </mat-tab-group>
      } @else {
        <p class="gdhcp-muted">Pool not found.</p>
      }
    }
  `,
  styles: [
    `
      .subtitle {
        color: var(--text-secondary);
        font-size: 13px;
      }
    `,
  ],
})
export class PoolDetailComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);

  readonly id = input.required<string>();

  /** `?tab=` from the query string (withComponentInputBinding). Global
   * search links a reservation hit to this page's Reservations tab, since a
   * reservation has no page of its own. */
  readonly tab = input<string | undefined>(undefined);

  /** Tab order must match the <mat-tab> order in the template. */
  private static readonly TAB_NAMES = ['overview', 'reservations', 'leases', 'options'];

  readonly selectedTabIndex = computed(() => {
    const index = PoolDetailComponent.TAB_NAMES.indexOf((this.tab() ?? '').toLowerCase());
    return index >= 0 ? index : 0;
  });

  readonly loading = signal(true);
  readonly pool = signal<DHCPPool | null>(null);
  readonly scope = signal<DHCPScope | null>(null);
  readonly inheritedLayers = signal<OptionLayer[]>([]);

  constructor() {
    effect(
      () => {
        const poolId = this.id();
        this.loading.set(true);
        this.api.getPool(poolId).subscribe({
          next: (pool) => {
            this.pool.set(pool);
            this.loading.set(false);
            this.loadScopeContext(pool.scope_id);
          },
          error: (err) => {
            this.loading.set(false);
            this.pool.set(null);
            this.notify.error(err, 'Could not load pool.');
          },
        });
      },
      { allowSignalWrites: true },
    );
  }

  onPoolUpdated(pool: DHCPPool): void {
    this.pool.set(pool);
  }

  private loadScopeContext(scopeId: string): void {
    this.api.getScope(scopeId).subscribe({
      next: (scope) => this.scope.set(scope),
      error: () => this.scope.set(null),
    });

    forkJoin({
      scopeOptions: this.api.getScopeOptions(scopeId).pipe(catchError(() => of<OptionValue[]>([]))),
      templates: fetchAttachedTemplates(this.api, scopeId).pipe(catchError(() => of<AttachedTemplate[]>([]))),
    }).subscribe(({ scopeOptions, templates }) => {
      forkJoin(templates.map((t) => this.api.getTemplateOptions(t.template.id).pipe(catchError(() => of<OptionValue[]>([]))))).subscribe(
        (templateOptionSets) => {
          const templateLayers: OptionLayer[] = templates.map((t, i) => ({
            level: 'template',
            label: `Template: ${t.template.name} (priority ${t.assoc.priority})`,
            values: templateOptionSets[i] ?? [],
          }));
          this.inheritedLayers.set([...templateLayers, { level: 'scope', label: 'Scope', values: scopeOptions }]);
        },
      );
    });
  }
}
