import { ChangeDetectionStrategy, Component, effect, inject, input, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { Router, RouterLink } from '@angular/router';
import { forkJoin, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { DescriptionFieldComponent } from '../../shared/components/description-field/description-field.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { ApiService } from '../../shared/api.service';
import { DHCPScope, DHCPTemplate, OptionValue, TemplateScopeAssoc } from '../../shared/models';
import { EffectiveOptionsComponent } from '../shared/effective-options.component';
import { NotifyService } from '../shared/notify.service';

interface AttachedScopeRow {
  assoc: TemplateScopeAssoc;
  scope: DHCPScope | null;
}

/**
 * Single-flow template detail page (no tabs — the whole entity is small
 * enough to show at once): editable name/description/enabled, the options
 * editor (same `EffectiveOptionsComponent` used by scope/pool, add-only —
 * see its class docstring on why template-level values can't be edited or
 * removed here), and the list of scopes this template is attached to.
 *
 * The spec's "attached scopes/pools lists" is scopes-only in this
 * implementation: docs/CONTRACTS.md and endpoints.md define template↔scope
 * association (`TemplateScopeAssoc`, `/templates/{id}/scopes`) but no
 * template↔pool association exists in the schema or API — pools only
 * inherit a template's values transitively through their owning scope. See
 * notes_for_integration.
 *
 * Tags are omitted here: `TaggableEntityType` in shared/models.ts doesn't
 * include `'template'`, so `TagEditorComponent` can't target one without
 * editing a file this unit doesn't own.
 */
@Component({
  selector: 'app-template-detail',
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    PageHeaderComponent,
    DescriptionFieldComponent,
    EffectiveOptionsComponent,
    MatButtonModule,
    MatIconModule,
    MatProgressBarModule,
    MatSlideToggleModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (loading()) {
      <mat-progress-bar mode="indeterminate" />
    } @else {
      @if (template(); as t) {
        <app-page-header [title]="t.name" [breadcrumbs]="[{ label: 'Templates', link: '/dhcp/templates' }, { label: t.name }]">
          <mat-slide-toggle [ngModel]="t.is_enabled" (ngModelChange)="saveEnabled($event)">
            {{ t.is_enabled ? 'Enabled' : 'Disabled' }}
          </mat-slide-toggle>
          <button mat-icon-button color="warn" (click)="remove()" aria-label="Delete template">
            <mat-icon>delete</mat-icon>
          </button>
        </app-page-header>

        <section class="gdhcp-card description-card">
          <h3>Description</h3>
          <app-description-field [value]="t.description" (save)="saveDescription($event)" />
        </section>

        <section class="gdhcp-card">
          <h3>Option values</h3>
          <app-effective-options ownLevel="template" ownLabel="This template" [ownValues]="ownValues()" [editable]="false" (add)="onAdd($event)" />
          <p class="gdhcp-muted hint">
            endpoints.md defines only add (POST) and read for template options — values set here can't be edited or removed
            from this UI yet; delete and re-add to change one.
          </p>
        </section>

        <section class="gdhcp-card">
          <h3>Attached scopes</h3>
          @if (attachedScopes().length === 0) {
            <p class="gdhcp-muted">Not attached to any scope yet. Attach it from a scope's Templates tab.</p>
          } @else {
            <table class="gdhcp-table">
              <thead>
                <tr>
                  <th>Scope</th>
                  <th>Priority</th>
                </tr>
              </thead>
              <tbody>
                @for (row of attachedScopes(); track row.assoc.id) {
                  <tr>
                    <td>
                      @if (row.scope) {
                        <a [routerLink]="['/dhcp', 'scopes', row.scope.id]">{{ row.scope.name }}</a>
                      } @else {
                        {{ row.assoc.scope_id }}
                      }
                    </td>
                    <td>{{ row.assoc.priority }}</td>
                  </tr>
                }
              </tbody>
            </table>
          }
        </section>
      } @else {
        <p class="gdhcp-muted">Template not found.</p>
      }
    }
  `,
  styles: [
    `
      .gdhcp-card {
        margin-bottom: 16px;
        max-width: 640px;
      }
      h3 {
        margin: 0 0 12px;
        font-size: 14px;
      }
      .hint {
        font-size: 12px;
        margin: 10px 0 0;
      }
      .gdhcp-table {
        width: 100%;
        border-collapse: collapse;
        font-size: 13px;
      }
      .gdhcp-table th {
        text-align: left;
        font-size: 11px;
        text-transform: uppercase;
        letter-spacing: 0.03em;
        color: var(--text-secondary);
        border-bottom: 1px solid var(--border-color);
        padding: 8px;
      }
      .gdhcp-table td {
        padding: 8px;
        border-bottom: 1px solid var(--divider-color);
      }
    `,
  ],
})
export class TemplateDetailComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);
  private readonly confirmSvc = inject(ConfirmDialogService);
  private readonly router = inject(Router);

  readonly id = input.required<string>();

  readonly loading = signal(true);
  readonly template = signal<DHCPTemplate | null>(null);
  readonly ownValues = signal<OptionValue[]>([]);
  readonly attachedScopes = signal<AttachedScopeRow[]>([]);

  constructor() {
    effect(
      () => {
        const templateId = this.id();
        this.loading.set(true);
        this.api.getTemplate(templateId).subscribe({
          next: (template) => {
            this.template.set(template);
            this.loading.set(false);
            this.loadOptions(templateId);
            this.loadAttachedScopes(templateId);
          },
          error: (err) => {
            this.loading.set(false);
            this.template.set(null);
            this.notify.error(err, 'Could not load template.');
          },
        });
      },
      { allowSignalWrites: true },
    );
  }

  private loadOptions(templateId: string): void {
    this.api.getTemplateOptions(templateId).subscribe((values) => this.ownValues.set(values));
  }

  private loadAttachedScopes(templateId: string): void {
    this.api.getTemplateScopes(templateId).subscribe((assocs) => {
      if (assocs.length === 0) {
        this.attachedScopes.set([]);
        return;
      }
      forkJoin(assocs.map((assoc) => this.api.getScope(assoc.scope_id).pipe(catchError(() => of(null))))).subscribe(
        (scopes) => {
          this.attachedScopes.set(assocs.map((assoc, i) => ({ assoc, scope: scopes[i] })));
        },
      );
    });
  }

  onAdd(result: { option_code: number; value: string }): void {
    this.api.setTemplateOption(this.id(), result).subscribe({
      next: () => {
        this.notify.success('Option value added.');
        this.loadOptions(this.id());
      },
      error: (err) => this.notify.error(err, 'Could not add option value.'),
    });
  }

  saveDescription(description: string | null): void {
    this.api.updateTemplate(this.id(), { description }).subscribe({
      next: (template) => {
        this.template.set(template);
        this.notify.success('Description updated.');
      },
      error: (err) => this.notify.error(err, 'Could not update description.'),
    });
  }

  saveEnabled(is_enabled: boolean): void {
    this.api.updateTemplate(this.id(), { is_enabled }).subscribe({
      next: (template) => {
        this.template.set(template);
        this.notify.success(is_enabled ? 'Template enabled.' : 'Template disabled.');
      },
      error: (err) => this.notify.error(err, 'Could not update template.'),
    });
  }

  remove(): void {
    const t = this.template();
    if (!t) return;
    this.confirmSvc
      .confirm({ title: 'Delete template', message: `Delete template "${t.name}"?`, danger: true })
      .subscribe((ok) => {
        if (!ok) return;
        this.api.deleteTemplate(t.id).subscribe({
          next: () => {
            this.notify.success('Template deleted.');
            this.router.navigate(['/dhcp/templates']);
          },
          error: (err) => this.notify.error(err, 'Could not delete template.'),
        });
      });
  }
}
