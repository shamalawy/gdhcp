import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';

/** One top-row dashboard metric: icon, label, headline value, and an
 * optional projected slot for a bar / status-dot row / secondary line
 * (e.g. the overall utilization bar, or "3/4 healthy" with a status dot).
 * Renders a skeleton value while `loading` so the row grid never jumps. */
@Component({
  selector: 'app-stat-card',
  standalone: true,
  imports: [MatIconModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="stat-card gdhcp-card">
      <div class="stat-icon" [class.stat-icon-alert]="alert()">
        <mat-icon>{{ icon() }}</mat-icon>
      </div>
      <div class="stat-body">
        <span class="stat-label">{{ label() }}</span>
        @if (loading()) {
          <span class="stat-value skeleton" aria-hidden="true">&nbsp;</span>
        } @else {
          <span class="stat-value">{{ value() }}</span>
        }
        <div class="stat-extra">
          <ng-content></ng-content>
        </div>
      </div>
    </div>
  `,
  styles: [
    `
      .stat-card {
        display: flex;
        align-items: flex-start;
        gap: 12px;
        min-width: 0;
      }

      .stat-icon {
        display: flex;
        align-items: center;
        justify-content: center;
        width: 36px;
        height: 36px;
        border-radius: 8px;
        background: var(--bg-highlight);
        color: var(--primary-color);
        flex: 0 0 auto;
      }

      .stat-icon-alert {
        background: color-mix(in srgb, var(--status-error) 16%, transparent);
        color: var(--status-error);
      }

      .stat-body {
        display: flex;
        flex-direction: column;
        gap: 2px;
        min-width: 0;
        flex: 1;
      }

      .stat-label {
        font-size: 12px;
        color: var(--text-secondary);
        white-space: nowrap;
        overflow: hidden;
        text-overflow: ellipsis;
      }

      .stat-value {
        font-size: 24px;
        font-weight: 600;
        line-height: 1.2;
        color: var(--text-primary);
        font-variant-numeric: tabular-nums;
      }

      .stat-value.skeleton {
        display: inline-block;
        width: 64px;
        border-radius: 4px;
        background: var(--util-track);
        animation: gdhcp-pulse 1.4s ease-in-out infinite;
      }

      @keyframes gdhcp-pulse {
        0%, 100% { opacity: 0.5; }
        50% { opacity: 0.9; }
      }

      .stat-extra {
        margin-top: 4px;
        min-height: 1px;
      }

      .stat-extra:empty {
        margin-top: 0;
      }
    `,
  ],
})
export class StatCardComponent {
  readonly icon = input('info');
  readonly label = input.required<string>();
  readonly value = input('—');
  readonly loading = input(false);
  /** Tints the icon chip red — used for "clusters healthy/total" when some
   * cluster is unhealthy, etc. */
  readonly alert = input(false);
}
