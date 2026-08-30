import { ChangeDetectionStrategy, Component, Inject, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';

import { OptionCatalogEntry } from '../../shared/models';
import { OptionCatalogService, categoryForCode } from './option-catalog.service';
import { OptionValueEditorComponent } from './option-value-editor.component';

export interface OptionPickerDialogData {
  /** Option codes already configured at the target level — excluded from
   * the picker list (setting a value for them is an "edit", not an "add"). */
  excludeCodes: number[];
  title: string;
}

export interface OptionPickerResult {
  option_code: number;
  value: string;
}

/** Two-step "add option" dialog: pick a catalog entry (searchable, grouped
 * by a client-side category heuristic — see option-catalog.service.ts),
 * then fill in its value with the type-appropriate editor. Reused by every
 * effective-options table's "Add value" action (scope/pool/template). */
@Component({
  selector: 'app-option-picker-dialog',
  standalone: true,
  imports: [
    FormsModule,
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    OptionValueEditorComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>{{ data.title }}</h2>
    <mat-dialog-content class="content">
      @if (!selected()) {
        <mat-form-field appearance="outline" subscriptSizing="dynamic" class="full">
          <mat-icon matPrefix>search</mat-icon>
          <input matInput placeholder="Search options by code or name…" [ngModel]="query()" (ngModelChange)="query.set($event)" />
        </mat-form-field>

        <div class="groups">
          @for (group of groups(); track group.name) {
            <div class="group">
              <div class="group-label">{{ group.name }}</div>
              @for (entry of group.entries; track entry.option_code) {
                <button type="button" class="entry-row" (click)="select(entry)">
                  <span class="code gdhcp-mono">{{ entry.option_code }}</span>
                  <span class="name">{{ entry.name }}</span>
                  <span class="type">{{ entry.value_type }}</span>
                </button>
              }
            </div>
          }
          @if (groups().length === 0) {
            <p class="empty">No matching options.</p>
          }
        </div>
      } @else {
        <div class="selected-header">
          <button type="button" class="back" (click)="selected.set(null)">
            <mat-icon>arrow_back</mat-icon> Choose a different option
          </button>
        </div>
        <h3 class="selected-title gdhcp-mono">
          {{ selected()!.option_code }} — {{ selected()!.name }}
        </h3>
        <p class="selected-desc">{{ selected()!.description }}</p>
        @if (selected()!.iana_reference) {
          <p class="selected-rfc gdhcp-muted">{{ selected()!.iana_reference }}</p>
        }

        <app-option-value-editor
          [valueType]="selected()!.value_type"
          [value]="draftValue()"
          (valueChange)="draftValue.set($event)"
          (validityChange)="draftValid.set($event)"
        />
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="undefined">Cancel</button>
      @if (selected()) {
        <button mat-flat-button color="primary" [disabled]="!draftValid()" (click)="save()">Add option</button>
      }
    </mat-dialog-actions>
  `,
  styles: [
    `
      .content {
        width: 460px;
        max-width: 80vw;
        min-height: 320px;
      }
      .full {
        width: 100%;
      }
      .groups {
        max-height: 380px;
        overflow-y: auto;
      }
      .group-label {
        font-size: 11px;
        text-transform: uppercase;
        letter-spacing: 0.04em;
        color: var(--text-secondary);
        padding: 10px 4px 4px;
      }
      .entry-row {
        width: 100%;
        display: flex;
        align-items: center;
        gap: 10px;
        border: none;
        background: none;
        font: inherit;
        text-align: left;
        padding: 6px 4px;
        cursor: pointer;
        border-radius: 4px;
        color: var(--text-primary);
      }
      .entry-row:hover {
        background: var(--hover-bg);
      }
      .entry-row .code {
        flex: 0 0 32px;
        color: var(--text-secondary);
        font-size: 12px;
      }
      .entry-row .name {
        flex: 1;
        font-size: 13px;
      }
      .entry-row .type {
        font-size: 11px;
        color: var(--text-secondary);
      }
      .empty {
        text-align: center;
        color: var(--text-secondary);
        padding: 24px;
      }
      .back {
        border: none;
        background: none;
        color: var(--primary-color);
        display: flex;
        align-items: center;
        gap: 4px;
        cursor: pointer;
        font: inherit;
        padding: 0 0 8px;
      }
      .selected-title {
        margin: 0 0 4px;
        font-size: 14px;
      }
      .selected-desc {
        margin: 0 0 4px;
        font-size: 13px;
        color: var(--text-primary);
      }
      .selected-rfc {
        margin: 0 0 16px;
        font-size: 11px;
      }
    `,
  ],
})
export class OptionPickerDialogComponent {
  private readonly catalogSvc = inject(OptionCatalogService);
  private readonly ref = inject(MatDialogRef<OptionPickerDialogComponent, OptionPickerResult | undefined>);

  readonly query = signal('');
  readonly selected = signal<OptionCatalogEntry | null>(null);
  readonly draftValue = signal('');
  readonly draftValid = signal(false);

  readonly groups = computed(() => {
    const q = this.query().trim().toLowerCase();
    const exclude = new Set(this.data.excludeCodes);
    const matches = this.catalogSvc
      .entries()
      .filter((e) => !exclude.has(e.option_code))
      .filter((e) => !q || e.name.toLowerCase().includes(q) || String(e.option_code).includes(q));

    const byCategory = new Map<string, OptionCatalogEntry[]>();
    for (const entry of matches) {
      const cat = categoryForCode(entry.option_code);
      const bucket = byCategory.get(cat) ?? [];
      bucket.push(entry);
      byCategory.set(cat, bucket);
    }
    return Array.from(byCategory.entries())
      .map(([name, entries]) => ({ name, entries: entries.sort((a, b) => a.option_code - b.option_code) }))
      .sort((a, b) => a.name.localeCompare(b.name));
  });

  constructor(@Inject(MAT_DIALOG_DATA) readonly data: OptionPickerDialogData) {
    this.catalogSvc.ensureLoaded();
  }

  select(entry: OptionCatalogEntry): void {
    this.selected.set(entry);
    this.draftValue.set('');
    this.draftValid.set(false);
  }

  save(): void {
    const entry = this.selected();
    if (!entry || !this.draftValid()) return;
    this.ref.close({ option_code: entry.option_code, value: this.draftValue() });
  }
}
