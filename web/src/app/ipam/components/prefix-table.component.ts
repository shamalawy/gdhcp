import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { RouterLink } from '@angular/router';
import { MatIconModule } from '@angular/material/icon';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';

import { IPAMPrefix, Tag } from '../../shared/models';
import { TagChipsComponent } from '../../shared/components/tag-chips/tag-chips.component';
import { UtilizationBarComponent } from '../../shared/components/utilization-bar/utilization-bar.component';
import { PREFIX_TYPE_META } from '../util/prefix-type.util';

/**
 * Read-only, click-through table of IPAM prefixes — the shared shape behind
 * the VRF detail page's "top-level prefixes" table, the prefix detail
 * page's "children" table (same columns per the task spec: prefix, name,
 * type, utilization, tags, description) and the VLAN detail page's
 * "prefixes on this VLAN".
 *
 * The VLAN page adds a VRF column: unlike the other two callers, its rows
 * are not all from one VRF — a layer-2 segment can be numbered in more than
 * one routing instance — so "which VRF is this?" is a real question there
 * and noise everywhere else. Hence the optional `vrfNamesById` input rather
 * than a seventh column for everyone.
 */
@Component({
  selector: 'app-prefix-table',
  standalone: true,
  imports: [RouterLink, MatIconModule, MatTableModule, MatTooltipModule, TagChipsComponent, UtilizationBarComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="table-scroll">
      <table mat-table [dataSource]="items()" class="prefix-table">
        <ng-container matColumnDef="prefix">
          <th mat-header-cell *matHeaderCellDef>Prefix</th>
          <td mat-cell *matCellDef="let row">
            <a [routerLink]="['/ipam', 'prefixes', row.id]" class="prefix-link gdhcp-mono">{{ row.prefix }}</a>
          </td>
        </ng-container>

        <ng-container matColumnDef="name">
          <th mat-header-cell *matHeaderCellDef>Name</th>
          <td mat-cell *matCellDef="let row">{{ row.name || '—' }}</td>
        </ng-container>

        <ng-container matColumnDef="vrf">
          <th mat-header-cell *matHeaderCellDef>VRF</th>
          <td mat-cell *matCellDef="let row">
            <a [routerLink]="['/ipam', 'vrfs', row.vrf_id]" class="vrf-link">{{ vrfNameFor(row.vrf_id) }}</a>
          </td>
        </ng-container>

        <ng-container matColumnDef="type">
          <th mat-header-cell *matHeaderCellDef>Type</th>
          <td mat-cell *matCellDef="let row">
            <span class="type-chip" [class]="typeMeta(row).chipClass" [matTooltip]="typeMeta(row).help">
              {{ typeMeta(row).label }}
            </span>
          </td>
        </ng-container>

        <ng-container matColumnDef="utilization">
          <th mat-header-cell *matHeaderCellDef>Utilization</th>
          <td mat-cell *matCellDef="let row">
            @if (row.utilization_percentage !== null) {
              <app-utilization-bar
                [used]="row.utilization_percentage"
                [total]="100"
                [compact]="true"
              />
            } @else {
              <!-- Amendment A20: GET /prefixes does not return space_pct, so a
                   container with no DHCP beneath it has no number to show here
                   without a per-row request. The prefix's own page shows its
                   address-space breakdown. -->
              <span
                class="gdhcp-muted"
                matTooltip="No DHCP scope on or beneath this prefix. Open it to see its address-space breakdown."
                >—</span
              >
            }
          </td>
        </ng-container>

        <ng-container matColumnDef="tags">
          <th mat-header-cell *matHeaderCellDef>Tags</th>
          <td mat-cell *matCellDef="let row">
            <app-tag-chips [tags]="tagsFor(row.id)" [editable]="false" />
          </td>
        </ng-container>

        <ng-container matColumnDef="description">
          <th mat-header-cell *matHeaderCellDef>Description</th>
          <td mat-cell *matCellDef="let row">
            <span class="description-cell" [matTooltip]="row.description || ''">{{ row.description || '—' }}</span>
          </td>
        </ng-container>

        <tr mat-header-row *matHeaderRowDef="columns()"></tr>
        <tr
          mat-row
          *matRowDef="let row; columns: columns()"
          class="clickable-row"
          [routerLink]="['/ipam', 'prefixes', row.id]"
        ></tr>
      </table>
    </div>
  `,
  styles: [
    `
      .table-scroll {
        overflow-x: auto;
      }

      .prefix-table {
        width: 100%;
        background: transparent;
      }

      .prefix-link {
        text-decoration: none;
        font-weight: 500;
      }

      .prefix-link:hover {
        text-decoration: underline;
      }

      .vrf-link {
        text-decoration: none;
        white-space: nowrap;
      }

      .vrf-link:hover {
        text-decoration: underline;
      }

      .clickable-row {
        cursor: pointer;
      }

      .clickable-row:hover {
        background: var(--hover-bg);
      }

      .type-chip {
        display: inline-block;
        font-size: 11px;
        font-weight: 600;
        letter-spacing: 0.02em;
        text-transform: uppercase;
        padding: 2px 8px;
        border-radius: 10px;
        cursor: help;
      }

      .chip-aggregate {
        background: var(--bg-highlight);
        color: var(--primary-color);
      }

      .chip-container {
        background: color-mix(in srgb, var(--accent-color) 20%, transparent);
        color: var(--accent-color);
      }

      .chip-subnet {
        background: color-mix(in srgb, var(--status-ok) 20%, transparent);
        color: var(--status-ok);
      }

      .description-cell {
        display: inline-block;
        max-width: 260px;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        vertical-align: bottom;
      }
    `,
  ],
})
export class PrefixTableComponent {
  readonly items = input.required<IPAMPrefix[]>();
  /** Tags keyed by prefix id, loaded by the parent page (no bulk
   * "tags for many entities" endpoint exists — see EntityTagsService). */
  readonly tagsByPrefixId = input<Record<string, Tag[]>>({});
  /** Supply to add a VRF column; omit (the default) to leave it out. */
  readonly vrfNamesById = input<Record<string, string> | null>(null);

  readonly columns = computed(() =>
    this.vrfNamesById()
      ? ['prefix', 'name', 'vrf', 'type', 'utilization', 'tags', 'description']
      : ['prefix', 'name', 'type', 'utilization', 'tags', 'description'],
  );

  typeMeta(row: IPAMPrefix) {
    return PREFIX_TYPE_META[row.prefix_type];
  }

  tagsFor(prefixId: string): Tag[] {
    return this.tagsByPrefixId()[prefixId] ?? [];
  }

  vrfNameFor(vrfId: string): string {
    return this.vrfNamesById()?.[vrfId] ?? vrfId;
  }
}
