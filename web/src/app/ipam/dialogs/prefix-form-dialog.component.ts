import { ChangeDetectionStrategy, Component, Inject, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatSelectModule } from '@angular/material/select';

import { ApiService } from '../../shared/api.service';
import { IPAMPrefix, PrefixType, VLAN } from '../../shared/models';
import { cidrContains, isValidCidr } from '../util/cidr.util';
import { extractApiError } from '../util/api-error.util';
import { PREFIX_TYPE_META, PREFIX_TYPE_ORDER } from '../util/prefix-type.util';

export interface PrefixFormDialogData {
  vrfId: string;
  vrfName: string;
  mode: 'create' | 'edit';
  /** Required when `mode: 'edit'`. */
  prefix?: IPAMPrefix;
  /** Set when creating a child of a specific prefix, purely for the
   * containment hint — `CreatePrefixRequest` has no `parent_id` field
   * (nesting is computed server-side from CIDR containment; see
   * notes_for_integration). */
  parentPrefix?: IPAMPrefix;
  /** Pre-fills the CIDR field, e.g. from the "next available subnet"
   * helper. */
  initialCidr?: string;
}

/** Create/edit dialog for an IPAM prefix. Handles both "add prefix" (VRF
 * detail) and "add child prefix" (prefix detail) — they're the same
 * operation from the API's point of view, since the server determines
 * where a new prefix nests purely from CIDR containment against existing
 * prefixes in the same VRF. */
