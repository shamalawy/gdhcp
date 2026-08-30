import { ChangeDetectionStrategy, Component, EventEmitter, Output, computed, input, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';

/** Inline-editable description textarea used on every entity detail header.
 * Click the pencil to enter edit mode; Save emits `save` with the new value
 * (or null if cleared), Cancel discards local edits. */
@Component({
  selector: 'app-description-field',
  standalone: true,
  imports: [FormsModule, MatButtonModule, MatIconModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (!editing()) {
      <div class="view-row">
        <p class="text" [class.empty]="!value()">{{ value() || placeholder() }}</p>
        @if (editable()) {
          <button mat-icon-button (click)="startEdit()" [attr.aria-label]="'Edit description'">
            <mat-icon>edit</mat-icon>
          </button>
        }
      </div>
    } @else {
      <div class="edit-row">
        <textarea
          class="editor"
          rows="3"
          [(ngModel)]="draft"
          [placeholder]="placeholder()"
        ></textarea>
        <div class="actions">
          <button mat-button (click)="cancel()">Cancel</button>
          <button mat-flat-button color="primary" (click)="save()">Save</button>
        </div>
      </div>
    }
  `,
  styles: [
    `
      /* Every consumer drops this component into a flex row beside a fixed
       * label. Without an explicit host box it stays inline, so the editor's
       * width:100% resolves against a zero-basis parent and the textarea
       * spills past the card border. */
      :host {
        display: block;
        flex: 1;
        min-width: 0;
      }

      .view-row {
        display: flex;
        align-items: flex-start;
        gap: 4px;
      }

      .text {
        margin: 0;
        color: var(--text-primary);
        white-space: pre-wrap;
        flex: 1;
        min-width: 0;
        /* Descriptions carry URLs and unspaced identifiers; without this a
         * single long token pushes the row wider than its container. */
        overflow-wrap: anywhere;
      }

      .text.empty {
        color: var(--text-secondary);
        font-style: italic;
      }

      .edit-row {
        display: flex;
        flex-direction: column;
        gap: 8px;
      }

      .editor {
        width: 100%;
        font: inherit;
        color: var(--text-primary);
        background: var(--bg-primary);
        border: 1px solid var(--border-color);
        border-radius: 4px;
        padding: 8px;
        resize: vertical;
        box-sizing: border-box;
      }

      .actions {
        display: flex;
        justify-content: flex-end;
        gap: 8px;
      }
    `,
  ],
})
export class DescriptionFieldComponent {
  readonly value = input<string | null>(null);
  readonly editable = input(true);
  readonly placeholder = input('No description');

  // Aliased to the public event name `save`; the class property is named
  // `saveEvent` to avoid colliding with the template's `save()` handler
  // method below.
  @Output('save') readonly saveEvent = new EventEmitter<string | null>();

  readonly editing = signal(false);
  draft = '';

  private readonly currentValue = computed(() => this.value() ?? '');

  startEdit(): void {
    this.draft = this.currentValue();
    this.editing.set(true);
  }

  cancel(): void {
    this.editing.set(false);
  }

  save(): void {
    const trimmed = this.draft.trim();
    this.saveEvent.emit(trimmed.length ? trimmed : null);
    this.editing.set(false);
  }
}
