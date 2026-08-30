import { DatePipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, OnInit, inject, signal } from '@angular/core';
import { RouterLink } from '@angular/router';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatSelectModule } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTableModule } from '@angular/material/table';
import { catchError, of } from 'rxjs';

import { ApiService } from '../../shared/api.service';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { AuditAction, AuditLog, Paginated, User } from '../../shared/models';
import { extractErrorMessage } from '../http-error.util';

const ACTIONS: AuditAction[] = ['create', 'update', 'delete', 'lease_assign', 'lease_release', 'failover', 'login', 'logout'];
const ENTITY_TYPES = ['vrf', 'prefix', 'scope', 'pool', 'reservation', 'lease', 'option', 'template', 'tag', 'cluster', 'user'];

const ACTION_CLASS: Record<string, string> = {
  create: 'action-create',
  update: 'action-update',
  delete: 'action-delete',
  lease_assign: 'action-create',
  lease_release: 'action-neutral',
  failover: 'action-warn',
  login: 'action-neutral',
  logout: 'action-neutral',
};

const PAGE_SIZE_OPTIONS = [25, 50, 100];

/** Only entity types whose detail routes are guaranteed by the scaffold's
 * BINDING CONTRACT (app.routes.ts) or owned directly by this unit get a
 * click-through link; everything else renders as plain text rather than
 * risk a dead link to a route another unit hasn't necessarily built at the
 * path this guesses. See notes_for_integration. */
function entityLink(entityType: string, entityId: string): string[] | null {
  switch (entityType) {
    case 'prefix':
      return ['/ipam', 'prefixes', entityId];
    case 'scope':
      return ['/dhcp', 'scopes', entityId];
    case 'pool':
      return ['/dhcp', 'pools', entityId];
    case 'cluster':
      return ['/ha', 'clusters', entityId];
    case 'user':
      return ['/admin', 'users', entityId];
    default:
      return null;
  }
}

function formatValue(v: unknown): string {
  if (v === null || v === undefined) return '—';
  if (typeof v === 'string') return v;
  if (typeof v === 'number' || typeof v === 'boolean') return String(v);
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}

/** `/admin/audit` — paginated, filterable audit log with an expandable
 * JSON-diff row per entry. Read-only per endpoints.md. */
