import { ChangeDetectionStrategy, Component, computed, input, output } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';

import { LeaseReservation, LeaseReservationState } from '../../models';

/**
 * How one `LeaseReservationState` is presented: the chip in the Pinned
 * column, the pin button's icon, and the sentence that explains it.
 *
 * Both the DHCP leases table and the IPAM clients table render from this one
 * table, so the two cannot drift apart again — the whole point of amendment
 * A19. `label` is deliberately short (it sits in a table column); `explain`
 * is the tooltip and says what the DHCP engine will actually do, not what the
 * database row looks like.
 */
interface PinPresentation {
  label: string;
  /** Material icon name. `push_pin` reads as pinned, `push_pin` outlined as
   *  not; the filled/outlined distinction is carried by the CSS class. */
  icon: string;
  /** Modifier class on the chip and the icon. */
  tone: 'none' | 'pinned' | 'warn' | 'blocked';
  explain: string;
  /** What clicking the pin button does in this state. */
  action: string;
}

/** `ip` is interpolated into the states that need to name another address. */
function present(state: LeaseReservationState, otherIP: string): PinPresentation {
  switch (state) {
    case 'pinned':
      return {
        label: 'Pinned',
        icon: 'push_pin',
        tone: 'pinned',
        explain: 'Reserved for this client — it keeps this address across renewals and reboots.',
        action: 'Unpin: remove the reservation holding this address for this client',
      };
    case 'pinned_disabled':
      return {
        label: 'Pinned (off)',
        icon: 'push_pin',
        tone: 'warn',
        explain:
          'A reservation binds this client to this address but is disabled, so the DHCP engine ignores it: ' +
          'the address is neither held for this client nor kept from anyone else.',
        action: 'Remove the disabled reservation',
      };
    case 'pinned_elsewhere':
      return {
        label: 'Pinned to ' + (otherIP || 'another address'),
        icon: 'push_pin',
        tone: 'warn',
        explain:
          `This client is already pinned to ${otherIP || 'another address'}. A client can hold only one ` +
          'reservation per pool, so this one has to be moved rather than added to.',
        action: 'Move the pin to this address',
      };
    case 'address_reserved':
      return {
        label: 'Reserved',
        icon: 'lock',
        tone: 'blocked',
        explain:
          'This address is reserved for a different client. The client holding it now keeps it ' +
          'until its lease lapses, after which the reserved client can claim it.',
        action: 'Reserved for another client',
      };
    case 'not_pinned':
    default:
      return {
        label: '—',
        icon: 'push_pin',
        tone: 'none',
        explain: 'Not reserved — this client may be given a different address next time it asks.',
        action: 'Pin this address to the client as a reservation',
      };
  }
}

/**
 * The Pinned column's cell. Read-only; the action lives in
 * `app-pin-button` so a table can put the two in different columns.
 */
@Component({
  selector: 'app-pin-chip',
  standalone: true,
  imports: [MatIconModule, MatTooltipModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (view().tone === 'none') {
      <span class="pin-chip tone-none" [matTooltip]="view().explain">—</span>
    } @else {
      <span class="pin-chip" [class]="'tone-' + view().tone" [matTooltip]="view().explain">
        <mat-icon inline>{{ view().icon }}</mat-icon>
        {{ view().label }}
      </span>
    }
  `,
  styles: [
    `
      .pin-chip {
        display: inline-flex;
        align-items: center;
        gap: 4px;
        font-size: 11px;
        padding: 1px 8px;
        border-radius: 10px;
        white-space: nowrap;
        font-weight: 500;
      }

      .pin-chip mat-icon {
        font-size: 13px;
        width: 13px;
        height: 13px;
      }

      .tone-none {
        color: var(--text-secondary);
        padding: 0;
      }

      .tone-pinned {
        background: color-mix(in srgb, var(--primary-color) 18%, transparent);
        color: var(--primary-color);
      }

      .tone-warn {
        background: color-mix(in srgb, var(--status-warn) 18%, transparent);
        color: var(--status-warn);
      }

      .tone-blocked {
        background: var(--util-track);
        color: var(--text-secondary);
      }
    `,
  ],
})
export class PinChipComponent {
  readonly state = input.required<LeaseReservationState>();
  /** The reservation behind the state, so "Pinned to 10.0.0.5" can name it. */
  readonly reservation = input<LeaseReservation | null>(null);

  readonly view = computed(() => present(this.state(), this.reservation()?.reserved_ip ?? ''));
}

/**
 * The pin toggle. One button whose meaning follows the state: pin when
 * nothing holds the address, unpin when this client holds it, move when the
 * client is pinned somewhere else. It is disabled only when there is genuinely
 * nothing to do — an address reserved for somebody else.
 *
 * Before A19 this was a plain "create a reservation" button that looked
 * identical whether or not a reservation already existed, so the only way to
 * discover the answer was to click it and read a database constraint error.
 */
@Component({
  selector: 'app-pin-button',
  standalone: true,
  imports: [MatButtonModule, MatIconModule, MatTooltipModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <button
      mat-icon-button
      class="pin-button"
      [class]="'tone-' + view().tone"
      [disabled]="disabled() || view().tone === 'blocked'"
      [matTooltip]="view().action"
      matTooltipPosition="before"
      [attr.aria-label]="view().action + (label() ? ' (' + label() + ')' : '')"
      [attr.aria-pressed]="state() === 'pinned' || state() === 'pinned_disabled'"
      (click)="toggle.emit()"
    >
      <mat-icon>{{ view().icon }}</mat-icon>
    </button>
  `,
  styles: [
    `
      .tone-pinned {
        color: var(--primary-color);
      }

      .tone-warn {
        color: var(--status-warn);
      }

      /* An unpinned address gets an outlined pin, so a glance down the column
         separates held addresses from free ones without reading any text. */
      .tone-none mat-icon {
        opacity: 0.55;
        transform: rotate(45deg);
      }
    `,
  ],
})
export class PinButtonComponent {
  readonly state = input.required<LeaseReservationState>();
  readonly reservation = input<LeaseReservation | null>(null);
  readonly disabled = input(false);
  /** Included in the aria-label — normally the row's IP address. */
  readonly label = input('');

  readonly toggle = output<void>();

  readonly view = computed(() => present(this.state(), this.reservation()?.reserved_ip ?? ''));
}
