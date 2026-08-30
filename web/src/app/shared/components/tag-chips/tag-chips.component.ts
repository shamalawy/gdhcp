import { ChangeDetectionStrategy, Component, EventEmitter, Output, input } from '@angular/core';
import { MatChipsModule } from '@angular/material/chips';
import { MatIconModule } from '@angular/material/icon';
import { MatFormFieldModule } from '@angular/material/form-field';

import { Tag } from '../../models';

const DEFAULT_CHIP_COLOR = '#9e9e9e';

/** View + edit tag chips for a single entity. In view mode, tags render as
 * read-only colored chips. In edit mode, an inline input lets the user
 * create a new tag by typing a name and pressing Enter, and each chip gets
 * a remove affordance. The component is presentation-only — it does not
 * call the API itself, keeping it reusable across entity types (`tagAdd`
 * emits the typed name, `tagRemove` emits the removed tag's id). */
@Component({
  selector: 'app-tag-chips',
  standalone: true,
  imports: [MatChipsModule, MatIconModule, MatFormFieldModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <mat-chip-set [attr.aria-label]="'Tags'">
      @for (tag of tags(); track tag.id) {
        <mat-chip [style.--mdc-chip-elevated-container-color]="tag.color || defaultColor" [highlighted]="true">
          {{ tag.name }}
          @if (editable()) {
            <button matChipRemove (click)="remove(tag)" [attr.aria-label]="'Remove ' + tag.name">
              <mat-icon>cancel</mat-icon>
            </button>
          }
        </mat-chip>
      }

      @if (editable()) {
        <mat-chip-grid #grid>
          <input
            class="new-tag-input"
            placeholder="Add tag…"
            [matChipInputFor]="grid"
            (keydown.enter)="onEnter($event)"
          />
        </mat-chip-grid>
      }

      @if (!editable() && tags().length === 0) {
        <span class="none">No tags</span>
      }
    </mat-chip-set>
  `,
  styles: [
    `
      mat-chip-set {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
      }

      .new-tag-input {
        border: none;
        outline: none;
        background: transparent;
        font: inherit;
        color: var(--text-primary);
        min-width: 96px;
      }

      .none {
        color: var(--text-secondary);
        font-size: 12px;
      }
    `,
  ],
})
export class TagChipsComponent {
  readonly tags = input.required<Tag[]>();
  readonly editable = input(false);

  readonly defaultColor = DEFAULT_CHIP_COLOR;

  @Output() readonly tagAdd = new EventEmitter<string>();
  @Output() readonly tagRemove = new EventEmitter<string>();

  remove(tag: Tag): void {
    this.tagRemove.emit(tag.id);
  }

  onEnter(event: Event): void {
    const input = event.target as HTMLInputElement;
    const name = input.value.trim();
    if (!name) return;
    this.tagAdd.emit(name);
    input.value = '';
  }
}
