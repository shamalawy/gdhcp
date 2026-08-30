import { DecimalPipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';

import { DHCPStats } from '../../shared/models';

interface FunnelRow {
  key: 'discover_count' | 'offer_count' | 'ack_count' | 'nak_count';
  label: string;
  colorVar: string;
}

const FUNNEL_ROWS: FunnelRow[] = [
  { key: 'discover_count', label: 'Discover', colorVar: 'var(--primary-color)' },
  { key: 'offer_count', label: 'Offer', colorVar: 'var(--primary-color)' },
  { key: 'ack_count', label: 'Ack', colorVar: 'var(--status-ok)' },
  { key: 'nak_count', label: 'Nak', colorVar: 'var(--status-error)' },
];

const BAR_TRACK = 100;

/** DHCP transaction counters for the dashboard, sourced from
 * `ApiService.dashboardDhcpStats()`. The four DORA-adjacent counters
 * (discover/offer/ack/nak) render as small SVG bars on one shared scale so
 * their relative volumes stay honest; ack is colored as a success state and
 * nak as a failure state (status color reserved for state, per design
 * system). Decline / no-leases (also failure states) and the remaining
 * request/release counters round out the panel as plain counts. */
@Component({
  selector: 'app-dhcp-activity',
  standalone: true,
  imports: [MatIconModule, DecimalPipe],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="activity-card gdhcp-card">
      <h2>DHCP activity</h2>

      @if (loading()) {
        <p class="hint">Loading…</p>
      } @else if (!stats()) {
        <p class="hint">No transaction data available.</p>
      } @else {
        <div class="funnel">
          @for (row of rows(); track row.key) {
            <div class="funnel-row">
              <span class="row-label" [style.color]="row.count > 0 && row.key === 'nak_count' ? 'var(--status-error)' : null">
                {{ row.label }}
              </span>
              <svg class="row-bar" viewBox="0 0 100 10" preserveAspectRatio="none">
                <rect class="track" x="0" y="1" width="100" height="8" rx="4"></rect>
                <rect class="fill" x="0" y="1" [attr.width]="row.width" height="8" rx="4" [style.fill]="row.colorVar"></rect>
              </svg>
              <span class="row-count" [class.error-count]="row.key === 'nak_count' && row.count > 0">
                {{ row.count | number }}
              </span>
            </div>
          }
        </div>

        <div class="issues" [class.has-issues]="declineCount() > 0 || noLeasesCount() > 0">
          @if (declineCount() > 0 || noLeasesCount() > 0) {
            <mat-icon class="issue-icon">warning</mat-icon>
          }
          <span [class.error-text]="declineCount() > 0">{{ declineCount() | number }} declined</span>
          <span class="sep">·</span>
          <span [class.error-text]="noLeasesCount() > 0">{{ noLeasesCount() | number }} pool exhausted (no leases)</span>
          <span class="sep">·</span>
          <span>{{ requestCount() | number }} requests</span>
          <span class="sep">·</span>
          <span>{{ releaseCount() | number }} released</span>
        </div>
      }
    </div>
  `,
  styles: [
    `
      .activity-card {
        display: flex;
        flex-direction: column;
        gap: 12px;
      }

      h2 {
        margin: 0;
        font-size: 15px;
        font-weight: 600;
      }

      .hint {
        margin: 0;
        color: var(--text-secondary);
        font-size: 13px;
      }

      .funnel {
        display: flex;
        flex-direction: column;
        gap: 8px;
      }

      .funnel-row {
        display: grid;
        grid-template-columns: 56px 1fr 56px;
        align-items: center;
        gap: 10px;
      }

      .row-label {
        font-size: 12px;
        color: var(--text-secondary);
      }

      .row-bar {
        width: 100%;
        height: 10px;
      }

      .track {
        fill: var(--util-track);
      }

      .row-count {
        text-align: right;
        font-size: 13px;
        font-variant-numeric: tabular-nums;
        color: var(--text-primary);
      }

      .error-count {
        color: var(--status-error);
        font-weight: 600;
      }

      .issues {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 6px;
        padding-top: 8px;
        border-top: 1px solid var(--divider-color);
        font-size: 12px;
        color: var(--text-secondary);
      }

      .issue-icon {
        font-size: 16px;
        width: 16px;
        height: 16px;
        color: var(--status-error);
      }

      .sep {
        opacity: 0.5;
      }

      .error-text {
        color: var(--status-error);
        font-weight: 600;
      }
    `,
  ],
})
export class DhcpActivityComponent {
  readonly stats = input<DHCPStats | null>(null);
  readonly loading = input(false);

  readonly declineCount = computed(() => this.stats()?.decline_count ?? 0);
  readonly noLeasesCount = computed(() => this.stats()?.no_leases_count ?? 0);
  readonly requestCount = computed(() => this.stats()?.request_count ?? 0);
  readonly releaseCount = computed(() => this.stats()?.release_count ?? 0);

  readonly rows = computed(() => {
    const stats = this.stats();
    if (!stats) return [];
    const max = Math.max(1, stats.discover_count, stats.offer_count, stats.ack_count, stats.nak_count);
    return FUNNEL_ROWS.map((row) => {
      const count = stats[row.key];
      const width = count > 0 ? Math.max(3, (count / max) * BAR_TRACK) : 0;
      return { ...row, count, width };
    });
  });
}
