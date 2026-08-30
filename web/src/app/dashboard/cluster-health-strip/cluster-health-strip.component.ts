import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { RouterLink } from '@angular/router';

import { ClusterRole, ClusterState, EntityStatus } from '../../shared/models';
import { StatusDotComponent } from '../../shared/components/status-dot/status-dot.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';

export interface ClusterHealthStripEntry {
  id: string;
  name: string;
  vip_address: string | null;
  state: ClusterState;
  primary_node: string | null;
  servers: { id: string; node_name: string; role: ClusterRole; is_healthy: boolean }[];
}

function statusFor(state: ClusterState): EntityStatus {
  switch (state) {
    case 'HEALTHY':
      return 'ok';
    case 'DEGRADED':
      return 'warn';
    case 'DOWN':
      return 'error';
    default:
      return 'unknown';
  }
}

/** Cluster health strip on the dashboard: one compact card per cluster with
 * its state, primary node, VIP, and a dot per server. Click-through to the
 * cluster's HA detail page. Fed by `ApiService.clusterHealth()` per cluster
 * (there is no bulk "all clusters' health" endpoint) — see
 * notes_for_integration. */
@Component({
  selector: 'app-cluster-health-strip',
  standalone: true,
  imports: [RouterLink, StatusDotComponent, EmptyStateComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="strip-card gdhcp-card">
      <h2>Cluster health</h2>

      @if (loading()) {
        <p class="hint">Loading…</p>
      } @else if (clusters().length === 0) {
        <app-empty-state icon="hub" title="No clusters yet" message="Create a cluster from the HA area to enable HA DHCP serving." />
      } @else {
        <div class="strip">
          @for (cluster of clusters(); track cluster.id) {
            <a class="cluster-tile" [routerLink]="['/ha', 'clusters', cluster.id]">
              <div class="tile-header">
                <app-status-dot [status]="statusOf(cluster.state)" [tooltip]="cluster.state" />
                <span class="cluster-name">{{ cluster.name }}</span>
              </div>
              <div class="tile-row">
                <span class="tile-label">Primary</span>
                <span class="tile-value">{{ cluster.primary_node ?? '—' }}</span>
              </div>
              <div class="tile-row">
                <span class="tile-label">VIP</span>
                <span class="tile-value gdhcp-mono">{{ cluster.vip_address ?? '—' }}</span>
              </div>
              <div class="server-dots">
                @for (server of cluster.servers; track server.id) {
                  <app-status-dot
                    [status]="server.is_healthy ? 'ok' : 'error'"
                    [tooltip]="server.node_name + ' (' + server.role + ')'"
                  />
                }
              </div>
            </a>
          }
        </div>
      }
    </div>
  `,
  styles: [
    `
      .strip-card {
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

      .strip {
        display: flex;
        gap: 12px;
        overflow-x: auto;
        padding-bottom: 2px;
      }

      .cluster-tile {
        flex: 0 0 auto;
        min-width: 200px;
        display: flex;
        flex-direction: column;
        gap: 6px;
        padding: 12px;
        border: 1px solid var(--border-color);
        border-radius: 8px;
        text-decoration: none;
        color: inherit;
      }

      .cluster-tile:hover {
        background: var(--hover-bg);
      }

      .tile-header {
        display: flex;
        align-items: center;
        gap: 8px;
      }

      .cluster-name {
        font-size: 13px;
        font-weight: 600;
        color: var(--text-primary);
      }

      .tile-row {
        display: flex;
        justify-content: space-between;
        gap: 8px;
        font-size: 11px;
      }

      .tile-label {
        color: var(--text-secondary);
      }

      .tile-value {
        color: var(--text-primary);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }

      .server-dots {
        display: flex;
        gap: 4px;
        margin-top: 2px;
      }
    `,
  ],
})
export class ClusterHealthStripComponent {
  readonly clusters = input<ClusterHealthStripEntry[]>([]);
  readonly loading = input(false);

  statusOf(state: ClusterState): EntityStatus {
    return statusFor(state);
  }
}
