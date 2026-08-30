import { ChangeDetectionStrategy, Component, input } from '@angular/core';
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
 * both the VRF detail page's "top-level prefixes" table and the prefix
 * detail page's "children" table (same columns per the task spec: prefix,
 * name, type, utilization, tags, description).
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
              <span class="gdhcp-muted">—</span>
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

        <tr mat-header-row *matHeaderRowDef="columns"></tr>
        <tr
          mat-row
          *matRowDef="let row; columns: columns"
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

  readonly columns = ['prefix', 'name', 'type', 'utilization', 'tags', 'description'];

  typeMeta(row: IPAMPrefix) {
    return PREFIX_TYPE_META[row.prefix_type];
  }

  tagsFor(prefixId: string): Tag[] {
    return this.tagsByPrefixId()[prefixId] ?? [];
  }
}
