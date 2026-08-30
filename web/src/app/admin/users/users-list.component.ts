import { ChangeDetectionStrategy, Component, OnInit, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { Subject, debounceTime, distinctUntilChanged } from 'rxjs';

import { ApiService } from '../../shared/api.service';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { User } from '../../shared/models';
import { UserDialogComponent } from './user-dialog.component';
import { extractErrorMessage } from '../http-error.util';

const PAGE_SIZE_OPTIONS = [10, 25, 50, 100];

/** `/admin/users` — user table with create/edit dialog and
 * activate/deactivate. The whole `/admin` area is already admin-gated by
 * `adminGuard`. */
@Component({
  selector: 'app-users-list',
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatPaginatorModule,
    MatTableModule,
    MatTooltipModule,
    PageHeaderComponent,
    EmptyStateComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="Users">
      <button mat-flat-button color="primary" (click)="openCreate()">
        <mat-icon>person_add</mat-icon>
        New user
      </button>
    </app-page-header>

    <mat-form-field appearance="outline" class="search-field" subscriptSizing="dynamic">
      <mat-icon matPrefix>search</mat-icon>
      <input matInput placeholder="Search users…" [(ngModel)]="searchText" (ngModelChange)="onSearchChange($event)" />
    </mat-form-field>

    @if (loading()) {
      <p class="hint">Loading users…</p>
    } @else if (users().length === 0) {
      <app-empty-state icon="group" title="No users found" [message]="searchText ? 'Try a different search.' : 'Create the first user account.'" />
    } @else {
      <div class="table-wrap gdhcp-card">
        <table mat-table [dataSource]="users()">
          <ng-container matColumnDef="username">
            <th mat-header-cell *matHeaderCellDef>Username</th>
            <td mat-cell *matCellDef="let u">
              {{ u.username }}
              @if (u.description) {
                <mat-icon class="desc-icon" [matTooltip]="u.description">notes</mat-icon>
              }
            </td>
          </ng-container>

          <ng-container matColumnDef="email">
            <th mat-header-cell *matHeaderCellDef>Email</th>
            <td mat-cell *matCellDef="let u">{{ u.email }}</td>
          </ng-container>

          <ng-container matColumnDef="role">
            <th mat-header-cell *matHeaderCellDef>Role</th>
            <td mat-cell *matCellDef="let u">
              <span class="role-badge" [class]="'role-' + u.role">{{ u.role }}</span>
            </td>
          </ng-container>

          <ng-container matColumnDef="status">
            <th mat-header-cell *matHeaderCellDef>Status</th>
            <td mat-cell *matCellDef="let u">
              <span class="status-badge" [class.status-active]="u.is_active">{{ u.is_active ? 'Active' : 'Inactive' }}</span>
            </td>
          </ng-container>

          <ng-container matColumnDef="actions">
            <th mat-header-cell *matHeaderCellDef></th>
            <td mat-cell *matCellDef="let u" class="actions-cell">
              <button mat-icon-button matTooltip="API tokens" [routerLink]="['/admin', 'users', u.id, 'tokens']">
                <mat-icon>vpn_key</mat-icon>
              </button>
              <button mat-icon-button matTooltip="Edit" (click)="openEdit(u)">
                <mat-icon>edit</mat-icon>
              </button>
              <button mat-icon-button [matTooltip]="u.is_active ? 'Deactivate' : 'Activate'" (click)="toggleActive(u)">
                <mat-icon>{{ u.is_active ? 'block' : 'check_circle' }}</mat-icon>
              </button>
              <button mat-icon-button matTooltip="Delete" (click)="remove(u)">
                <mat-icon>delete</mat-icon>
              </button>
            </td>
          </ng-container>

          <tr mat-header-row *matHeaderRowDef="columns"></tr>
          <tr mat-row *matRowDef="let row; columns: columns"></tr>
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

      .search-field {
        width: 320px;
        margin-bottom: 12px;
        display: block;
      }

      .table-wrap {
        padding: 0;
        overflow-x: auto;
      }

      table {
        width: 100%;
        background: transparent;
      }

      .desc-icon {
        font-size: 14px;
        width: 14px;
        height: 14px;
        color: var(--text-secondary);
        vertical-align: middle;
        margin-left: 4px;
        cursor: help;
      }

      .role-badge {
        font-size: 11px;
        font-weight: 600;
        padding: 2px 8px;
        border-radius: 4px;
        background: var(--bg-secondary);
        color: var(--text-secondary);
        text-transform: capitalize;
      }

      .role-admin {
        background: color-mix(in srgb, var(--primary-color) 16%, transparent);
        color: var(--primary-color);
      }

      .status-badge {
        font-size: 12px;
        color: var(--text-secondary);
      }

      .status-active {
        color: var(--status-ok);
        font-weight: 600;
      }

      .actions-cell {
        display: flex;
        justify-content: flex-end;
      }
    `,
  ],
})
export class UsersListComponent implements OnInit {
  private readonly api = inject(ApiService);
  private readonly dialog = inject(MatDialog);
  private readonly confirmDialog = inject(ConfirmDialogService);
  private readonly snackBar = inject(MatSnackBar);

  readonly columns = ['username', 'email', 'role', 'status', 'actions'];
  readonly pageSizeOptions = PAGE_SIZE_OPTIONS;

  readonly users = signal<User[]>([]);
  readonly total = signal(0);
  readonly loading = signal(true);
  readonly pageIndex = signal(0);
  readonly pageSize = signal(25);

  searchText = '';
  private readonly search$ = new Subject<string>();

  constructor() {
    this.search$.pipe(debounceTime(300), distinctUntilChanged(), takeUntilDestroyed()).subscribe(() => {
      this.pageIndex.set(0);
      this.load();
    });
  }

  ngOnInit(): void {
    this.load();
  }

  onSearchChange(value: string): void {
    this.search$.next(value);
  }

  onPage(event: PageEvent): void {
    this.pageIndex.set(event.pageIndex);
    this.pageSize.set(event.pageSize);
    this.load();
  }

  openCreate(): void {
    this.dialog
      .open(UserDialogComponent, { width: '480px', data: {} })
      .afterClosed()
      .subscribe((user: User | undefined) => {
        if (user) {
          this.snackBar.open(`User "${user.username}" created`, 'Dismiss', { duration: 3000 });
          this.load();
        }
      });
  }

  openEdit(user: User): void {
    this.dialog
      .open(UserDialogComponent, { width: '480px', data: { user } })
      .afterClosed()
      .subscribe((updated: User | undefined) => {
        if (updated) {
          this.snackBar.open('User updated', 'Dismiss', { duration: 3000 });
          this.load();
        }
      });
  }

  toggleActive(user: User): void {
    this.api.updateUser(user.id, { is_active: !user.is_active }).subscribe({
      next: () => {
        this.snackBar.open(user.is_active ? `"${user.username}" deactivated` : `"${user.username}" activated`, 'Dismiss', { duration: 3000 });
        this.load();
      },
      error: (err: unknown) => this.snackBar.open(extractErrorMessage(err), 'Dismiss', { duration: 4000 }),
    });
  }

  remove(user: User): void {
    this.confirmDialog
      .confirm({
        title: 'Delete user',
        message: `Delete the account "${user.username}"? This cannot be undone.`,
        confirmText: 'Delete',
        danger: true,
      })
      .subscribe((confirmed) => {
        if (!confirmed) return;
        this.api.deleteUser(user.id).subscribe({
          next: () => {
            this.snackBar.open(`User "${user.username}" deleted`, 'Dismiss', { duration: 3000 });
            this.load();
          },
          error: (err: unknown) => this.snackBar.open(extractErrorMessage(err), 'Dismiss', { duration: 4000 }),
        });
      });
  }

  private load(): void {
    this.loading.set(true);
    this.api
      .listUsers({
        limit: this.pageSize(),
        offset: this.pageIndex() * this.pageSize(),
        search: this.searchText.trim() || undefined,
      })
      .subscribe({
        next: (res) => {
          this.users.set(res.items);
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
