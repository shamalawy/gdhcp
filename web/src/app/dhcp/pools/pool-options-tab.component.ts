import { ChangeDetectionStrategy, Component, effect, inject, input, signal } from '@angular/core';

import { ApiService } from '../../shared/api.service';
import { DHCPPool, OptionValue } from '../../shared/models';
import { EffectiveOptionsComponent, OptionLayer } from '../shared/effective-options.component';
import { NotifyService } from '../shared/notify.service';

/** Pool Options tab: this pool's own values layered over the owning
 * scope's (and its templates') values, passed down from `PoolDetailComponent`.
 * Pool options have no PUT endpoint (only POST "set" and DELETE per
 * endpoints.md), so edits re-POST via `setPoolOption` — an upsert on
 * (pool_id, option_code) — rather than referencing the row id. */
@Component({
  selector: 'app-pool-options-tab',
  standalone: true,
  imports: [EffectiveOptionsComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-effective-options
      ownLevel="pool"
      ownLabel="This pool"
      [ownValues]="ownValues()"
      [inheritedLayers]="inheritedLayers()"
      (add)="onSet($event)"
      (update)="onSet($event)"
      (remove)="onRemove($event)"
    />
  `,
})
export class PoolOptionsTabComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);

  readonly pool = input.required<DHCPPool>();
  readonly inheritedLayers = input<OptionLayer[]>([]);

  readonly ownValues = signal<OptionValue[]>([]);

  constructor() {
    effect(() => {
      this.pool();
      this.refresh();
    });
  }

  onSet(result: { option_code: number; value: string }): void {
    this.api.setPoolOption(this.pool().id, { option_code: result.option_code, value: result.value }).subscribe({
      next: () => {
        this.notify.success('Option value set.');
        this.refresh();
      },
      error: (err) => this.notify.error(err, 'Could not set option value.'),
    });
  }

  onRemove(value: OptionValue): void {
    this.api.removePoolOption(this.pool().id, value.id).subscribe({
      next: () => {
        this.notify.success('Option value removed.');
        this.refresh();
      },
      error: (err) => this.notify.error(err, 'Could not remove option value.'),
    });
  }

  private refresh(): void {
    this.api.getPoolOptions(this.pool().id).subscribe((values) => this.ownValues.set(values));
  }
}
