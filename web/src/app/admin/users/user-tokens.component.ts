import { DatePipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, OnInit, inject, signal } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { catchError, of } from 'rxjs';

import { ApiService } from '../../shared/api.service';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';
import { ConfirmDialogService } from '../../shared/components/confirm-dialog/confirm-dialog.component';
import { APIToken, Paginated, User } from '../../shared/models';
import { CreateTokenDialogComponent } from './create-token-dialog.component';
import { extractErrorMessage } from '../http-error.util';

/** `/admin/users/:id/tokens` — API token list + create/revoke for one user.
 * The raw token value is never available here (only at creation, per A14) —
 * see create-token-dialog.component.ts. */
@Component({
  selector: 'app-user-tokens',
  standalone: true,
  imports: [DatePipe, MatButtonModule, MatDialogModule, MatIconModule, MatTableModule, MatTooltipModule, PageHeaderComponent, EmptyStateComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header
      [title]="user() ? user()!.username + ' — API tokens' : 'API tokens'"
      [breadcrumbs]="[{ label: 'Users', link: '/admin/users' }, { label: user()?.username ?? '…' }]"
    >
      <button mat-flat-button color="primary" (click)="openCreate()">
        <mat-icon>add</mat-icon>
        New token
      </button>
    </app-page-header>

    @if (loading()) {
      <p class="hint">Loading tokens…</p>
    } @else if (tokens().length === 0) {
      <app-empty-state icon="vpn_key" title="No tokens yet" message="Create a token to authenticate API/CLI access as this user." />
    } @else {
      <div class="table-wrap gdhcp-card">
        <table mat-table [dataSource]="tokens()">
          <ng-container matColumnDef="description">
            <th mat-header-cell *matHeaderCellDef>Description</th>
            <td mat-cell *matCellDef="let t">{{ t.description || '—' }}</td>
          </ng-container>

          <ng-container matColumnDef="created">
            <th mat-header-cell *matHeaderCellDef>Created</th>
            <td mat-cell *matCellDef="let t">{{ t.created_at | date: 'medium' }}</td>
          </ng-container>

          <ng-container matColumnDef="lastUsed">
            <th mat-header-cell *matHeaderCellDef>Last used</th>
            <td mat-cell *matCellDef="let t">{{ t.last_used_at ? (t.last_used_at | date: 'medium') : 'Never' }}</td>
          </ng-container>

          <ng-container matColumnDef="expires">
            <th mat-header-cell *matHeaderCellDef>Expires</th>
            <td mat-cell *matCellDef="let t">{{ t.expires_at ? (t.expires_at | date: 'medium') : 'Never' }}</td>
          </ng-container>

          <ng-container matColumnDef="actions">
            <th mat-header-cell *matHeaderCellDef></th>
            <td mat-cell *matCellDef="let t" class="actions-cell">
              <button mat-icon-button matTooltip="Revoke token" (click)="revoke(t)">
                <mat-icon>delete</mat-icon>
              </button>
            </td>
          </ng-container>

          <tr mat-header-row *matHeaderRowDef="columns"></tr>
          <tr mat-row *matRowDef="let row; columns: columns"></tr>
        </table>
      </div>
    }
  `,
  styles: [
    `
      .hint {
        color: var(--text-secondary);
        font-size: 13px;
      }

      .table-wrap {
        padding: 0;
        overflow-x: auto;
      }

      table {
        width: 100%;
        background: transparent;
      }

      .actions-cell {
        display: flex;
        justify-content: flex-end;
      }
    `,
  ],
})
export class UserTokensComponent implements OnInit {
  private readonly api = inject(ApiService);
  private readonly route = inject(ActivatedRoute);
  private readonly dialog = inject(MatDialog);
  private readonly confirmDialog = inject(ConfirmDialogService);
  private readonly snackBar = inject(MatSnackBar);

  readonly columns = ['description', 'created', 'lastUsed', 'expires', 'actions'];
  readonly user = signal<User | null>(null);
  readonly tokens = signal<APIToken[]>([]);
  readonly loading = signal(true);

  private userId = 0;

  ngOnInit(): void {
    this.userId = Number(this.route.snapshot.paramMap.get('id'));
    this.api
      .getUser(this.userId)
      .pipe(catchError(() => of(null)))
      .subscribe((user) => this.user.set(user));
    this.load();
  }

  openCreate(): void {
    this.dialog
      .open(CreateTokenDialogComponent, { width: '460px', data: { userId: this.userId } })
      .afterClosed()
      .subscribe((created: boolean | undefined) => {
        if (created) this.load();
      });
  }

  revoke(token: APIToken): void {
    this.confirmDialog
      .confirm({
        title: 'Revoke token',
        message: `Revoke the token "${token.description || 'untitled'}"? Anything using it will immediately lose access.`,
        confirmText: 'Revoke',
        danger: true,
      })
      .subscribe((confirmed) => {
        if (!confirmed) return;
        this.api.revokeUserToken(this.userId, token.id).subscribe({
          next: () => {
            this.snackBar.open('Token revoked', 'Dismiss', { duration: 3000 });
            this.load();
          },
          error: (err: unknown) => this.snackBar.open(extractErrorMessage(err), 'Dismiss', { duration: 4000 }),
        });
      });
  }

  private load(): void {
    this.loading.set(true);
    this.api
      .listUserTokens(this.userId, { limit: 100 })
      .pipe(catchError(() => of<Paginated<APIToken>>({ items: [], total: 0, limit: 0, offset: 0 })))
      .subscribe((res) => {
        this.tokens.set(res.items);
        this.loading.set(false);
      });
  }
}
