import { ChangeDetectionStrategy, Component, EventEmitter, Output, inject, input, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';

import { DescriptionFieldComponent } from '../../shared/components/description-field/description-field.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { ApiService } from '../../shared/api.service';
import { DHCPScope } from '../../shared/models';
import { NotifyService } from '../shared/notify.service';
import { TagEditorComponent } from '../shared/tag-editor.component';

@Component({
  selector: 'app-scope-settings-tab',
  standalone: true,
  imports: [FormsModule, DescriptionFieldComponent, TagEditorComponent, MatButtonModule, MatIconModule, MatSlideToggleModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="settings">
      <section class="gdhcp-card">
        <h3>Name</h3>
        @if (editingName()) {
          <div class="name-edit">
            <input class="name-input" [(ngModel)]="nameDraft" />
            <button mat-button (click)="editingName.set(false)">Cancel</button>
            <button mat-flat-button color="primary" (click)="saveName()">Save</button>
          </div>
        } @else {
          <div class="name-view">
            <span>{{ scope().name }}</span>
            <button mat-icon-button (click)="startEditName()" aria-label="Edit name"><mat-icon>edit</mat-icon></button>
          </div>
        }
      </section>

      <section class="gdhcp-card">
        <h3>Description</h3>
        <app-description-field [value]="scope().description" (save)="saveDescription($event)" />
      </section>

      <section class="gdhcp-card">
        <h3>Enabled</h3>
        <mat-slide-toggle [ngModel]="scope().is_enabled" (ngModelChange)="saveEnabled($event)">
          {{ scope().is_enabled ? 'Serving DHCP requests' : 'Disabled — no requests are served' }}
        </mat-slide-toggle>
      </section>

      <section class="gdhcp-card">
        <h3>Tags</h3>
        <app-tag-editor entityType="scope" [entityId]="scope().id" />
      </section>

      <section class="gdhcp-card danger">
        <h3>Delete scope</h3>
        <p class="gdhcp-muted">Deletes this scope and cascades to its pools, reservations, and lease history. This cannot be undone.</p>
        <button mat-flat-button color="warn" (click)="deleteScope()">Delete scope</button>
      </section>
    </div>
  `,
  styles: [
    `
      .settings {
        display: flex;
        flex-direction: column;
        gap: 16px;
        max-width: 560px;
      }
      h3 {
        margin: 0 0 12px;
        font-size: 14px;
      }
      .name-view {
        display: flex;
        align-items: center;
        gap: 4px;
      }
      .name-edit {
        display: flex;
        align-items: center;
        gap: 8px;
      }
      .name-input {
        flex: 1;
        font: inherit;
        padding: 6px 8px;
        border: 1px solid var(--border-color);
        border-radius: 4px;
        background: var(--bg-primary);
        color: var(--text-primary);
      }
      .danger {
        border-color: var(--status-error);
      }
    `,
  ],
})
export class ScopeSettingsTabComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);
  private readonly confirmSvc = inject(ConfirmDialogService);

  readonly scope = input.required<DHCPScope>();

  @Output() readonly updated = new EventEmitter<DHCPScope>();
  @Output() readonly deleted = new EventEmitter<void>();

  readonly editingName = signal(false);
  nameDraft = '';

  startEditName(): void {
    this.nameDraft = this.scope().name;
    this.editingName.set(true);
  }

  saveName(): void {
    const name = this.nameDraft.trim();
    if (!name) return;
    this.api.updateScope(this.scope().id, { name }).subscribe({
      next: (scope) => {
        this.updated.emit(scope);
        this.editingName.set(false);
        this.notify.success('Name updated.');
      },
      error: (err) => this.notify.error(err, 'Could not update name.'),
    });
  }

  saveDescription(description: string | null): void {
    this.api.updateScope(this.scope().id, { description }).subscribe({
      next: (scope) => {
        this.updated.emit(scope);
        this.notify.success('Description updated.');
      },
      error: (err) => this.notify.error(err, 'Could not update description.'),
    });
  }

  saveEnabled(is_enabled: boolean): void {
    this.api.updateScope(this.scope().id, { is_enabled }).subscribe({
      next: (scope) => {
        this.updated.emit(scope);
        this.notify.success(is_enabled ? 'Scope enabled.' : 'Scope disabled.');
      },
      error: (err) => this.notify.error(err, 'Could not update scope.'),
    });
  }

  deleteScope(): void {
    this.confirmSvc
      .confirm({
        title: 'Delete scope',
        message: `Delete scope "${this.scope().name}"? This cascades to all of its pools, reservations, and lease history.`,
        danger: true,
      })
      .subscribe((ok) => {
        if (!ok) return;
        this.api.deleteScope(this.scope().id).subscribe({
          next: () => {
            this.notify.success('Scope deleted.');
            this.deleted.emit();
          },
          error: (err) => this.notify.error(err, 'Could not delete scope.'),
        });
      });
  }
}
