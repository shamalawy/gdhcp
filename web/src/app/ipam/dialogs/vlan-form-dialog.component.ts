import { ChangeDetectionStrategy, Component, Inject, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatSelectModule } from '@angular/material/select';

import { ApiService } from '../../shared/api.service';
import { VLAN, VLANGroup, VLANStatus } from '../../shared/models';
import { extractApiError } from '../util/api-error.util';

export interface VlanFormDialogData {
  mode: 'create' | 'edit';
  vlan?: VLAN;
  groups: VLANGroup[];
}

const STATUS_HELP: Record<VLANStatus, string> = {
  active: 'In service and available to assign to prefixes.',
  reserved: 'Held back for future use — assign nothing to it yet.',
  deprecated: 'Being retired; leave existing assignments alone but add no new ones.',
};

/** Create/edit dialog for a VLAN. A VLAN is one 802.1Q broadcast domain;
 * IPAM prefixes point at it to record which layer-2 segment an address
 * block serves. VLAN IDs run 1–4094 (0 and 4095 are reserved by the
 * standard) and are unique within their group — or, for ungrouped VLANs,
 * across the whole estate. */
@Component({
  selector: 'app-vlan-form-dialog',
  standalone: true,
  imports: [
    FormsModule,
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatProgressSpinnerModule,
    MatSelectModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>{{ data.mode === 'create' ? 'New VLAN' : 'Edit VLAN' }}</h2>

    <mat-dialog-content>
      <p class="explainer">
        A VLAN is one layer-2 broadcast domain. Assign it to IPAM subnets so the address plan
        records which segment each block serves.
      </p>

      @if (bannerError() && !fieldError()) {
        <div class="banner-error">
          <mat-icon>error_outline</mat-icon>
          <span>{{ bannerError() }}</span>
        </div>
      }

      <div class="row">
        <mat-form-field appearance="outline" class="vid">
          <mat-label>VLAN ID</mat-label>
          <input matInput type="number" min="1" max="4094" [(ngModel)]="vid" required [disabled]="submitting()" />
          <mat-hint>1–4094</mat-hint>
        </mat-form-field>

        <mat-form-field appearance="outline" class="grow">
          <mat-label>Name</mat-label>
          <input matInput [(ngModel)]="name" required maxlength="255" [disabled]="submitting()" />
        </mat-form-field>
      </div>
      @if (fieldError() === 'vid' || fieldError() === 'name') {
        <p class="field-error">{{ bannerError() }}</p>
      }

      <mat-form-field appearance="outline" class="full">
        <mat-label>Group</mat-label>
        <mat-select [(ngModel)]="groupId" [disabled]="submitting()">
          <mat-option [value]="''">No group (global VLAN ID)</mat-option>
          @for (group of data.groups; track group.id) {
            <mat-option [value]="group.id">{{ group.name }} ({{ group.min_vid }}–{{ group.max_vid }})</mat-option>
          }
        </mat-select>
        <mat-hint>
          Groups let the same VLAN ID exist in more than one layer-2 domain — e.g. VID 100 at two sites.
        </mat-hint>
      </mat-form-field>

      <mat-form-field appearance="outline" class="full">
        <mat-label>Status</mat-label>
        <mat-select [(ngModel)]="status" [disabled]="submitting()">
          @for (option of statuses; track option) {
            <mat-option [value]="option">{{ option }}</mat-option>
          }
        </mat-select>
        <mat-hint>{{ statusHelp[status] }}</mat-hint>
      </mat-form-field>

      <mat-form-field appearance="outline" class="full">
        <mat-label>Description</mat-label>
        <textarea
          matInput
          rows="3"
          [(ngModel)]="description"
          placeholder="What is this VLAN for?"
          [disabled]="submitting()"
        ></textarea>
      </mat-form-field>
    </mat-dialog-content>

    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="null" [disabled]="submitting()">Cancel</button>
      <button mat-flat-button color="primary" [disabled]="!canSubmit() || submitting()" (click)="submit()">
        @if (submitting()) {
          <mat-spinner diameter="18" />
        } @else {
          {{ data.mode === 'create' ? 'Create VLAN' : 'Save changes' }}
        }
      </button>
    </mat-dialog-actions>
  `,
  styles: [
    `
      .full {
        width: 100%;
      }

      .row {
        display: flex;
        gap: 12px;
      }

      .row .vid {
        width: 120px;
        flex: 0 0 auto;
      }

      .row .grow {
        flex: 1;
        min-width: 0;
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
export class VlanFormDialogComponent {
  private readonly api = inject(ApiService);
  private readonly dialogRef = inject(MatDialogRef<VlanFormDialogComponent, VLAN | null>);

  vid: number | null;
  name: string;
  groupId: string;
  status: VLANStatus;
  description: string;

  readonly statuses: VLANStatus[] = ['active', 'reserved', 'deprecated'];
  readonly statusHelp = STATUS_HELP;

  readonly submitting = signal(false);
  readonly bannerError = signal<string | null>(null);
  readonly fieldError = signal<string | null>(null);

  constructor(@Inject(MAT_DIALOG_DATA) readonly data: VlanFormDialogData) {
    this.vid = data.vlan?.vid ?? null;
    this.name = data.vlan?.name ?? '';
    this.groupId = data.vlan?.group_id ?? '';
    this.status = data.vlan?.status ?? 'active';
    this.description = data.vlan?.description ?? '';
  }

  canSubmit(): boolean {
    return !!this.name.trim() && this.vid !== null && this.vid >= 1 && this.vid <= 4094;
  }

  submit(): void {
    if (!this.canSubmit() || this.submitting()) return;

    this.submitting.set(true);
    this.bannerError.set(null);
    this.fieldError.set(null);

    const body = {
      vid: this.vid!,
      name: this.name.trim(),
      group_id: this.groupId || null,
      status: this.status,
      description: this.description.trim() || null,
    };

    const result$ =
      this.data.mode === 'create'
        ? this.api.createVLAN(body)
        : this.api.updateVLAN(this.data.vlan!.id, body);

    result$.subscribe({
      next: (vlan) => this.dialogRef.close(vlan),
      error: (err) => {
        this.submitting.set(false);
        const friendly = extractApiError(err, 'Could not save this VLAN.');
        this.bannerError.set(friendly.message);
        this.fieldError.set(friendly.field);
      },
    });
  }
}
