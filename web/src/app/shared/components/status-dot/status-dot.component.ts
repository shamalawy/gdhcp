import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { MatTooltipModule } from '@angular/material/tooltip';

import { EntityStatus } from '../../models';

@Component({
  selector: 'app-status-dot',
  standalone: true,
  imports: [MatTooltipModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <span
      class="status-dot"
      [class]="status()"
      [matTooltip]="tooltip() ?? defaultTooltip()"
      [attr.aria-label]="tooltip() ?? defaultTooltip()"
    ></span>
  `,
  styles: [
    `
      .status-dot {
        display: inline-block;
        width: 8px;
        height: 8px;
        border-radius: 50%;
        flex-shrink: 0;
      }

      .status-dot.ok { background: var(--status-ok); }
      .status-dot.warn { background: var(--status-warn); }
      .status-dot.error { background: var(--status-error); }
      .status-dot.unknown { background: var(--status-unknown); }
    `,
  ],
})
export class StatusDotComponent {
  readonly status = input<EntityStatus>('unknown');
  readonly tooltip = input<string | null>(null);

  defaultTooltip(): string {
    switch (this.status()) {
      case 'ok':
        return 'Healthy';
      case 'warn':
        return 'Approaching limits';
      case 'error':
        return 'Critical';
      default:
        return 'Unknown';
    }
  }
}
