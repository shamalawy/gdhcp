import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';

/** Placeholder for empty lists/tables. Project a button via `<ng-content>`
 * for the primary action (e.g. "Create Pool"). */
@Component({
  selector: 'app-empty-state',
  standalone: true,
  imports: [MatIconModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="empty-state">
      <mat-icon class="icon">{{ icon() }}</mat-icon>
      <p class="title">{{ title() }}</p>
      @if (message()) {
        <p class="message">{{ message() }}</p>
      }
      <div class="action">
        <ng-content></ng-content>
      </div>
    </div>
  `,
  styles: [
    `
      .empty-state {
        display: flex;
        flex-direction: column;
        align-items: center;
        text-align: center;
        padding: 40px 16px;
        color: var(--text-secondary);
      }

      .icon {
        font-size: 40px;
        width: 40px;
        height: 40px;
        opacity: 0.5;
        margin-bottom: 8px;
      }

      .title {
        margin: 0;
        font-size: 15px;
        font-weight: 500;
        color: var(--text-primary);
      }

      .message {
        margin: 4px 0 0;
        font-size: 13px;
        max-width: 360px;
      }

      .action {
        margin-top: 16px;
      }
    `,
  ],
})
export class EmptyStateComponent {
  readonly icon = input('inbox');
  readonly title = input('Nothing here yet');
  readonly message = input<string | null>(null);
}
