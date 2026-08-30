import { ChangeDetectionStrategy, Component, Inject, computed, signal } from '@angular/core';
import { AbstractControl, FormBuilder, ReactiveFormsModule, ValidationErrors, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';

import { BindingType, DHCPReservation } from '../../shared/models';
import { ipInRange, isValidIPv4, isValidMac, normalizeMac } from './ip-utils';

export interface ReservationFormDialogData {
  poolRangeStart?: string;
  poolRangeEnd?: string;
  reservation?: DHCPReservation;
  // Prefill data for "reserve this client" actions
  ip?: string;
  mac?: string;
  clientId?: string;
  hostname?: string;
}

export interface ReservationFormResult {
  reserved_ip: string;
  binding_type: BindingType;
  binding_key: string;
  hostname: string | null;
  description: string | null;
}

/** Split a prefixed binding key (e.g., "mac:aa:bb:cc:00:01:10") into type and value. */
function splitBindingKey(key: string): { type: BindingType; value: string } | null {
  const colonIdx = key.indexOf(':');
  if (colonIdx < 0) return null;
  const type = key.substring(0, colonIdx);
  const value = key.substring(colonIdx + 1);
  const types: BindingType[] = ['mac', 'client_id', 'hostname', 'circuit_id', 'remote_id', 'vendor_class', 'user_class'];
  if (types.includes(type as BindingType)) {
    return { type: type as BindingType, value };
  }
  return null;
}

function ipInPoolValidator(rangeStart: string | undefined, rangeEnd: string | undefined) {
  return (control: AbstractControl): ValidationErrors | null => {
    if (!rangeStart || !rangeEnd) return null; // No pool range validation if not provided
    const ip = control.value as string;
    if (!ip || !isValidIPv4(ip)) return null;
    return ipInRange(ip, rangeStart, rangeEnd) ? null : { outsidePool: true };
  };
}

type BindingValueMode = 'text' | 'hex';

const OPAQUE_TYPES: readonly BindingType[] = ['client_id', 'circuit_id', 'remote_id', 'vendor_class', 'user_class'];

/** True for the identifier types whose wire value is arbitrary bytes and which
 * are therefore stored as hex (see AMENDMENTS A17.2). */
function isOpaque(type: BindingType | undefined): boolean {
  return !!type && OPAQUE_TYPES.includes(type);
}

/** Hex-encode a string's UTF-8 bytes, matching the server's
 * models.CanonicalBindingValue for a text-mode opaque value. */
function textToHex(value: string): string {
  return Array.from(new TextEncoder().encode(value))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('');
}

function isHexString(value: string): boolean {
  return value.length > 0 && value.length % 2 === 0 && /^[0-9a-fA-F]+$/.test(value);
}

function bindingKeyValidator(control: AbstractControl): ValidationErrors | null {
  const type = control.parent?.get('binding_type')?.value as BindingType | undefined;
  const mode = control.parent?.get('binding_value_mode')?.value as BindingValueMode | undefined;
  const key = control.value as string;
  if (type === 'mac' && key && !isValidMac(key)) return { invalidMac: true };
  if (type === 'hostname' && key && !/^[a-z0-9-]+(\.[a-z0-9-]+)*$/.test(key)) return { invalidHostname: true };
  // In hex mode the operator is asserting the value IS hex, so hold them to
  // it rather than silently hex-encoding their typo as text.
  if (isOpaque(type) && mode === 'hex' && key && !isHexString(key)) return { invalidHex: true };
  return null;
}

/** Add/edit reservation dialog — validates the reserved IP falls within the
 * owning pool's range before allowing submit (when pool range is provided).
 * Supports all 7 binding types with per-type validation and UI hints. */
@Component({
  selector: 'app-reservation-form-dialog',
  standalone: true,
  imports: [ReactiveFormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatInputModule, MatSelectModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>{{ data.reservation ? 'Edit reservation' : 'New reservation' }}</h2>
    <mat-dialog-content>
      <form [formGroup]="form" class="form">
        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Reserved IP</mat-label>
          <input matInput formControlName="reserved_ip" class="gdhcp-mono" placeholder="10.1.0.50" [disabled]="isEdit()" />
          @if (data.poolRangeStart && data.poolRangeEnd) {
            <mat-hint>Must fall within the pool's range ({{ data.poolRangeStart }}–{{ data.poolRangeEnd }}).</mat-hint>
          }
          @if (form.controls.reserved_ip.hasError('outsidePool')) {
            <mat-error>Outside the pool's range ({{ data.poolRangeStart }}–{{ data.poolRangeEnd }}).</mat-error>
          }
          @if (form.controls.reserved_ip.hasError('pattern')) {
            <mat-error>Not a valid IPv4 address.</mat-error>
          }
        </mat-form-field>

        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Binding type</mat-label>
          <mat-select formControlName="binding_type" [disabled]="isEdit()" (selectionChange)="onTypeChange()">
            <mat-option value="mac">MAC address</mat-option>
            <mat-option value="client_id">Client ID (option 61)</mat-option>
            <mat-option value="hostname">Hostname (option 12)</mat-option>
            <mat-option value="circuit_id">Circuit ID (option 82/1)</mat-option>
            <mat-option value="remote_id">Remote ID (option 82/2)</mat-option>
            <mat-option value="vendor_class">Vendor class (option 60)</mat-option>
            <mat-option value="user_class">User class (option 77)</mat-option>
          </mat-select>
        </mat-form-field>

        @if (isOpaqueType()) {
          <mat-form-field appearance="outline" subscriptSizing="dynamic">
            <mat-label>Value is</mat-label>
            <mat-select formControlName="binding_value_mode" (selectionChange)="onModeChange()">
              <mat-option value="text">Text — encoded to hex for you</mat-option>
              <mat-option value="hex">Hex — stored as typed</mat-option>
            </mat-select>
          </mat-form-field>
        }

        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>{{ bindingKeyLabel() }}</mat-label>
          <input matInput formControlName="binding_key" [class.gdhcp-mono]="!isTextInputType()" [placeholder]="bindingKeyPlaceholder()" />
          <mat-hint>{{ bindingKeyHint() }}</mat-hint>
          @if (form.controls.binding_key.hasError('invalidHex')) {
            <mat-error>Not valid hex (an even number of 0-9 a-f digits).</mat-error>
          }
          @if (form.controls.binding_key.hasError('invalidMac')) {
            <mat-error>Not a valid MAC address (expected aa:bb:cc:dd:ee:ff).</mat-error>
          }
          @if (form.controls.binding_key.hasError('invalidHostname')) {
            <mat-error>Not a valid hostname.</mat-error>
          }
        </mat-form-field>

        @if (isOpaqueType() && storedPreview()) {
          <p class="stored-preview">Stored as <span class="gdhcp-mono">{{ storedPreview() }}</span></p>
        }

        <mat-form-field appearance="outline" subscriptSizing="dynamic">
          <mat-label>Hostname</mat-label>
          <input matInput formControlName="hostname" placeholder="Optional" />
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
        {{ data.reservation ? 'Save' : 'Create' }}
      </button>
    </mat-dialog-actions>
  `,
  styles: [
    `
      .stored-preview {
        margin: 2px 0 0;
        font-size: 12px;
        opacity: 0.7;
      }
      .form {
        display: flex;
        flex-direction: column;
        gap: 4px;
        width: 360px;
        max-width: 80vw;
        padding-top: 4px;
      }
      input.gdhcp-mono,
      textarea.gdhcp-mono {
        font-family: monospace;
        font-size: 12px;
      }
    `,
  ],
})
export class ReservationFormDialogComponent {
  readonly form;
  readonly isEdit = computed(() => !!this.data.reservation);

  constructor(
    private readonly fb: FormBuilder,
    private readonly ref: MatDialogRef<ReservationFormDialogComponent, ReservationFormResult | undefined>,
    @Inject(MAT_DIALOG_DATA) readonly data: ReservationFormDialogData,
  ) {
    const ipPattern = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
    const isEdit = !!data.reservation;

    let initialBindingType: BindingType = 'mac';
    let initialBindingKey = '';
    // An existing or prefilled opaque value is already hex.
    let initialMode: BindingValueMode = 'hex';
    if (data.reservation) {
      const parsed = splitBindingKey(data.reservation.binding_key);
      if (parsed) {
        initialBindingType = parsed.type;
        initialBindingKey = parsed.value;
      }
    } else if (data.clientId) {
      initialBindingType = 'client_id';
      initialBindingKey = data.clientId;
    } else if (data.mac) {
      initialBindingType = 'mac';
      initialBindingKey = data.mac;
    } else if (data.hostname) {
      initialBindingType = 'hostname';
      initialBindingKey = data.hostname;
    }

    // reserved_ip and binding_type aren't in UpdateReservationRequest — the
    // API only lets binding_key/hostname/description/is_enabled change on
    // an existing reservation, so both are locked (disabled) when editing.
    this.form = this.fb.nonNullable.group({
      reserved_ip: [
        { value: data.reservation?.reserved_ip ?? data.ip ?? '', disabled: isEdit },
        [Validators.required, Validators.pattern(ipPattern), ipInPoolValidator(data.poolRangeStart, data.poolRangeEnd)],
      ],
      binding_type: [{ value: initialBindingType, disabled: isEdit }, Validators.required],
      // Opaque identifiers are stored as hex. "1234" is both valid hex and
      // plausible text, so the operator says which they mean rather than
      // leaving the server to guess (AMENDMENTS A17.2).
      binding_value_mode: [initialMode as BindingValueMode],
      binding_key: [initialBindingKey, [Validators.required, bindingKeyValidator]],
      hostname: [data.reservation?.hostname ?? data.hostname ?? ''],
      description: [data.reservation?.description ?? ''],
    });
  }

  bindingKeyLabel(): string {
    const type = this.form.controls.binding_type.value as BindingType;
    switch (type) {
      case 'mac':
        return 'MAC address';
      case 'client_id':
        return 'Client ID';
      case 'hostname':
        return 'Hostname';
      case 'circuit_id':
        return 'Circuit ID';
      case 'remote_id':
        return 'Remote ID';
      case 'vendor_class':
        return 'Vendor class';
      case 'user_class':
        return 'User class';
      default:
        return 'Identifier';
    }
  }

  bindingKeyPlaceholder(): string {
    const type = this.form.controls.binding_type.value as BindingType;
    switch (type) {
      case 'mac':
        return 'aa:bb:cc:dd:ee:ff';
      case 'hostname':
        return 'printer.example.com';
      case 'client_id':
      case 'circuit_id':
      case 'remote_id':
      case 'vendor_class':
      case 'user_class':
        return 'Hex or text';
      default:
        return '';
    }
  }

  bindingKeyHint(): string {
    const type = this.form.controls.binding_type.value as BindingType;
    switch (type) {
      case 'mac':
        return 'Format: aa:bb:cc:dd:ee:ff';
      case 'hostname':
        return 'Must be a valid hostname.';
      case 'client_id':
        return 'Option 61: hex (e.g., 01aabbcc) or text. Stored as-is.';
      case 'circuit_id':
        return 'Option 82/1 (relay): hex or text.';
      case 'remote_id':
        return 'Option 82/2 (relay): hex or text.';
      case 'vendor_class':
        return 'Option 60: hex or text.';
      case 'user_class':
        return 'Option 77: hex or text.';
      default:
        return '';
    }
  }

  isTextInputType(): boolean {
    const type = this.form.controls.binding_type.value as BindingType;
    return type === 'hostname' || (this.isOpaqueType() && this.form.controls.binding_value_mode.value === 'text');
  }

  isOpaqueType(): boolean {
    return isOpaque(this.form.controls.binding_type.value as BindingType);
  }

  /** What will actually be stored, so the operator can see the encoding. */
  storedPreview(): string {
    const value = (this.form.controls.binding_key.value as string).trim();
    if (!value) return '';
    const hex = this.form.controls.binding_value_mode.value === 'text' ? textToHex(value) : value.toLowerCase();
    return `${this.form.controls.binding_type.value}:${hex}`;
  }

  onTypeChange(): void {
    this.form.controls.binding_key.reset('');
    this.form.controls.binding_value_mode.setValue('hex');
    this.form.controls.binding_key.updateValueAndValidity();
  }

  onModeChange(): void {
    this.form.controls.binding_key.updateValueAndValidity();
  }

  save(): void {
    if (this.form.invalid) return;
    const raw = this.form.getRawValue();
    let bindingKey = raw.binding_key.trim();
    if (raw.binding_type === 'mac') {
      bindingKey = normalizeMac(bindingKey);
    } else if (raw.binding_type === 'hostname') {
      bindingKey = bindingKey.toLowerCase();
    } else if (isOpaque(raw.binding_type)) {
      // Always submit hex: it round-trips through the server's canonicaliser
      // unchanged, so a value like "1234" cannot be mistaken for text.
      bindingKey = raw.binding_value_mode === 'text' ? textToHex(bindingKey) : bindingKey.toLowerCase();
    }
    this.ref.close({
      reserved_ip: raw.reserved_ip,
      binding_type: raw.binding_type,
      binding_key: bindingKey,
      hostname: raw.hostname || null,
      description: raw.description || null,
    });
  }
}
