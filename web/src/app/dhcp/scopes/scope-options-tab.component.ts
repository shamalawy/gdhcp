import { ChangeDetectionStrategy, Component, effect, inject, input, signal } from '@angular/core';
import { forkJoin, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { ApiService } from '../../shared/api.service';
import { DHCPScope, OptionValue } from '../../shared/models';
import { EffectiveOptionsComponent, OptionLayer } from '../shared/effective-options.component';
import { NotifyService } from '../shared/notify.service';
import { AttachedTemplate } from '../shared/template-attachment';

/** Scope Options tab: this scope's own values, layered over its attached
 * templates' values (lowest-precedence first — see EffectiveOptionsComponent
 * for the priority-ordering assumption). No global layer is shown: the API
 * exposes no `GET` for global-level option values (CONTRACTS.md's
 * `ConfigStore.GetGlobalOptions` has no REST route in endpoints.md). */
@Component({
  selector: 'app-scope-options-tab',
  standalone: true,
  imports: [EffectiveOptionsComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-effective-options
      ownLevel="scope"
      ownLabel="This scope"
      [ownValues]="ownValues()"
      [inheritedLayers]="inheritedLayers()"
      (add)="onAdd($event)"
      (update)="onUpdate($event)"
      (remove)="onRemove($event)"
    />
  `,
})
export class ScopeOptionsTabComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);

  readonly scope = input.required<DHCPScope>();
  readonly attachedTemplates = input<AttachedTemplate[]>([]);

  readonly ownValues = signal<OptionValue[]>([]);
  readonly inheritedLayers = signal<OptionLayer[]>([]);

  constructor() {
    effect(() => {
      const scopeId = this.scope().id;
      this.api.getScopeOptions(scopeId).subscribe({
        next: (values) => this.ownValues.set(values),
        error: (err) => this.notify.error(err, 'Could not load scope options.'),
      });
    });

    effect(() => {
      const templates = this.attachedTemplates();
      if (templates.length === 0) {
        this.inheritedLayers.set([]);
        return;
      }
      forkJoin(
        templates.map((t) =>
          this.api.getTemplateOptions(t.template.id).pipe(catchError(() => of<OptionValue[]>([]))),
        ),
      ).subscribe((results) => {
        this.inheritedLayers.set(
          templates.map((t, i) => ({
            level: 'template' as const,
            label: `Template: ${t.template.name} (priority ${t.assoc.priority})`,
            values: results[i],
          })),
        );
      });
    });
  }

  onAdd(result: { option_code: number; value: string }): void {
    this.api.setScopeOption(this.scope().id, result).subscribe({
      next: () => {
        this.notify.success('Option value set.');
        this.refresh();
      },
      error: (err) => this.notify.error(err, 'Could not set option value.'),
    });
  }

  onUpdate(result: { id: string; value: string; is_enabled: boolean }): void {
    this.api.updateScopeOption(this.scope().id, result.id, { value: result.value, is_enabled: result.is_enabled }).subscribe({
      next: () => {
        this.notify.success('Option value updated.');
        this.refresh();
      },
      error: (err) => this.notify.error(err, 'Could not update option value.'),
    });
  }

  onRemove(value: OptionValue): void {
    this.api.removeScopeOption(this.scope().id, value.id).subscribe({
      next: () => {
        this.notify.success('Option value removed.');
        this.refresh();
      },
      error: (err) => this.notify.error(err, 'Could not remove option value.'),
    });
  }

  private refresh(): void {
    this.api.getScopeOptions(this.scope().id).subscribe((values) => this.ownValues.set(values));
  }
}
