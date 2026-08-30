import { ChangeDetectionStrategy, Component, computed, effect, inject, input, signal } from '@angular/core';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatTabsModule } from '@angular/material/tabs';
import { Router } from '@angular/router';

import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { ApiService } from '../../shared/api.service';
import { Cluster, DHCPScope, VRF } from '../../shared/models';
import { AttachedTemplate, fetchAttachedTemplates } from '../shared/template-attachment';
import { NotifyService } from '../shared/notify.service';
import { ScopeOverviewTabComponent } from './scope-overview-tab.component';
import { ScopePoolsTabComponent } from './scope-pools-tab.component';
import { ScopeOptionsTabComponent } from './scope-options-tab.component';
import { ScopeReservationsTabComponent } from './scope-reservations-tab.component';
import { ScopeTemplatesTabComponent } from './scope-templates-tab.component';
import { ScopeSettingsTabComponent } from './scope-settings-tab.component';

/** Scope detail shell: loads the scope + its VRF/cluster names, then hosts
 * the six tabs. Attached-template resolution (needed by both the Options
 * tab, for inherited layers, and the Templates tab, for the reorder table)
 * is fetched once here and shared — see `loadAttachedTemplates` for the
 * N+1 workaround this requires (ApiService has no `GET /scopes/{id}/
 * templates`; only the reverse `GET /templates/{id}/scopes` exists). */
@Component({
  selector: 'app-scope-detail',
  standalone: true,
  imports: [
    PageHeaderComponent,
    MatProgressBarModule,
    MatTabsModule,
    ScopeOverviewTabComponent,
    ScopePoolsTabComponent,
    ScopeOptionsTabComponent,
    ScopeReservationsTabComponent,
    ScopeTemplatesTabComponent,
    ScopeSettingsTabComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (loading()) {
      <mat-progress-bar mode="indeterminate" />
    } @else {
      @if (scope(); as scope) {
        <app-page-header
          [title]="scope.name"
          [breadcrumbs]="[{ label: 'Scopes', link: '/dhcp/scopes' }, { label: scope.name }]"
        >
          <span class="subtitle gdhcp-mono">{{ scope.network }}</span>
          <span class="subtitle">{{ vrfName() }} · {{ clusterName() }}</span>
        </app-page-header>

        <mat-tab-group animationDuration="150ms">
          <mat-tab label="Overview">
            <ng-template matTabContent>
              <app-scope-overview-tab [scope]="scope" />
            </ng-template>
          </mat-tab>
          <mat-tab label="Pools">
            <ng-template matTabContent>
              <app-scope-pools-tab [scope]="scope" />
            </ng-template>
          </mat-tab>
          <mat-tab label="Options">
            <ng-template matTabContent>
              <app-scope-options-tab [scope]="scope" [attachedTemplates]="attachedTemplates()" />
            </ng-template>
          </mat-tab>
          <mat-tab label="Reservations">
            <ng-template matTabContent>
              <app-scope-reservations-tab [scope]="scope" />
            </ng-template>
          </mat-tab>
          <mat-tab label="Templates">
            <ng-template matTabContent>
              <app-scope-templates-tab
                [scope]="scope"
                [attachedTemplates]="attachedTemplates()"
                (changed)="loadAttachedTemplates(scope.id)"
              />
            </ng-template>
          </mat-tab>
          <mat-tab label="Settings">
            <ng-template matTabContent>
              <app-scope-settings-tab [scope]="scope" (updated)="onScopeUpdated($event)" (deleted)="onScopeDeleted()" />
            </ng-template>
          </mat-tab>
        </mat-tab-group>
      } @else {
        <p class="gdhcp-muted">Scope not found.</p>
      }
    }
  `,
  styles: [
    `
      .subtitle {
        color: var(--text-secondary);
        font-size: 13px;
        margin-right: 12px;
      }
    `,
  ],
})
export class ScopeDetailComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);
  private readonly router = inject(Router);

  readonly id = input.required<string>();

  readonly loading = signal(true);
  readonly scope = signal<DHCPScope | null>(null);
  readonly attachedTemplates = signal<AttachedTemplate[]>([]);

  private readonly vrfs = signal<VRF[]>([]);
  private readonly clusters = signal<Cluster[]>([]);
  readonly vrfName = computed(() => this.vrfs().find((v) => v.id === this.scope()?.vrf_id)?.name ?? '');
  readonly clusterName = computed(() => this.clusters().find((c) => c.id === this.scope()?.cluster_id)?.name ?? '');

  constructor() {
    this.api.listVRFs({ limit: 200 }).subscribe((res) => this.vrfs.set(res.items));
    this.api.listClusters({ limit: 200 }).subscribe((res) => this.clusters.set(res.items));

    effect(
      () => {
        const scopeId = this.id();
        this.loading.set(true);
        this.api.getScope(scopeId).subscribe({
          next: (scope) => {
            this.scope.set(scope);
            this.loading.set(false);
            this.loadAttachedTemplates(scope.id);
          },
          error: (err) => {
            this.loading.set(false);
            this.scope.set(null);
            this.notify.error(err, 'Could not load scope.');
          },
        });
      },
      { allowSignalWrites: true },
    );
  }

  /** N+1 across every template (see template-attachment.ts docstring) —
   * acceptable for a management UI with a modest template count; flagged in
   * notes_for_integration as a candidate for a dedicated endpoint. */
  loadAttachedTemplates(scopeId: string): void {
    fetchAttachedTemplates(this.api, scopeId).subscribe((attached) => this.attachedTemplates.set(attached));
  }

  onScopeUpdated(scope: DHCPScope): void {
    this.scope.set(scope);
  }

  onScopeDeleted(): void {
    this.router.navigate(['/dhcp/scopes']);
  }
}
