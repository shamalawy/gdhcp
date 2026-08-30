import { ChangeDetectionStrategy, Component, Inject, computed, inject, signal } from '@angular/core';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';

import { ApiService } from '../../shared/api.service';
import { DHCPTemplate } from '../../shared/models';

export interface AttachTemplateDialogData {
  alreadyAttachedTemplateIds: string[];
}

export interface AttachTemplateResult {
  template_id: string;
  priority: number;
}

/** Picker for the scope Templates tab's "Attach template" action — lists
 * templates not already attached to this scope, with a priority field. */
@Component({
  selector: 'app-attach-template-dialog',
  standalone: true,
  imports: [ReactiveFormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatInputModule, MatSelectModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>Attach template</h2>
    <mat-dialog-content>
      <form [formGroup]="form" class="form">
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Template</mat-label>
          <mat-select formControlName="template_id">
            @for (t of available(); track t.id) {
              <mat-option [value]="t.id">{{ t.name }}</mat-option>
            }
          </mat-select>
          @if (available().length === 0) {
            <mat-hint>All existing templates are already attached to this scope.</mat-hint>
          }
        </mat-form-field>
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Priority</mat-label>
          <input matInput type="number" formControlName="priority" />
          <mat-hint>Among conflicting values from multiple templates, the highest priority number wins.</mat-hint>
        </mat-form-field>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="undefined">Cancel</button>
      <button mat-flat-button color="primary" [disabled]="form.invalid" (click)="save()">Attach</button>
    </mat-dialog-actions>
  `,
  styles: [
    `
      .form {
        display: flex;
        flex-direction: column;
        gap: 4px;
        width: 320px;
        padding-top: 4px;
      }
    `,
  ],
})
export class AttachTemplateDialogComponent {
  private readonly api = inject(ApiService);
  private readonly fb = inject(FormBuilder);
  private readonly ref = inject(MatDialogRef<AttachTemplateDialogComponent, AttachTemplateResult | undefined>);

  readonly templates = signal<DHCPTemplate[]>([]);
  readonly available = computed(() => {
    const attached = new Set(this.data.alreadyAttachedTemplateIds);
    return this.templates().filter((t) => !attached.has(t.id));
  });

  readonly form = this.fb.nonNullable.group({
    template_id: ['', Validators.required],
    priority: [100, [Validators.required, Validators.min(0)]],
  });

  constructor(@Inject(MAT_DIALOG_DATA) readonly data: AttachTemplateDialogData) {
    this.api.listTemplates({ limit: 500 }).subscribe((res) => this.templates.set(res.items));
  }

  save(): void {
    if (this.form.invalid) return;
    const raw = this.form.getRawValue();
    this.ref.close({ template_id: raw.template_id, priority: raw.priority });
  }
}
