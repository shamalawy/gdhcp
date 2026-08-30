import { ChangeDetectionStrategy, Component, EventEmitter, Output, computed, inject, input, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';

import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { OptionTooltipComponent } from '../../shared/components/option-tooltip/option-tooltip.component';
import { OptionScopeLevel, OptionValue, OptionValueType } from '../../shared/models';
import { OptionCatalogService } from './option-catalog.service';
import { OptionPickerDialogComponent, OptionPickerResult } from './option-picker-dialog.component';
import { OptionValueEditorComponent } from './option-value-editor.component';

export interface OptionLayer {
  level: OptionScopeLevel;
  label: string;
  values: OptionValue[];
}

interface EffectiveRow {
  option_code: number;
  name: string;
  description: string;
  value_type: OptionValueType;
  value: string;
  sourceLevel: OptionScopeLevel;
  sourceLabel: string;
  /** Set when this row's winning value came from the level this component
   * instance is editing — i.e. it's editable/removable right here. */
  own: OptionValue | null;
}

/** Renders the inheritance-resolved ("effective") view of DHCP option
 * values for one scope/pool/template, per docs/design/frontend.md's
 * "DHCPOptionsEditorComponent" and the product spec: one row per option
 * code showing whichever value wins (reservation > pool > scope > template
 * > global — A-doc precedence), with a badge naming the level it came from,
 * plus add/edit/remove for the level this instance owns.
 *
 * Callers supply already-fetched `inheritedLayers` (ordered lowest-to-
 * highest precedence — this component does not know how to fetch other
 * levels' values, keeping it decoupled from which ApiService calls apply
 * where) and the level's own values separately; persistence (calling the
 * right ApiService method) is the caller's job via the `add`/`update`/
 * `remove` outputs, since scope/pool/template each hit different endpoints.
 *
 * Among multiple attached templates, "wins" is decided by array order in
 * `inheritedLayers` (later entries override earlier ones) — callers sort
 * template layers by priority ascending, so the highest-priority-number
 * template wins a conflict. See notes_for_integration: CONTRACTS.md doesn't
 * pin this direction, so it's this unit's documented assumption.
 */
@Component({
  selector: 'app-effective-options',
  standalone: true,
  imports: [MatButtonModule, MatIconModule, OptionTooltipComponent, OptionValueEditorComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="effective-options">
      <table class="options-table">
        <thead>
          <tr>
            <th class="col-code">Code</th>
            <th class="col-name">Option</th>
            <th class="col-value">Value</th>
            <th class="col-source">Source</th>
            @if (editable()) {
              <th class="col-actions"></th>
            }
          </tr>
        </thead>
        <tbody>
          @for (row of rows(); track row.option_code) {
            <tr [class.editing]="editingCode() === row.option_code">
              @if (editingCode() === row.option_code && row.own) {
                <td class="col-code gdhcp-mono">{{ row.option_code }}</td>
                <td class="col-name">
                  {{ row.name }}
                  <app-option-tooltip [optionCode]="row.option_code" [entry]="catalogEntry(row.option_code)" />
                </td>
                <td class="col-value" colspan="2">
                  <app-option-value-editor
                    [valueType]="row.value_type"
                    [value]="editDraft()"
                    (valueChange)="editDraft.set($event)"
                    (validityChange)="editValid.set($event)"
                  />
                </td>
                <td class="col-actions">
                  <button mat-icon-button [disabled]="!editValid()" (click)="commitEdit(row)" aria-label="Save value">
                    <mat-icon>check</mat-icon>
                  </button>
                  <button mat-icon-button (click)="cancelEdit()" aria-label="Cancel edit">
                    <mat-icon>close</mat-icon>
                  </button>
                </td>
              } @else {
                <td class="col-code gdhcp-mono">{{ row.option_code }}</td>
                <td class="col-name">
                  {{ row.name }}
                  <app-option-tooltip [optionCode]="row.option_code" [entry]="catalogEntry(row.option_code)" />
                </td>
                <td class="col-value gdhcp-mono">{{ row.value }}</td>
                <td class="col-source">
                  <span class="badge" [class]="'level-' + row.sourceLevel">{{ row.sourceLabel }}</span>
                </td>
                @if (editable()) {
                  <td class="col-actions">
                    @if (row.own) {
                      <button mat-icon-button (click)="startEdit(row)" [attr.aria-label]="'Edit ' + row.name">
                        <mat-icon>edit</mat-icon>
                      </button>
                      <button mat-icon-button (click)="removeRow(row)" [attr.aria-label]="'Remove ' + row.name">
                        <mat-icon>delete</mat-icon>
                      </button>
                    } @else {
                      <span class="inherited-hint gdhcp-muted">inherited</span>
                    }
                  </td>
                }
              }
            </tr>
          }
          @if (rows().length === 0) {
            <tr>
              <td [attr.colspan]="editable() ? 5 : 4" class="empty">No options resolved for this entity yet.</td>
            </tr>
          }
        </tbody>
      </table>

      @if (canAdd()) {
        <button mat-stroked-button class="add-btn" (click)="openPicker()">
          <mat-icon>add</mat-icon>
          Add value at {{ ownLabel() }}
        </button>
      }
    </div>
  `,
  styles: [
    `
      .effective-options {
        display: flex;
        flex-direction: column;
        gap: 12px;
      }
      .options-table {
        width: 100%;
        border-collapse: collapse;
        font-size: 13px;
      }
      .options-table th {
        text-align: left;
        font-size: 11px;
        text-transform: uppercase;
        letter-spacing: 0.03em;
        color: var(--text-secondary);
        border-bottom: 1px solid var(--border-color);
        padding: 6px 8px;
      }
      .options-table td {
        padding: 6px 8px;
        border-bottom: 1px solid var(--divider-color);
        vertical-align: middle;
      }
      tr.editing td {
        vertical-align: top;
        padding-top: 8px;
      }
      .col-code {
        width: 48px;
        color: var(--text-secondary);
      }
      .col-actions {
        width: 92px;
        text-align: right;
        white-space: nowrap;
      }
      .badge {
        display: inline-block;
        font-size: 11px;
        padding: 1px 8px;
        border-radius: 10px;
        border: 1px solid var(--border-color);
        color: var(--text-secondary);
      }
      .badge.level-global {
        background: var(--bg-secondary);
      }
      .badge.level-template {
        background: var(--bg-highlight);
        color: var(--primary-color);
        border-color: transparent;
      }
      .badge.level-scope {
        background: var(--active-bg);
        color: var(--primary-color);
        border-color: transparent;
      }
      .badge.level-pool,
      .badge.level-reservation {
        background: var(--accent-color);
        color: #fff;
        border-color: transparent;
      }
      .inherited-hint {
        font-size: 11px;
      }
      .empty {
        text-align: center;
        color: var(--text-secondary);
        padding: 20px;
      }
      .add-btn {
        align-self: flex-start;
      }
    `,
  ],
})
export class EffectiveOptionsComponent {
  private readonly catalogSvc = inject(OptionCatalogService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmSvc = inject(ConfirmDialogService);

  /** The level this instance can add/edit/remove values at. */
  readonly ownLevel = input.required<OptionScopeLevel>();
  readonly ownLabel = input('this level');
  readonly ownValues = input.required<OptionValue[]>();
  /** Lower-precedence layers (global/template/scope, as applicable),
   * ordered lowest-to-highest precedence. Empty for a from-scratch view. */
  readonly inheritedLayers = input<OptionLayer[]>([]);
  /** Template context has no update/remove endpoint (endpoints.md defines
   * only GET/POST for `/templates/{id}/options`) — callers pass `false` to
   * hide the per-row edit/remove affordances (`canAdd` stays independently
   * controllable so "add-only" contexts like templates still get the add
   * button). */
  readonly editable = input(true);
  readonly canAdd = input(true);

  @Output() readonly add = new EventEmitter<OptionPickerResult>();
  @Output() readonly update = new EventEmitter<{ id: string; option_code: number; value: string; is_enabled: boolean }>();
  @Output() readonly remove = new EventEmitter<OptionValue>();

  readonly editingCode = signal<number | null>(null);
  readonly editDraft = signal('');
  readonly editValid = signal(false);

  constructor() {
    this.catalogSvc.ensureLoaded();
  }

  private readonly layers = computed<OptionLayer[]>(() => [
    ...this.inheritedLayers(),
    { level: this.ownLevel(), label: this.ownLabel(), values: this.ownValues() },
  ]);

  readonly rows = computed<EffectiveRow[]>(() => {
    const map = new Map<number, EffectiveRow>();
    for (const layer of this.layers()) {
      for (const v of layer.values) {
        if (!v.is_enabled) continue;
        const catalog = this.catalogSvc.lookup(v.option_code);
        map.set(v.option_code, {
          option_code: v.option_code,
          name: catalog?.name ?? `Option ${v.option_code}`,
          description: catalog?.description ?? '',
          value_type: catalog?.value_type ?? 'string',
          value: v.value,
          sourceLevel: layer.level,
          sourceLabel: layer.level === this.ownLevel() ? this.ownLabel() : layer.label,
          own: layer.level === this.ownLevel() ? v : null,
        });
      }
    }
    return Array.from(map.values()).sort((a, b) => a.option_code - b.option_code);
  });

  catalogEntry(code: number) {
    return this.catalogSvc.lookup(code);
  }

  startEdit(row: EffectiveRow): void {
    this.editingCode.set(row.option_code);
    this.editDraft.set(row.value);
    this.editValid.set(true);
  }

  cancelEdit(): void {
    this.editingCode.set(null);
  }

  commitEdit(row: EffectiveRow): void {
    if (!row.own || !this.editValid()) return;
    this.update.emit({ id: row.own.id, option_code: row.option_code, value: this.editDraft(), is_enabled: row.own.is_enabled });
    this.editingCode.set(null);
  }

  removeRow(row: EffectiveRow): void {
    if (!row.own) return;
    this.confirmSvc
      .confirm({
        title: 'Remove option value',
        message: `Remove the ${row.name} (option ${row.option_code}) value set at ${this.ownLabel()}? Lower-precedence values, if any, will take effect instead.`,
        danger: true,
      })
      .subscribe((ok) => {
        if (ok && row.own) this.remove.emit(row.own);
      });
  }

  openPicker(): void {
    const excludeCodes = this.ownValues().map((v) => v.option_code);
    this.dialog
      .open<OptionPickerDialogComponent, { excludeCodes: number[]; title: string }, OptionPickerResult | undefined>(
        OptionPickerDialogComponent,
        { data: { excludeCodes, title: `Add option value at ${this.ownLabel()}` }, width: '520px' },
      )
      .afterClosed()
      .subscribe((result) => {
        if (result) this.add.emit(result);
      });
  }
}
