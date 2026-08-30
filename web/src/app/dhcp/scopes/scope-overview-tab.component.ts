import { ChangeDetectionStrategy, Component, computed, effect, inject, input, signal } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterLink } from '@angular/router';

import { UtilizationBarComponent } from '../../shared/components/utilization-bar/utilization-bar.component';
import { ApiService } from '../../shared/api.service';
import { DHCPScope, DHCPStats, IPAMPrefix, UtilizationSummary } from '../../shared/models';
import { NotifyService } from '../shared/notify.service';

interface TxnBar {
  label: string;
  value: number;
  isError: boolean;
}

@Component({
  selector: 'app-scope-overview-tab',
  standalone: true,
  imports: [MatIconModule, MatTooltipModule, RouterLink, UtilizationBarComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="overview">
      <!-- Every scope is carved from an IPAM prefix (dhcp_scopes.ipam_prefix_id
           is a NOT NULL FK), but nothing in the DHCP section used to say so or
           link back, which made the two halves of the app feel unrelated. -->
      <section class="gdhcp-card">
        <h3>Address space</h3>
        <dl class="facts">
          <dt>Network</dt>
          <dd class="gdhcp-mono">{{ scope().network }}</dd>
          <dt>IPAM prefix</dt>
          <dd>
            <a [routerLink]="['/ipam', 'prefixes', scope().ipam_prefix_id]">
              {{ prefix()?.name || prefix()?.prefix || 'View in IPAM' }}
            </a>
          </dd>
          @if (prefix()?.vlan_id) {
            <dt>VLAN</dt>
            <dd>
              <a [routerLink]="['/ipam', 'vlans', prefix()!.vlan_id]">
                VLAN {{ prefix()!.vlan_vid }}@if (prefix()!.vlan_name) { — {{ prefix()!.vlan_name }} }
              </a>
            </dd>
          }
        </dl>
      </section>

      <section class="gdhcp-card">
        <h3>Utilization</h3>
        @if (utilization(); as u) {
          <app-utilization-bar [used]="u.active + u.reserved" [total]="u.total" />
          <p class="detail gdhcp-muted">{{ u.active }} active · {{ u.reserved }} reserved · {{ u.total - u.active - u.reserved }} available</p>
        } @else {
          <p class="gdhcp-muted">Loading…</p>
        }
      </section>

      <section class="gdhcp-card">
        <div class="stats-header">
          <h3>Transaction stats</h3>
          @if (hasErrors()) {
            <span class="error-flag" matTooltip="This scope has NAKs or exhausted-pool events in the current window">
              <mat-icon>warning</mat-icon> Errors detected
            </span>
          }
        </div>
        @if (stats(); as s) {
          <div class="bars">
            @for (bar of txnBars(); track bar.label) {
              <div class="bar-row">
                <span class="bar-label">{{ bar.label }}</span>
                <div class="bar-track">
                  <div class="bar-fill" [class.error]="bar.isError" [style.width.%]="barWidth(bar.value)"></div>
                </div>
                <span class="bar-value" [class.error]="bar.isError">{{ bar.value }}</span>
              </div>
            }
          </div>
        } @else {
          <p class="gdhcp-muted">Loading…</p>
        }
      </section>
    </div>
  `,
  styles: [
    `
      .facts {
        display: grid;
        grid-template-columns: 110px 1fr;
        gap: 6px 12px;
        margin: 0;
        font-size: 13px;
      }

      .facts dt {
        color: var(--text-secondary);
      }

      .facts dd {
        margin: 0;
        min-width: 0;
        overflow-wrap: anywhere;
      }
    `,
    `
      .overview {
        display: flex;
        flex-direction: column;
        gap: 16px;
        max-width: 640px;
      }
      h3 {
        margin: 0 0 12px;
        font-size: 14px;
      }
      .detail {
        font-size: 12px;
        margin: 8px 0 0;
      }
      .stats-header {
        display: flex;
        align-items: center;
        justify-content: space-between;
      }
      .error-flag {
        display: flex;
        align-items: center;
        gap: 4px;
        font-size: 12px;
        color: var(--status-error);
      }
      .error-flag mat-icon {
        font-size: 16px;
        width: 16px;
        height: 16px;
      }
      .bars {
        display: flex;
        flex-direction: column;
        gap: 8px;
      }
      .bar-row {
        display: grid;
        grid-template-columns: 90px 1fr 48px;
        align-items: center;
        gap: 8px;
      }
      .bar-label {
        font-size: 12px;
        color: var(--text-secondary);
      }
      .bar-track {
        height: 8px;
        border-radius: 4px;
        background: var(--util-track);
        overflow: hidden;
      }
      .bar-fill {
        height: 100%;
        background: var(--primary-color);
        border-radius: 4px;
      }
      .bar-fill.error {
        background: var(--status-error);
      }
      .bar-value {
        font-size: 12px;
        text-align: right;
        font-variant-numeric: tabular-nums;
      }
      .bar-value.error {
        color: var(--status-error);
        font-weight: 600;
      }
    `,
  ],
})
export class ScopeOverviewTabComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);

  readonly scope = input.required<DHCPScope>();

  readonly prefix = signal<IPAMPrefix | null>(null);
  readonly utilization = signal<UtilizationSummary | null>(null);
  readonly stats = signal<DHCPStats | null>(null);

  readonly hasErrors = computed(() => {
    const s = this.stats();
    return !!s && (s.nak_count > 0 || s.no_leases_count > 0);
  });

  readonly txnBars = computed<TxnBar[]>(() => {
    const s = this.stats();
    if (!s) return [];
    return [
      { label: 'Discover', value: s.discover_count, isError: false },
      { label: 'Offer', value: s.offer_count, isError: false },
      { label: 'Request', value: s.request_count, isError: false },
      { label: 'Ack', value: s.ack_count, isError: false },
      { label: 'Nak', value: s.nak_count, isError: s.nak_count > 0 },
      { label: 'Decline', value: s.decline_count, isError: s.decline_count > 0 },
      { label: 'Release', value: s.release_count, isError: false },
      { label: 'No leases', value: s.no_leases_count, isError: s.no_leases_count > 0 },
    ];
  });

  private readonly maxBarValue = computed(() => Math.max(1, ...this.txnBars().map((b) => b.value)));

  constructor() {
    effect(() => {
      const scope = this.scope();
      // Resolve the backing prefix so the link can show its name and VLAN
      // rather than a bare UUID.
      this.api.getPrefix(scope.ipam_prefix_id).subscribe({
        next: (p) => this.prefix.set(p),
        error: () => this.prefix.set(null),
      });
    });

    effect(() => {
      const scopeId = this.scope().id;
      this.api.scopeUtilization(scopeId).subscribe({
        next: (u) => this.utilization.set(u),
        error: (err) => this.notify.error(err, 'Could not load utilization.'),
      });
      this.api.scopeStats(scopeId).subscribe({
        next: (s) => this.stats.set(s),
        error: (err) => this.notify.error(err, 'Could not load transaction stats.'),
      });
    });
  }

  barWidth(value: number): number {
    return Math.min(100, (value / this.maxBarValue()) * 100);
  }
}
