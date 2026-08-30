import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';

import { OptionCatalogEntry } from '../../models';

/** Info icon showing a DHCP option's catalog description on hover. Every
 * option value editor (scope/pool/template options) renders one of these
 * next to each option row so operators never have to guess what an option
 * code does. */
@Component({
  selector: 'app-option-tooltip',
  standalone: true,
  imports: [MatIconModule, MatTooltipModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <mat-icon
      class="info-icon"
      [matTooltip]="tooltipText()"
      matTooltipPosition="above"
      [attr.aria-label]="tooltipText()"
      tabindex="0"
    >
      info
    </mat-icon>
  `,
  styles: [
    `
      .info-icon {
        font-size: 16px;
        width: 16px;
        height: 16px;
        color: var(--text-secondary);
        cursor: help;
        vertical-align: middle;
      }
    `,
  ],
})
export class OptionTooltipComponent {
  readonly entry = input<OptionCatalogEntry | null>(null);
  readonly optionCode = input<number | null>(null);

  readonly tooltipText = computed(() => {
    const entry = this.entry();
    if (!entry) {
      const code = this.optionCode();
      return code !== null ? `Option ${code}: no catalog description available.` : 'No description available.';
    }
    const ref = entry.iana_reference ? ` (${entry.iana_reference})` : '';
    return `${entry.name} [${entry.option_code}, ${entry.value_type}]${ref}\n${entry.description}`;
  });
}
