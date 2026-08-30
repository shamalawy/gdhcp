import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { Router } from '@angular/router';

import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { ApiService } from '../../shared/api.service';
import { DHCPTemplate } from '../../shared/models';
import { NotifyService } from '../shared/notify.service';
import { TemplateFormDialogComponent, TemplateFormDialogData, TemplateFormResult } from './template-form-dialog.component';

@Component({
  selector: 'app-templates-list',
  standalone: true,
  imports: [EmptyStateComponent, PageHeaderComponent, MatButtonModule, MatIconModule, MatProgressBarModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="DHCP Templates">
      <button mat-flat-button color="primary" (click)="openCreate()">
        <mat-icon>add</mat-icon> New template
      </button>
    </app-page-header>

    @if (loading()) {
      <mat-progress-bar mode="indeterminate" />
    }

    @if (!loading() && templates().length === 0) {
      <app-empty-state icon="description" title="No templates yet" message="Templates bundle a set of option values you can attach to multiple scopes.">
        <button mat-stroked-button (click)="openCreate()">Create template</button>
      </app-empty-state>
    } @else if (templates().length > 0) {
      <div class="table-wrap">
        <table class="gdhcp-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Description</th>
              <th>Enabled</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            @for (t of templates(); track t.id) {
              <tr class="clickable" (click)="open(t.id)">
                <td>{{ t.name }}</td>
                <td class="gdhcp-muted">{{ t.description || '—' }}</td>
                <td>{{ t.is_enabled ? 'Yes' : 'No' }}</td>
                <td class="actions" (click)="$event.stopPropagation()">
                  <button mat-icon-button (click)="openEdit(t)" [attr.aria-label]="'Edit ' + t.name">
                    <mat-icon>edit</mat-icon>
                  </button>
                  <button mat-icon-button (click)="remove(t)" [attr.aria-label]="'Delete ' + t.name">
                    <mat-icon>delete</mat-icon>
                  </button>
                </td>
              </tr>
            }
          </tbody>
        </table>
      </div>
    }
  `,
  styles: [
    `
      .table-wrap {
        overflow-x: auto;
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
      tr.clickable {
        cursor: pointer;
      }
      tr.clickable:hover {
        background: var(--hover-bg);
      }
      .actions {
        text-align: right;
        white-space: nowrap;
      }
    `,
  ],
})
export class TemplatesListComponent {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmSvc = inject(ConfirmDialogService);
  private readonly notify = inject(NotifyService);
  private readonly router = inject(Router);

  readonly loading = signal(true);
  readonly templates = signal<DHCPTemplate[]>([]);

  constructor() {
    this.load();
  }

  load(): void {
    this.loading.set(true);
    this.api.listTemplates({ limit: 500 }).subscribe({
      next: (page) => {
        this.templates.set(page.items);
        this.loading.set(false);
      },
      error: (err) => {
        this.loading.set(false);
        this.notify.error(err, 'Could not load templates.');
      },
    });
  }

  open(id: string): void {
    this.router.navigate(['/dhcp/templates', id]);
  }

  openCreate(): void {
    this.dialog
      .open<TemplateFormDialogComponent, TemplateFormDialogData, TemplateFormResult | undefined>(TemplateFormDialogComponent, {
        width: '440px',
      })
      .afterClosed()
      .subscribe((result) => {
        if (!result) return;
        this.api.createTemplate(result).subscribe({
          next: (template) => {
            this.notify.success('Template created.');
            this.router.navigate(['/dhcp/templates', template.id]);
          },
          error: (err) => this.notify.error(err, 'Could not create template.'),
        });
      });
  }

  openEdit(template: DHCPTemplate): void {
    this.dialog
      .open<TemplateFormDialogComponent, TemplateFormDialogData, TemplateFormResult | undefined>(TemplateFormDialogComponent, {
        data: { template },
        width: '440px',
      })
      .afterClosed()
      .subscribe((result) => {
        if (!result) return;
        this.api.updateTemplate(template.id, result).subscribe({
          next: () => {
            this.notify.success('Template updated.');
            this.load();
          },
          error: (err) => this.notify.error(err, 'Could not update template.'),
        });
      });
  }

  remove(template: DHCPTemplate): void {
    this.confirmSvc
      .confirm({
        title: 'Delete template',
        message: `Delete template "${template.name}"? It will be detached from every scope using it.`,
        danger: true,
      })
      .subscribe((ok) => {
        if (!ok) return;
        this.api.deleteTemplate(template.id).subscribe({
          next: () => {
            this.notify.success('Template deleted.');
            this.load();
          },
          error: (err) => this.notify.error(err, 'Could not delete template.'),
        });
      });
  }
}