@Component({
  selector: 'app-audit-log',
  standalone: true,
  imports: [
    DatePipe,
    FormsModule,
    RouterLink,
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatPaginatorModule,
    MatSelectModule,
    MatTableModule,
    PageHeaderComponent,
    EmptyStateComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="Audit log" />

    <div class="filters">
      <mat-form-field appearance="outline" subscriptSizing="dynamic">
        <mat-label>Action</mat-label>
        <mat-select [(ngModel)]="actionFilter" (selectionChange)="onFilterChange()">
          <mat-option [value]="null">All actions</mat-option>
          @for (a of actions; track a) {
            <mat-option [value]="a">{{ a }}</mat-option>
          }
        </mat-select>
      </mat-form-field>

      <mat-form-field appearance="outline" subscriptSizing="dynamic">
        <mat-label>Entity type</mat-label>
        <mat-select [(ngModel)]="entityTypeFilter" (selectionChange)="onFilterChange()">
          <mat-option [value]="null">All entities</mat-option>
          @for (t of entityTypes; track t) {
            <mat-option [value]="t">{{ t }}</mat-option>
          }
        </mat-select>
      </mat-form-field>

      @if (actionFilter || entityTypeFilter) {
        <button mat-button (click)="clearFilters()">
          <mat-icon>filter_alt_off</mat-icon>
          Clear filters
        </button>
      }
    </div>

    @if (loading()) {
      <p class="hint">Loading audit log…</p>
    } @else if (entries().length === 0) {
      <app-empty-state icon="history" title="No matching entries" message="Try clearing filters, or check back after some activity." />
    } @else {
      <div class="table-wrap gdhcp-card">
        <table mat-table [dataSource]="entries()" multiTemplateDataRows>
          <ng-container matColumnDef="time">
            <th mat-header-cell *matHeaderCellDef>Time</th>
            <td mat-cell *matCellDef="let e">{{ e.timestamp | date: 'medium' }}</td>
          </ng-container>

          <ng-container matColumnDef="user">
            <th mat-header-cell *matHeaderCellDef>User</th>
            <td mat-cell *matCellDef="let e">{{ userName(e.user_id) }}</td>
          </ng-container>

          <ng-container matColumnDef="action">
            <th mat-header-cell *matHeaderCellDef>Action</th>
            <td mat-cell *matCellDef="let e">
              <span class="action-chip" [class]="actionClass(e.action)">{{ e.action }}</span>
            </td>
          </ng-container>

          <ng-container matColumnDef="entity">
            <th mat-header-cell *matHeaderCellDef>Entity</th>
            <td mat-cell *matCellDef="let e">
              <span class="entity-type">{{ e.entity_type }}</span>
              @if (link(e); as l) {
                <a [routerLink]="l" class="entity-name">{{ e.entity_name || e.entity_id }}</a>
              } @else {
                <span class="entity-name">{{ e.entity_name || e.entity_id }}</span>
              }
            </td>
          </ng-container>

          <ng-container matColumnDef="expand">
            <th mat-header-cell *matHeaderCellDef></th>
            <td mat-cell *matCellDef="let e">
              @if (e.changes) {
                <button mat-icon-button [attr.aria-label]="'Toggle changes for entry ' + e.id" (click)="toggle(e.id)">
                  <mat-icon>{{ expandedId() === e.id ? 'expand_less' : 'expand_more' }}</mat-icon>
                </button>
              }
            </td>
          </ng-container>

          <ng-container matColumnDef="detail">
            <td mat-cell *matCellDef="let e" [attr.colspan]="columns.length" class="detail-cell">
              <div class="detail-panel" [class.open]="expandedId() === e.id">
                @if (expandedId() === e.id && e.changes; as changes) {
                  <table class="diff-table">
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Old</th>
                        <th>New</th>
                      </tr>
                    </thead>
                    <tbody>
                      @for (field of objectKeys(changes); track field) {
                        <tr>
                          <td class="gdhcp-mono">{{ field }}</td>
                          <td class="diff-old">{{ formatVal(changes[field].old) }}</td>
                          <td class="diff-new">{{ formatVal(changes[field].new) }}</td>
                        </tr>
                      }
                    </tbody>
                  </table>
                }
              </div>
            </td>
          </ng-container>

          <tr mat-header-row *matHeaderRowDef="columns"></tr>
          <tr mat-row *matRowDef="let row; columns: columns"></tr>
          <tr mat-row *matRowDef="let row; columns: ['detail']" class="detail-row"></tr>
        </table>

        <mat-paginator
          [length]="total()"
          [pageSize]="pageSize()"
          [pageSizeOptions]="pageSizeOptions"
          [pageIndex]="pageIndex()"
          (page)="onPage($event)"
        ></mat-paginator>
      </div>
    }
  `,
  styles: [
    `
      .hint {
        color: var(--text-secondary);
        font-size: 13px;
      }

      .filters {
        display: flex;
        align-items: center;
        gap: 12px;
        margin-bottom: 12px;
      }

      .table-wrap {
        padding: 0;
        overflow-x: auto;
      }

      table {
        width: 100%;
        background: transparent;
      }

      .action-chip {
        display: inline-block;
        font-size: 11px;
        font-weight: 600;
        padding: 2px 8px;
        border-radius: 10px;
        text-transform: capitalize;
        background: var(--bg-secondary);
        color: var(--text-secondary);
      }

      .action-create {
        background: color-mix(in srgb, var(--status-ok) 16%, transparent);
        color: var(--status-ok);
      }

      .action-update {
        background: color-mix(in srgb, var(--primary-color) 16%, transparent);
        color: var(--primary-color);
      }

      .action-delete {
        background: color-mix(in srgb, var(--status-error) 16%, transparent);
        color: var(--status-error);
      }

      .action-warn {
        background: color-mix(in srgb, var(--status-warn) 16%, transparent);
        color: var(--status-warn);
      }

      .entity-type {
        font-size: 11px;
        color: var(--text-secondary);
        text-transform: uppercase;
        letter-spacing: 0.03em;
        margin-right: 6px;
      }

      .entity-name {
        color: var(--text-primary);
        text-decoration: none;
      }

      a.entity-name:hover {
        text-decoration: underline;
      }

      .detail-row {
        height: 0;
      }

      .detail-cell {
        border-bottom: none;
        padding: 0 !important;
      }

      .detail-panel {
        max-height: 0;
        overflow: hidden;
      }

      .detail-panel.open {
        max-height: none;
        padding: 8px 16px 16px;
      }

      .diff-table {
        width: 100%;
        border-collapse: collapse;
        font-size: 12px;
      }

      .diff-table th {
        text-align: left;
        color: var(--text-secondary);
        font-weight: 500;
        padding: 4px 8px;
        border-bottom: 1px solid var(--divider-color);
      }

      .diff-table td {
        padding: 4px 8px;
        border-bottom: 1px solid var(--divider-color);
        vertical-align: top;
        word-break: break-all;
      }

      .diff-old {
        color: var(--status-error);
      }

      .diff-new {
        color: var(--status-ok);
      }
    `,
  ],
})
export class AuditLogComponent implements OnInit {
  private readonly api = inject(ApiService);
  private readonly snackBar = inject(MatSnackBar);

  readonly columns = ['time', 'user', 'action', 'entity', 'expand'];
  readonly actions = ACTIONS;
  readonly entityTypes = ENTITY_TYPES;
  readonly pageSizeOptions = PAGE_SIZE_OPTIONS;

  readonly entries = signal<AuditLog[]>([]);
  readonly total = signal(0);
  readonly loading = signal(true);
  readonly pageIndex = signal(0);
  readonly pageSize = signal(25);
  readonly expandedId = signal<number | null>(null);

  actionFilter: AuditAction | null = null;
  entityTypeFilter: string | null = null;

  private readonly userNames = new Map<number, string>();

  ngOnInit(): void {
    this.api
      .listUsers({ limit: 200 })
      .pipe(catchError(() => of<Paginated<User>>({ items: [], total: 0, limit: 0, offset: 0 })))
      .subscribe((res) => {
        for (const u of res.items) this.userNames.set(u.id, u.username);
      });
    this.load();
  }

  onFilterChange(): void {
    this.pageIndex.set(0);
    this.load();
  }

  clearFilters(): void {
    this.actionFilter = null;
    this.entityTypeFilter = null;
    this.onFilterChange();
  }

  onPage(event: PageEvent): void {
    this.pageIndex.set(event.pageIndex);
    this.pageSize.set(event.pageSize);
    this.load();
  }

  toggle(id: number): void {
    this.expandedId.set(this.expandedId() === id ? null : id);
  }

  userName(userId: number | null): string {
    if (userId === null) return 'system';
    return this.userNames.get(userId) ?? `user #${userId}`;
  }

  actionClass(action: string): string {
    return ACTION_CLASS[action] ?? '';
  }

  link(entry: AuditLog): string[] | null {
    return entityLink(entry.entity_type, entry.entity_id);
  }

  objectKeys(obj: Record<string, unknown>): string[] {
    return Object.keys(obj);
  }

  formatVal(v: unknown): string {
    return formatValue(v);
  }

  private load(): void {
    this.loading.set(true);
    this.expandedId.set(null);
    this.api
      .auditLog({
        limit: this.pageSize(),
        offset: this.pageIndex() * this.pageSize(),
        filter: this.actionFilter ?? undefined,
        entity_type: this.entityTypeFilter ?? undefined,
      })
      .subscribe({
        next: (res) => {
          this.entries.set(res.items);
          this.total.set(res.total);
          this.loading.set(false);
        },
        error: (err: unknown) => {
          this.loading.set(false);
          this.snackBar.open(extractErrorMessage(err), 'Dismiss', { duration: 4000 });
        },
      });
  }
}
