import { ChangeDetectionStrategy, Component, Inject } from '@angular/core';
import { AbstractControl, FormBuilder, ReactiveFormsModule, ValidationErrors, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';

import { DHCPPool } from '../../shared/models';
import { isValidIPv4, rangeWithinCidr } from './ip-utils';

export interface PoolFormDialogData {
  scopeNetwork: string;
  /** Present when editing an existing pool; absent when creating. */
  pool?: DHCPPool;
}

export interface PoolFormResult {
  name: string;
  range_start: string;
  range_end: string;
  priority: number;
  description: string | null;
}

function rangeValidator(scopeNetwork: string) {
  return (group: AbstractControl): ValidationErrors | null => {
    const start = group.get('range_start')?.value as string;
    const end = group.get('range_end')?.value as string;
    if (!start || !end || !isValidIPv4(start) || !isValidIPv4(end)) return null; // per-field validators already flag this
    if (!rangeWithinCidr(start, end, scopeNetwork)) {
      return { rangeOutsideScope: true };
    }
    return null;
  };
}

/** Create/edit dialog for a pool's range within its scope's network — range
 * validation (start ≤ end, both inside the scope's CIDR) runs client-side
 * before submit; the server still re-validates authoritatively. */
@Component({
  selector: 'app-pool-form-dialog',
  standalone: true,
  imports: [ReactiveFormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatInputModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>{{ data.pool ? 'Edit pool' : 'Create pool' }}</h2>
    <mat-dialog-content>
      <form [formGroup]="form" class="form">
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Name</mat-label>
          <input matInput formControlName="name" />
        </mat-form-field>

        <div class="range-row">
          <mat-form-field appearance="outline" subscriptSizing="dynamic">
            <mat-label>Range start</mat-label>
            <input matInput formControlName="range_start" class="gdhcp-mono" placeholder="10.1.0.10" />
            @if (form.controls.range_start.hasError('pattern') && form.controls.range_start.touched) {
              <mat-error>Not a valid IPv4 address</mat-error>
            }
          </mat-form-field>
          <mat-form-field appearance="outline" subscriptSizing="dynamic">
            <mat-label>Range end</mat-label>
            <input matInput formControlName="range_end" class="gdhcp-mono" placeholder="10.1.0.200" />
            @if (form.controls.range_end.hasError('pattern') && form.controls.range_end.touched) {
              <mat-error>Not a valid IPv4 address</mat-error>
            }
          </mat-form-field>
        </div>
        @if (form.hasError('rangeOutsideScope')) {
          <p class="err">Range must fall entirely inside the scope's network ({{ data.scopeNetwork }}), with start ≤ end.</p>
        }

        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Priority</mat-label>
          <input matInput type="number" formControlName="priority" />
          <mat-hint>Lower numbers are tried first when multiple pools can serve a request.</mat-hint>
        </mat-form-field>

        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Description</mat-label>
          <textarea matInput rows="2" formControlName="description"></textarea>
        </mat-form-field>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="undefined">Cancel</button>
      <button mat-flat-button color="primary" [disabled]="form.invalid" (click)="save()">
        {{ data.pool ? 'Save' : 'Create' }}
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
      .range-row {
        display: flex;
        gap: 8px;
      }
      .range-row mat-form-field {
        flex: 1;
      }
      .err {
        margin: -4px 0 8px;
        font-size: 12px;
        color: var(--status-error);
      }
    `,
  ],
})
export class PoolFormDialogComponent {
  readonly form;

  constructor(
    private readonly fb: FormBuilder,
    private readonly ref: MatDialogRef<PoolFormDialogComponent, PoolFormResult | undefined>,
    @Inject(MAT_DIALOG_DATA) readonly data: PoolFormDialogData,
  ) {
    const ipPattern = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
    this.form = this.fb.nonNullable.group(
      {
        name: [data.pool?.name ?? '', [Validators.required, Validators.maxLength(255)]],
        range_start: [data.pool?.range_start ?? '', [Validators.required, Validators.pattern(ipPattern)]],
        range_end: [data.pool?.range_end ?? '', [Validators.required, Validators.pattern(ipPattern)]],
        priority: [data.pool?.priority ?? 100, [Validators.required, Validators.min(0)]],
        description: [data.pool?.description ?? ''],
      },
      { validators: rangeValidator(data.scopeNetwork) },
    );
  }

  save(): void {
    if (this.form.invalid) return;
    const raw = this.form.getRawValue();
    this.ref.close({
      name: raw.name,
      range_start: raw.range_start,
      range_end: raw.range_end,
      priority: raw.priority,
      description: raw.description || null,
    });
  }
}