@Component({
  selector: 'app-prefix-form-dialog',
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
    <h2 mat-dialog-title>
      {{ data.mode === 'create' ? (data.parentPrefix ? 'Add child prefix' : 'Add prefix') : 'Edit prefix' }}
    </h2>

    <mat-dialog-content>
      <p class="explainer">
        VRF <strong>{{ data.vrfName }}</strong>
        @if (data.parentPrefix) {
          , nested under <strong class="gdhcp-mono">{{ data.parentPrefix.prefix }}</strong>.
        } @else {
          .
        }
        Where a prefix nests is determined automatically from its CIDR range, not chosen here.
      </p>

      @if (bannerError() && fieldError() !== 'cidr') {
        <div class="banner-error">
          <mat-icon>error_outline</mat-icon>
          <span>{{ bannerError() }}</span>
        </div>
      }

      <mat-form-field appearance="outline" class="full">
        <mat-label>CIDR</mat-label>
        <input
          matInput
          class="gdhcp-mono"
          [(ngModel)]="cidr"
          (ngModelChange)="onCidrChange()"
          placeholder="10.1.5.0/24"
          [disabled]="data.mode === 'edit' || submitting()"
        />
        @if (data.mode === 'edit') {
          <mat-hint>CIDR cannot be changed after creation.</mat-hint>
        } @else if (cidr && !cidrValid()) {
          <mat-hint>Enter a valid IPv4 CIDR, e.g. 10.1.5.0/24.</mat-hint>
        }
      </mat-form-field>
      @if (fieldError() === 'cidr') {
        <p class="field-error">{{ bannerError() }}</p>
      }
      @if (containmentWarning()) {
        <p class="field-warning">
          <mat-icon inline>warning</mat-icon>
          {{ containmentWarning() }}
        </p>
      }

      <mat-form-field appearance="outline" class="full">
        <mat-label>Name</mat-label>
        <input matInput [(ngModel)]="name" maxlength="100" [disabled]="submitting()" />
      </mat-form-field>

      <mat-form-field appearance="outline" class="full">
        <mat-label>Type</mat-label>
        <mat-select [(ngModel)]="type" [disabled]="submitting()">
          @for (t of typeOrder; track t) {
            <mat-option [value]="t">{{ typeMeta(t).label }}</mat-option>
          }
        </mat-select>
        <mat-hint>{{ typeMeta(type).help }}</mat-hint>
      </mat-form-field>

      <mat-form-field appearance="outline" class="full">
        <mat-label>VLAN</mat-label>
        <mat-select [(ngModel)]="vlanId" [disabled]="submitting()">
          <mat-option [value]="''">None</mat-option>
          @for (vlan of vlans(); track vlan.id) {
            <mat-option [value]="vlan.id">
              {{ vlan.vid }} — {{ vlan.name }}@if (vlan.group_name) { ({{ vlan.group_name }}) }
            </mat-option>
          }
        </mat-select>
        <mat-hint>Which layer-2 segment this address block serves. Optional.</mat-hint>
      </mat-form-field>

      <mat-form-field appearance="outline" class="full">
        <mat-label>Description</mat-label>
        <textarea matInput rows="3" [(ngModel)]="description" [disabled]="submitting()"></textarea>
      </mat-form-field>
    </mat-dialog-content>

    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="null" [disabled]="submitting()">Cancel</button>
      <button mat-flat-button color="primary" [disabled]="!canSubmit() || submitting()" (click)="submit()">
        @if (submitting()) {
          <mat-spinner diameter="18" />
        } @else {
          {{ data.mode === 'create' ? 'Create prefix' : 'Save changes' }}
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
        margin: -12px 0 12px;
      }

      .field-warning {
        display: flex;
        align-items: center;
        gap: 4px;
        color: var(--status-warn);
        font-size: 12px;
        margin: -12px 0 12px;
      }

      .field-warning mat-icon {
        font-size: 16px;
        width: 16px;
        height: 16px;
      }
    `,
  ],
})
export class PrefixFormDialogComponent {
  private readonly api = inject(ApiService);
  private readonly dialogRef = inject(MatDialogRef<PrefixFormDialogComponent, IPAMPrefix | null>);

  readonly typeOrder = PREFIX_TYPE_ORDER;

  cidr: string;
  name: string;
  type: PrefixType;
  description: string;
  vlanId: string;

  readonly vlans = signal<VLAN[]>([]);

  readonly submitting = signal(false);
  readonly bannerError = signal<string | null>(null);
  readonly fieldError = signal<string | null>(null);
  private readonly cidrSignal = signal('');

  readonly cidrValid = computed(() => isValidCidr(this.cidrSignal()));

  readonly containmentWarning = computed(() => {
    const parent = this.data.parentPrefix;
    if (!parent || this.data.mode === 'edit') return null;
    const value = this.cidrSignal();
    if (!value || !isValidCidr(value)) return null;
    if (cidrContains(parent.prefix, value)) return null;
    return `${value} is not inside ${parent.prefix} — the server will place it wherever it actually nests (or at the top level of this VRF).`;
  });

  readonly canSubmit = computed(() => {
    if (this.data.mode === 'edit') return true;
    return this.cidrValid();
  });

  constructor(@Inject(MAT_DIALOG_DATA) readonly data: PrefixFormDialogData) {
    this.cidr = data.mode === 'edit' ? data.prefix!.prefix : (data.initialCidr ?? '');
    this.cidrSignal.set(this.cidr);
    this.name = data.mode === 'edit' ? (data.prefix!.name ?? '') : '';
    this.type = data.mode === 'edit' ? data.prefix!.prefix_type : (data.parentPrefix ? 'subnet' : 'aggregate');
    this.description = data.mode === 'edit' ? (data.prefix!.description ?? '') : '';
    this.vlanId = data.mode === 'edit' ? (data.prefix!.vlan_id ?? '') : '';

    this.api.listVLANs({ limit: 500, status: 'active' }).subscribe({
      next: (page) => this.vlans.set(page.items),
      // A VLAN list failure must not block prefix editing — the select just
      // stays empty and the prefix keeps whatever VLAN it already had.
      error: () => this.vlans.set([]),
    });
  }

  onCidrChange(): void {
    this.cidrSignal.set(this.cidr);
  }

  typeMeta(t: PrefixType) {
    return PREFIX_TYPE_META[t];
  }

  submit(): void {
    if (!this.canSubmit() || this.submitting()) return;

    this.submitting.set(true);
    this.bannerError.set(null);
    this.fieldError.set(null);

    const name = this.name.trim() || null;
    const description = this.description.trim() || null;

    const result$ =
      this.data.mode === 'create'
        ? this.api.createPrefix({
            vrf_id: this.data.vrfId,
            cidr: this.cidr.trim(),
            type: this.type,
            name,
            description,
            vlan_id: this.vlanId || null,
          })
        : this.api.updatePrefix(this.data.prefix!.id, {
            name,
            description,
            type: this.type,
            vlan_id: this.vlanId || null,
          });

    result$.subscribe({
      next: (prefix) => this.dialogRef.close(prefix),
      error: (err) => {
        this.submitting.set(false);
        const friendly = extractApiError(
          err,
          this.data.mode === 'create' ? 'Could not create this prefix.' : 'Could not save this prefix.',
        );
        this.bannerError.set(friendly.message);
        this.fieldError.set(friendly.field ?? (friendly.code === 'CONFLICT' ? 'cidr' : null));
      },
    });
  }
}
