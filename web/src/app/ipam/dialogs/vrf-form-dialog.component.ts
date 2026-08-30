import { ChangeDetectionStrategy, Component, Inject, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';

import { ApiService } from '../../shared/api.service';
import { VRF } from '../../shared/models';
import { extractApiError } from '../util/api-error.util';

export interface VrfFormDialogData {
  mode: 'create' | 'edit';
  vrf?: VRF;
}

/** Create/edit dialog for a VRF. A VRF (Virtual Routing and Forwarding
 * instance) is an isolated IP address space: the same prefix, e.g.
 * 10.0.0.0/8, can exist independently in more than one VRF without
 * conflicting — used to separate customers, sites, or environments that
 * happen to reuse the same address ranges. */
@Component({
  selector: 'app-vrf-form-dialog',
  standalone: true,
  imports: [
    FormsModule,
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatProgressSpinnerModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>{{ data.mode === 'create' ? 'New VRF' : 'Edit VRF' }}</h2>

    <mat-dialog-content>
      <p class="explainer">
        A VRF gives this set of prefixes its own isolated address space, so the same CIDR block
        can be reused in another VRF without overlapping.
      </p>

      @if (bannerError() && fieldError() !== 'name') {
        <div class="banner-error">
          <mat-icon>error_outline</mat-icon>
          <span>{{ bannerError() }}</span>
        </div>
      }

      <mat-form-field appearance="outline" class="full">
        <mat-label>Name</mat-label>
        <input matInput [(ngModel)]="name" required maxlength="100" [disabled]="submitting()" />
      </mat-form-field>
      @if (fieldError() === 'name') {
        <p class="field-error">{{ bannerError() }}</p>
      }

      <mat-form-field appearance="outline" class="full">
        <mat-label>Route distinguisher</mat-label>
        <input
          matInput
          [(ngModel)]="routeDistinguisher"
          placeholder="e.g. 65000:100"
          maxlength="64"
          [disabled]="submitting()"
        />
        <mat-hint>Optional BGP/MPLS route distinguisher, for VRFs that map to a routed network.</mat-hint>
      </mat-form-field>

      <mat-form-field appearance="outline" class="full">
        <mat-label>Description</mat-label>
        <textarea
          matInput
          rows="3"
          [(ngModel)]="description"
          placeholder="What is this VRF for?"
          [disabled]="submitting()"
        ></textarea>
      </mat-form-field>
    </mat-dialog-content>

    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="null" [disabled]="submitting()">Cancel</button>
      <button mat-flat-button color="primary" [disabled]="!name.trim() || submitting()" (click)="submit()">
        @if (submitting()) {
          <mat-spinner diameter="18" />
        } @else {
          {{ data.mode === 'create' ? 'Create VRF' : 'Save changes' }}
        }
      </button>
    </mat-dialog-actions>
  `,
  styles: [
    `
      .full {
        width: 100%;
      }

      .explainer {
        margin: 0 0 16px;
        color: var(--text-secondary);
        font-size: 13px;
      }

      .banner-error {
        display: flex;
        align-items: center;
        gap: 8px;
        background: var(--bg-highlight);
        color: var(--status-error);
        border: 1px solid var(--status-error);
        border-radius: 4px;
        padding: 8px 12px;
        margin-bottom: 12px;
        font-size: 13px;
      }

      .field-error {
        color: var(--status-error);
        font-size: 12px;
        margin: 0 0 12px;
      }
    `,
  ],
})
export class VrfFormDialogComponent {
  private readonly api = inject(ApiService);
  private readonly dialogRef = inject(MatDialogRef<VrfFormDialogComponent, VRF | null>);

  name: string;
  routeDistinguisher: string;
  description: string;

  readonly submitting = signal(false);
  readonly bannerError = signal<string | null>(null);
  readonly fieldError = signal<string | null>(null);

  constructor(@Inject(MAT_DIALOG_DATA) readonly data: VrfFormDialogData) {
    this.name = data.vrf?.name ?? '';
    this.routeDistinguisher = data.vrf?.route_distinguisher ?? '';
    this.description = data.vrf?.description ?? '';
  }

  submit(): void {
    const name = this.name.trim();
    if (!name || this.submitting()) return;

    this.submitting.set(true);
    this.bannerError.set(null);
    this.fieldError.set(null);

    const body = {
      name,
      route_distinguisher: this.routeDistinguisher.trim() || null,
      description: this.description.trim() || null,
    };

    const result$ =
      this.data.mode === 'create'
        ? this.api.createVRF(body)
        : this.api.updateVRF(this.data.vrf!.id, body);

    result$.subscribe({
      next: (vrf) => this.dialogRef.close(vrf),
      error: (err) => {
        this.submitting.set(false);
        const friendly = extractApiError(err, 'Could not save this VRF.');
        this.bannerError.set(friendly.message);
        this.fieldError.set(friendly.field);
      },
    });
  }
}
