import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { RouterLink } from '@angular/router';
import { MatIconModule } from '@angular/material/icon';

export interface Breadcrumb {
  label: string;
  link?: string;
}

/** Standard detail/list page heading: title, optional breadcrumb trail, and
 * a projected actions slot (buttons, menus) right-aligned. Used at the top
 * of every feature page. */
@Component({
  selector: 'app-page-header',
  standalone: true,
  imports: [RouterLink, MatIconModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <header class="page-header">
      @if (breadcrumbs().length) {
        <nav class="breadcrumbs" aria-label="Breadcrumb">
          @for (crumb of breadcrumbs(); track $index) {
            @if (crumb.link && !$last) {
              <a [routerLink]="crumb.link">{{ crumb.label }}</a>
            } @else {
              <span class="current">{{ crumb.label }}</span>
            }
            @if (!$last) {
              <mat-icon class="sep">chevron_right</mat-icon>
            }
          }
        </nav>
      }

      <div class="title-row">
        <h1>{{ title() }}</h1>
        <div class="actions">
          <ng-content></ng-content>
        </div>
      </div>
    </header>
  `,
  styles: [
    `
      .page-header {
        display: flex;
        flex-direction: column;
        gap: 4px;
        margin-bottom: 16px;
      }

      .breadcrumbs {
        display: flex;
        align-items: center;
        font-size: 12px;
        color: var(--text-secondary);
      }

      .breadcrumbs a {
        color: var(--text-secondary);
        text-decoration: none;
      }

      .breadcrumbs a:hover {
        text-decoration: underline;
      }

      .breadcrumbs .current {
        color: var(--text-primary);
      }

      .sep {
        font-size: 14px;
        width: 14px;
        height: 14px;
        margin: 0 2px;
      }

      .title-row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 16px;
      }

      h1 {
        margin: 0;
        font-size: 20px;
        font-weight: 600;
      }

      .actions {
        display: flex;
        align-items: center;
        gap: 8px;
      }
    `,
  ],
})
export class PageHeaderComponent {
  readonly title = input.required<string>();
  readonly breadcrumbs = input<Breadcrumb[]>([]);
}
