import { ChangeDetectionStrategy, Component, EventEmitter, Output, computed, effect, input, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';

import { OptionValueType } from '../../shared/models';
import { isValidIPv4, parseCIDR } from './ip-utils';

function validateValue(type: OptionValueType, raw: string): boolean {
  const draft = raw.trim();
  switch (type) {
    case 'string':
    case 'fqdn':
    case 'bytes':
      return draft.length > 0;
    case 'uint8':
      return isUintInRange(draft, 255);
    case 'uint16':
      return isUintInRange(draft, 65535);
    case 'uint32':
      return isUintInRange(draft, 4294967295);
    case 'boolean':
      return draft === 'true' || draft === 'false';
    case 'ip':
      return isValidIPv4(draft);
    case 'ip_list':
      return splitList(draft).length > 0 && splitList(draft).every(isValidIPv4);
    case 'route_list':
      return splitLines(draft).length > 0 && splitLines(draft).every(isValidRouteLine);
  }
}

function isUintInRange(raw: string, max: number): boolean {
  if (!/^\d+$/.test(raw)) return false;
  const n = Number(raw);
  return Number.isInteger(n) && n >= 0 && n <= max;
}

function splitList(raw: string): string[] {
  return raw
    .split(/[,\n]/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

function splitLines(raw: string): string[] {
  return raw
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

function isValidRouteLine(line: string): boolean {
  const tokens = line.split(/\s+via\s+|\s+/).filter(Boolean);
  if (tokens.length !== 2) return false;
  const [cidr, gateway] = tokens;
  return parseCIDR(cidr) !== null && isValidIPv4(gateway);
}

function hintFor(type: OptionValueType): string {
  switch (type) {
    case 'uint8':
      return 'Whole number, 0–255';
    case 'uint16':
      return 'Whole number, 0–65535';
    case 'uint32':
      return 'Whole number, 0–4294967295';
    case 'ip':
      return 'Single IPv4 address, e.g. 10.1.0.1';
    case 'ip_list':
      return 'One or more IPv4 addresses, comma- or newline-separated';
    case 'route_list':
      return 'One classless static route per line: <network>/<prefix> via <gateway-ip>';
    case 'fqdn':
      return 'Fully-qualified domain name';
    case 'bytes':
      return 'Raw option payload (hex or literal text, per option definition)';
    default:
      return 'Free-text value';
  }
}

/** Renders (and validates) the right input control for a DHCP option's
 * `value: string` field based on its catalog `value_type` — used by
 * `EffectiveOptionsComponent`'s inline editors and the option-picker
 * "add option" dialog. The serialized `value` string is always what flows
 * in/out; this component only adapts the *input widget* to the type. */
@Component({
  selector: 'app-option-value-editor',
  standalone: true,
  imports: [FormsModule, MatFormFieldModule, MatInputModule, MatSlideToggleModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @switch (valueType()) {
      @case ('boolean') {
        <mat-slide-toggle [ngModel]="draft() === 'true'" [disabled]="disabled()" (ngModelChange)="onBoolChange($event)">
          {{ draft() === 'true' ? 'True' : 'False' }}
        </mat-slide-toggle>
      }
      @case ('ip_list') {
        <mat-form-field appearance="outline" subscriptSizing="dynamic" class="full">
          <mat-label>Value</mat-label>
          <textarea matInput rows="3" class="gdhcp-mono" [ngModel]="draft()" [disabled]="disabled()" (ngModelChange)="onTextChange($event)"></textarea>
          <mat-hint>{{ hint() }}</mat-hint>
        </mat-form-field>
      }
      @case ('route_list') {
        <mat-form-field appearance="outline" subscriptSizing="dynamic" class="full">
          <mat-label>Value</mat-label>
          <textarea matInput rows="3" class="gdhcp-mono" [ngModel]="draft()" [disabled]="disabled()" (ngModelChange)="onTextChange($event)"></textarea>
          <mat-hint>{{ hint() }}</mat-hint>
        </mat-form-field>
      }
      @default {
        <mat-form-field appearance="outline" subscriptSizing="dynamic" class="full">
          <mat-label>Value</mat-label>
          <input matInput class="gdhcp-mono" [type]="isNumeric() ? 'number' : 'text'" [ngModel]="draft()" [disabled]="disabled()" (ngModelChange)="onTextChange($event)" />
          <mat-hint>{{ hint() }}</mat-hint>
        </mat-form-field>
      }
    }
    @if (!valid() && draft().length > 0) {
      <p class="err">Value doesn't match the expected format for this option's type ({{ valueType() }}).</p>
    }
  `,
  styles: [
    `
      :host {
        display: block;
      }
      .full {
        width: 100%;
      }
      .err {
        margin: 2px 0 0;
        font-size: 11px;
        color: var(--status-error);
      }
      mat-slide-toggle {
        display: block;
        padding: 8px 0;
      }
    `,
  ],
})
export class OptionValueEditorComponent {
  readonly valueType = input.required<OptionValueType>();
  readonly value = input('');
  readonly disabled = input(false);

  @Output() readonly valueChange = new EventEmitter<string>();
  @Output() readonly validityChange = new EventEmitter<boolean>();

  private readonly draftSignal = signal('');
  readonly draft = this.draftSignal.asReadonly();

  readonly valid = computed(() => validateValue(this.valueType(), this.draftSignal()));
  readonly hint = computed(() => hintFor(this.valueType()));
  readonly isNumeric = computed(() => ['uint8', 'uint16', 'uint32'].includes(this.valueType()));

  constructor() {
    effect(
      () => {
        this.draftSignal.set(this.value());
      },
      { allowSignalWrites: true },
    );
  }

  onTextChange(next: string): void {
    this.draftSignal.set(next);
    this.emit();
  }

  onBoolChange(next: boolean): void {
    this.draftSignal.set(String(next));
    this.emit();
  }

  private emit(): void {
    this.valueChange.emit(this.draftSignal());
    this.validityChange.emit(this.valid());
  }
}
