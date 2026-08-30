import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { RouterLink } from '@angular/router';

import { UtilizationBarComponent } from '../../shared/components/utilization-bar/utilization-bar.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';

export interface TopScopeEntry {
  id: string;
  name: string;
  network: string;
  used: number;
  total: number;
}

/** "Top scopes by utilization" list on the dashboard — each row is a
 * click-through to the scope's detail page in the DHCP area. The parent
 * computes and sorts the entries (there's no dedicated backend endpoint for
 * this; see notes_for_integration). */
@Component({
  selector: 'app-top-scopes',
  standalone: true,
  imports: [RouterLink, UtilizationBarComponent, EmptyStateComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="top-scopes gdhcp-card">
      <h2>Top scopes by utilization</h2>

      @if (loading()) {
        <p class="hint">Loading…</p>
      } @else if (scopes().length === 0) {
        <app-empty-state icon="dns" title="No scopes yet" message="Scopes will appear here once created." />
      } @else {
        <ul class="scope-list">
          @for (scope of scopes(); track scope.id) {
            <li>
              <a class="scope-row" [routerLink]="['/dhcp', 'scopes', scope.id]">
                <div class="scope-meta">
                  <span class="scope-name">{{ scope.name }}</span>
                  <span class="scope-network gdhcp-mono">{{ scope.network }}</span>
                </div>
                <app-utilization-bar [used]="scope.used" [total]="scope.total" />
              </a>
            </li>
          }
        </ul>
      }
    </div>
  `,
  styles: [
    `
      .top-scopes {
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

      .scope-list {
        list-style: none;
        margin: 0;
        padding: 0;
        display: flex;
        flex-direction: column;
        gap: 4px;
      }

      .scope-row {
        display: flex;
        align-items: center;
        gap: 16px;
        padding: 8px;
        border-radius: 6px;
        text-decoration: none;
        color: inherit;
      }

      .scope-row:hover {
        background: var(--hover-bg);
      }

      .scope-meta {
        display: flex;
        flex-direction: column;
        min-width: 140px;
        flex: 0 0 auto;
      }

      .scope-name {
        font-size: 13px;
        color: var(--text-primary);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }

      .scope-network {
        font-size: 11px;
        color: var(--text-secondary);
      }
    `,
  ],
})
export class TopScopesComponent {
  readonly scopes = input<TopScopeEntry[]>([]);
  readonly loading = input(false);
}
