import { ChangeDetectionStrategy, Component, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatTooltipModule } from '@angular/material/tooltip';

import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { ApiService } from '../../shared/api.service';
import { OptionCatalogService } from '../shared/option-catalog.service';

/** `/dhcp/options` — the option catalog browser: every IANA DHCPv4 option
 * (code, name, type, description, RFC), searchable, with a best-effort
 * "where used" count.
 *
 * That count comes from `GET /options` (docs: "List custom/standard
 * options") tallied by `option_code` — it's the only bulk listing of
 * configured `DHCPOption` rows the API exposes. Nothing in endpoints.md
 * guarantees that endpoint enumerates every configured value across every
 * scope/pool/template (as opposed to just custom-option definitions), so
 * treat the count as approximate; see notes_for_integration. */
@Component({
  selector: 'app-options-catalog',
  standalone: true,
  imports: [
    FormsModule,
    EmptyStateComponent,
    PageHeaderComponent,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatProgressBarModule,
    MatTooltipModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="DHCP Option Catalog" />

    <mat-form-field appearance="outline" subscriptSizing="dynamic" class="search">
      <mat-icon matPrefix>search</mat-icon>
      <input matInput placeholder="Search by code or name…" [ngModel]="query()" (ngModelChange)="query.set($event)" />
    </mat-form-field>

    @if (!catalogSvc.loaded()) {
      <mat-progress-bar mode="indeterminate" />
    }

    @if (catalogSvc.loaded() && filtered().length === 0) {
      <app-empty-state icon="search_off" title="No matching options" />
    } @else if (filtered().length > 0) {
      <div class="table-wrap">
        <table class="gdhcp-table">
          <thead>
            <tr>
              <th class="col-code">Code</th>
              <th>Name</th>
              <th>Type</th>
              <th>Description</th>
              <th>RFC</th>
              <th
                class="col-used"
                matTooltip="How many configured values (scope/pool/template) reference this code — approximate, see component source."
              >
                Used
              </th>
            </tr>
          </thead>
          <tbody>
            @for (entry of filtered(); track entry.option_code) {
              <tr>
                <td class="col-code gdhcp-mono">{{ entry.option_code }}</td>
                <td>{{ entry.name }}</td>
                <td class="gdhcp-mono">{{ entry.value_type }}</td>
                <td>{{ entry.description }}</td>
                <td class="gdhcp-muted">{{ entry.iana_reference || '—' }}</td>
                <td class="col-used">{{ usageCount(entry.option_code) }}</td>
              </tr>
            }
          </tbody>
        </table>
      </div>
    }
  `,
  styles: [
    `
      .search {
        width: 100%;
        max-width: 360px;
        margin-bottom: 16px;
      }
      .table-wrap {
        overflow-x: auto;
      }
      .gdhcp-table {
        width: 100%;
        border-collapse: collapse;
        font-size: 13px;
      }
      .gdhcp-table th {
        text-align: left;
        font-size: 11px;
        text-transform: uppercase;
        letter-spacing: 0.03em;
        color: var(--text-secondary);
        border-bottom: 1px solid var(--border-color);
        padding: 8px;
      }
      .gdhcp-table td {
        padding: 8px;
        border-bottom: 1px solid var(--divider-color);
        vertical-align: top;
      }
      .col-code {
        width: 48px;
      }
      .col-used {
        width: 56px;
        text-align: right;
      }
    `,
  ],
})
export class OptionsCatalogComponent {
  private readonly api = inject(ApiService);
  readonly catalogSvc = inject(OptionCatalogService);

  readonly query = signal('');
  private readonly usageByCode = signal<Map<number, number>>(new Map());

  readonly filtered = computed(() => {
    const q = this.query().trim().toLowerCase();
    const entries = this.catalogSvc.entries();
    if (!q) return entries;
    return entries.filter((e) => e.name.toLowerCase().includes(q) || String(e.option_code).includes(q));
  });

  constructor() {
    this.catalogSvc.ensureLoaded();
    this.api.listOptions({ limit: 1000 }).subscribe({
      next: (page) => {
        const counts = new Map<number, number>();
        for (const opt of page.items) {
          counts.set(opt.option_code, (counts.get(opt.option_code) ?? 0) + 1);
        }
        this.usageByCode.set(counts);
      },
      error: () => this.usageByCode.set(new Map()),
    });
  }

  usageCount(code: number): number {
    return this.usageByCode().get(code) ?? 0;
  }
}
