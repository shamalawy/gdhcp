import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { MatTooltipModule } from '@angular/material/tooltip';

import { AddressStatus } from '../../models';

/**
 * How one `AddressStatus` is presented. Every surface renders from this single
 * table -- the DHCP leases table, the IPAM clients table, the lease detail
 * page -- so the two sections cannot drift into describing one condition with
 * different words, which is exactly what happened while they each rendered the
 * raw `binding_state` string (amendment A20).
 *
 * `explain` says what the DHCP engine will actually do with the address, not
 * what the database row looks like. That distinction is the whole point: an
 * address whose lease row says "released" is, to the engine, simply free.
 */
interface StatusPresentation {
  label: string;
  /** Modifier class selecting the chip's colour. */
  tone: 'free' | 'assigned' | 'reserved' | 'abandoned';
  explain: string;
}

const STATUS_META: Record<AddressStatus, StatusPresentation> = {
  free: {
    label: 'Free',
    tone: 'free',
    explain: 'Nothing holds this address — it can be handed to the next client that asks.',
  },
  assigned: {
    label: 'Assigned',
    tone: 'assigned',
    explain: 'A client currently holds this address. It will not be offered to anyone else.',
  },
  reserved: {
    label: 'Reserved',
    tone: 'reserved',
    explain:
      'Held for a specific client that is not currently using it. It stays out of the dynamic ' +
      'range until that client asks for an address.',
  },
  abandoned: {
    label: 'Abandoned',
    tone: 'abandoned',
    explain:
      'Held back after a conflict — something else on the network answered on this address. ' +
      'It will not be reallocated until the lease reaper sweeps it.',
  },
};

/**
 * The Status column's cell: what this address is doing right now.
 *
 * This replaced a chip that rendered `binding_state` verbatim, which showed
 * "Released" and "Expired" as if they were distinct conditions when the
 * allocator treats both as free, and when the utilization panel on the very
 * same page was already counting them as free.
 */
@Component({
  selector: 'app-address-status-chip',
  standalone: true,
  imports: [MatTooltipModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <span class="status-chip" [class]="'tone-' + view().tone" [matTooltip]="view().explain">
      {{ view().label }}
    </span>
  `,
  styles: [
    `
      .status-chip {
        display: inline-block;
        font-size: 11px;
        padding: 1px 8px;
        border-radius: 10px;
        white-space: nowrap;
        font-weight: 500;
      }

      /* Free is deliberately the quietest: on a healthy pool most rows are
         free, and a wall of coloured chips reads as a wall of alarms. */
      .tone-free {
        background: var(--util-track);
        color: var(--text-secondary);
      }

      .tone-assigned {
        background: var(--util-ok);
        color: #fff;
      }

      .tone-reserved {
        background: color-mix(in srgb, var(--primary-color) 18%, transparent);
        color: var(--primary-color);
      }

      .tone-abandoned {
        background: var(--status-error);
        color: #fff;
      }
    `,
  ],
})
export class AddressStatusChipComponent {
  readonly status = input.required<AddressStatus>();

  readonly view = computed(() => STATUS_META[this.status()] ?? STATUS_META.free);
}
