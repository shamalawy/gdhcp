import { CdkDrag, CdkDragDrop, CdkDropList, moveItemInArray } from '@angular/cdk/drag-drop';
import { ChangeDetectionStrategy, Component, EventEmitter, Output, inject, input, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { RouterLink } from '@angular/router';
import { forkJoin } from 'rxjs';

import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { ApiService } from '../../shared/api.service';
import { DHCPScope } from '../../shared/models';
import { AttachTemplateDialogComponent, AttachTemplateDialogData, AttachTemplateResult } from '../shared/attach-template-dialog.component';
import { NotifyService } from '../shared/notify.service';
import { AttachedTemplate } from '../shared/template-attachment';

export type { AttachedTemplate } from '../shared/template-attachment';

/** Templates tab: attached-template list with drag-to-reorder priority.
 * Reorder re-POSTs `attachTemplateToScope` with each row's new priority for
 * every row whose priority changed, relying on the endpoint upserting on
 * (template_id, scope_id) conflict rather than 409ing on a duplicate attach
 * — endpoints.md labels it "Associate template to scope" without saying
 * whether re-attaching an already-attached template updates its priority.
 * See notes_for_integration; if the API 409s instead, this needs to
 * detach-then-reattach per row instead. */
@Component({
  selector: 'app-scope-templates-tab',
  standalone: true,
  imports: [CdkDrag, CdkDropList, RouterLink, EmptyStateComponent, MatButtonModule, MatIconModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="header-row">
      <button mat-flat-button color="primary" (click)="openAttach()">
        <mat-icon>add</mat-icon> Attach template
      </button>
    </div>

    @if (attachedTemplates().length === 0) {
      <app-empty-state icon="description" title="No templates attached" message="Attach a template to apply a shared set of option values to this scope.">
        <button mat-stroked-button (click)="openAttach()">Attach template</button>
      </app-empty-state>
    } @else {
      <p class="hint gdhcp-muted">Drag to reorder — the template lowest in the list wins when two templates set the same option.</p>
      <div cdkDropList class="list" (cdkDropListDropped)="onDrop($event)">
        @for (item of attachedTemplates(); track item.template.id) {
          <div class="row" cdkDrag>
            <mat-icon class="handle" cdkDragHandle>drag_indicator</mat-icon>
            <a [routerLink]="['/dhcp', 'templates', item.template.id]" class="name">{{ item.template.name }}</a>
            <span class="priority">priority {{ item.assoc.priority }}</span>
            <button mat-icon-button (click)="detach(item)" [attr.aria-label]="'Detach ' + item.template.name">
              <mat-icon>link_off</mat-icon>
            </button>
          </div>
        }
      </div>
    }
  `,
  styles: [
    `
      .header-row {
        margin-bottom: 12px;
      }
      .hint {
        font-size: 12px;
        margin: 0 0 8px;
      }
      .list {
        display: flex;
        flex-direction: column;
        gap: 4px;
        max-width: 520px;
      }
      .row {
        display: flex;
        align-items: center;
        gap: 8px;
        padding: 8px 10px;
        border: 1px solid var(--border-color);
        border-radius: 6px;
        background: var(--bg-primary);
      }
      .handle {
        cursor: grab;
        color: var(--text-secondary);
      }
      .name {
        flex: 1;
        font-size: 13px;
      }
      .priority {
        font-size: 12px;
        color: var(--text-secondary);
      }
      .cdk-drag-preview {
        box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
      }
      .cdk-drag-placeholder {
        opacity: 0.3;
      }
    `,
  ],
})
export class ScopeTemplatesTabComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmSvc = inject(ConfirmDialogService);
  private readonly notify = inject(NotifyService);

  readonly scope = input.required<DHCPScope>();
  readonly attachedTemplates = input<AttachedTemplate[]>([]);

  @Output() readonly changed = new EventEmitter<void>();

  openAttach(): void {
    const alreadyAttachedTemplateIds = this.attachedTemplates().map((t) => t.template.id);
    this.dialog
      .open<AttachTemplateDialogComponent, AttachTemplateDialogData, AttachTemplateResult | undefined>(
        AttachTemplateDialogComponent,
        { data: { alreadyAttachedTemplateIds }, width: '380px' },
      )
      .afterClosed()
      .subscribe((result) => {
        if (!result) return;
        this.api.attachTemplateToScope(result.template_id, this.scope().id, result.priority).subscribe({
          next: () => {
            this.notify.success('Template attached.');
            this.changed.emit();
          },
          error: (err) => this.notify.error(err, 'Could not attach template.'),
        });
      });
  }

  detach(item: AttachedTemplate): void {
    this.confirmSvc
      .confirm({
        title: 'Detach template',
        message: `Detach "${item.template.name}" from this scope? Its option values will no longer apply here.`,
        danger: true,
      })
      .subscribe((ok) => {
        if (!ok) return;
        this.api.detachTemplateFromScope(item.template.id, this.scope().id).subscribe({
          next: () => {
            this.notify.success('Template detached.');
            this.changed.emit();
          },
          error: (err) => this.notify.error(err, 'Could not detach template.'),
        });
      });
  }

  onDrop(event: CdkDragDrop<AttachedTemplate[]>): void {
    const reordered = this.attachedTemplates().slice();
    moveItemInArray(reordered, event.previousIndex, event.currentIndex);

    const updates = reordered
      .map((item, index) => ({ item, priority: (index + 1) * 10 }))
      .filter(({ item, priority }) => item.assoc.priority !== priority);

    if (updates.length === 0) return;

    forkJoin(
      updates.map(({ item, priority }) => this.api.attachTemplateToScope(item.template.id, this.scope().id, priority)),
    ).subscribe({
      next: () => this.changed.emit(),
      error: (err) => this.notify.error(err, 'Could not save the new template order.'),
    });
  }
}
