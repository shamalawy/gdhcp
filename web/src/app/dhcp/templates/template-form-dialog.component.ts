import { ChangeDetectionStrategy, Component, Inject } from '@angular/core';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';

import { DHCPTemplate } from '../../shared/models';

export interface TemplateFormDialogData {
  template?: DHCPTemplate;
}

export interface TemplateFormResult {
  name: string;
  description: string | null;
}

@Component({
  selector: 'app-template-form-dialog',
  standalone: true,
  imports: [ReactiveFormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatInputModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>{{ data.template ? 'Edit template' : 'New template' }}</h2>
    <mat-dialog-content>
      <form [formGroup]="form" class="form">
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Name</mat-label>
          <input matInput formControlName="name" />
        </mat-form-field>
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Description</mat-label>
          <textarea matInput rows="3" formControlName="description" placeholder="What this template configures and where it's meant to be attached"></textarea>
        </mat-form-field>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="undefined">Cancel</button>
      <button mat-flat-button color="primary" [disabled]="form.invalid" (click)="save()">
        {{ data.template ? 'Save' : 'Create' }}
      </button>
    </mat-dialog-actions>
  `,
  styles: [
    `
      .form {
        display: flex;
        flex-direction: column;
        gap: 4px;
        width: 380px;
        max-width: 80vw;
        padding-top: 4px;
      }
    `,
  ],
})
export class TemplateFormDialogComponent {
  readonly form;

  constructor(
    private readonly fb: FormBuilder,
    private readonly ref: MatDialogRef<TemplateFormDialogComponent, TemplateFormResult | undefined>,
    @Inject(MAT_DIALOG_DATA) readonly data: TemplateFormDialogData,
  ) {
    this.form = this.fb.nonNullable.group({
      name: [data.template?.name ?? '', [Validators.required, Validators.maxLength(255)]],
      description: [data.template?.description ?? ''],
    });
  }

  save(): void {
    if (this.form.invalid) return;
    const raw = this.form.getRawValue();
    this.ref.close({ name: raw.name, description: raw.description || null });
  }
}
