import { ChangeDetectionStrategy, Component, EventEmitter, Output, effect, inject, input, signal } from '@angular/core';

import { DescriptionFieldComponent } from '../../shared/components/description-field/description-field.component';
import { UtilizationBarComponent } from '../../shared/components/utilization-bar/utilization-bar.component';
import { ApiService } from '../../shared/api.service';
import { DHCPPool, UtilizationSummary } from '../../shared/models';
import { NotifyService } from '../shared/notify.service';
import { TagEditorComponent } from '../shared/tag-editor.component';

/** Pool has no dedicated Settings tab in the product spec (name/range/
 * priority are edited from the owning scope's Pools tab, since a pool's
 * range must be validated against the scope's network) — description and
 * tags are still every-entity-supports-them fields, so they're editable
 * right here per the quality bar. */
@Component({
  selector: 'app-pool-overview-tab',
  standalone: true,
  imports: [DescriptionFieldComponent, TagEditorComponent, UtilizationBarComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="overview">
      <section class="gdhcp-card">
        <h3>Utilization</h3>
        @if (utilization(); as u) {
          <!-- Amendment A20: use server's authoritative 'available' field (total - active - reservedUnleased).
               do not recompute as total - active - reserved, which double-counts leased reservations. -->
          <app-utilization-bar [used]="u.total - u.available" [total]="u.total" />
          <p class="detail gdhcp-muted">{{ u.active }} active · {{ u.reserved }} reserved · {{ u.available }} available</p>
        } @else {
          <p class="gdhcp-muted">Loading…</p>
        }
      </section>

      <section class="gdhcp-card">
        <h3>Range &amp; priority</h3>
        <dl class="facts">
          <dt>Range</dt>
          <dd class="gdhcp-mono">{{ pool().range_start }} – {{ pool().range_end }}</dd>
          <dt>Priority</dt>
          <dd>{{ pool().priority }} <span class="gdhcp-muted">(lower is tried first)</span></dd>
          <dt>Enabled</dt>
          <dd>{{ pool().is_enabled ? 'Yes' : 'No' }}</dd>
        </dl>
        <p class="gdhcp-muted hint">Edit range/priority from this pool's scope → Pools tab.</p>
      </section>

      <section class="gdhcp-card">
        <h3>Description</h3>
        <app-description-field [value]="pool().description" (save)="saveDescription($event)" />
      </section>

      <section class="gdhcp-card">
        <h3>Tags</h3>
        <app-tag-editor entityType="pool" [entityId]="pool().id" />
      </section>
    </div>
  `,
  styles: [
    `
      .overview {
        display: flex;
        flex-direction: column;
        gap: 16px;
        max-width: 560px;
      }
      h3 {
        margin: 0 0 12px;
        font-size: 14px;
      }
      .detail {
        font-size: 12px;
        margin: 8px 0 0;
      }
      .facts {
        display: grid;
        grid-template-columns: 100px 1fr;
        row-gap: 6px;
        margin: 0;
        font-size: 13px;
      }
      .facts dt {
        color: var(--text-secondary);
      }
      .facts dd {
        margin: 0;
      }
      .hint {
        font-size: 12px;
        margin: 12px 0 0;
      }
    `,
  ],
})
export class PoolOverviewTabComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);

  readonly pool = input.required<DHCPPool>();
  @Output() readonly updated = new EventEmitter<DHCPPool>();

  readonly utilization = signal<UtilizationSummary | null>(null);

  constructor() {
    effect(() => {
      this.api.poolUtilization(this.pool().id).subscribe({
        next: (u) => this.utilization.set(u),
        error: (err) => this.notify.error(err, 'Could not load utilization.'),
      });
    });
  }

  saveDescription(description: string | null): void {
    this.api.updatePool(this.pool().id, { description }).subscribe({
      next: (pool) => {
        this.updated.emit(pool);
        this.notify.success('Description updated.');
      },
      error: (err) => this.notify.error(err, 'Could not update description.'),
    });
  }
}
