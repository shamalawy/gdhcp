import { ChangeDetectionStrategy, Component, Inject, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';

import { ApiService } from '../../shared/api.service';
import { CreateUserRequest, UpdateUserRequest, User, UserRole } from '../../shared/models';
import { extractErrorMessage } from '../http-error.util';

export interface UserDialogData {
  /** Omitted for create mode; present for edit mode. */
  user?: User;
}

const ROLE_HELP: { role: UserRole; text: string }[] = [
  { role: 'admin', text: 'Full access — including users, API tokens, and cluster (HA) configuration.' },
  { role: 'operator', text: 'Manages IPAM, DHCP, and templates day-to-day. Cannot manage users, tokens, or cluster config.' },
  { role: 'viewer', text: 'Read-only access across the whole application.' },
];

/** Create/edit user dialog. The role select is always shown with the full
 * admin/operator/viewer legend beneath it (per A15) so the choice is never a
 * guess. Password is only collected on create — there is no password-reset
 * endpoint in the API contract, so editing a user never touches it.
 * Active/inactive is edit-only (a brand new user is active by definition). */
@Component({
  selector: 'app-user-dialog',
  standalone: true,
  imports: [
    FormsModule,
    MatButtonModule,
    MatDialogModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatSlideToggleModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>{{ isEdit ? 'Edit user' : 'New user' }}</h2>
    <mat-dialog-content class="form">
      <mat-form-field appearance="outline">
        <mat-label>Username</mat-label>
        <input matInput [(ngModel)]="username" required maxlength="64" />
      </mat-form-field>

      <mat-form-field appearance="outline">
        <mat-label>Email</mat-label>
        <input matInput type="email" [(ngModel)]="email" required />
      </mat-form-field>

      @if (!isEdit) {
        <mat-form-field appearance="outline">
          <mat-label>Password</mat-label>
          <input matInput type="password" [(ngModel)]="password" required minlength="8" autocomplete="new-password" />
        </mat-form-field>
      }

      <mat-form-field appearance="outline">
        <mat-label>Description</mat-label>
        <textarea matInput rows="2" [(ngModel)]="description" placeholder="Optional"></textarea>
      </mat-form-field>

      <mat-form-field appearance="outline">
        <mat-label>Role</mat-label>
        <mat-select [(ngModel)]="role">
          <mat-option value="admin">admin</mat-option>
          <mat-option value="operator">operator</mat-option>
          <mat-option value="viewer">viewer</mat-option>
        </mat-select>
      </mat-form-field>

      <ul class="role-legend">
        @for (r of roleHelp; track r.role) {
          <li [class.active]="role === r.role">
            <strong>{{ r.role }}</strong> — {{ r.text }}
          </li>
        }
      </ul>

      @if (isEdit) {
        <mat-slide-toggle [(ngModel)]="isActive">Account active</mat-slide-toggle>
      }

      @if (error()) {
        <p class="error-note">{{ error() }}</p>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="undefined" [disabled]="submitting()">Cancel</button>
      <button mat-flat-button color="primary" [disabled]="!valid() || submitting()" (click)="submit()">
        {{ submitting() ? 'Saving…' : isEdit ? 'Save changes' : 'Create user' }}
      </button>
    </mat-dialog-actions>
  `,
  styles: [
    `
      .form {
        display: flex;
        flex-direction: column;
        gap: 4px;
        min-width: 420px;
      }

      .role-legend {
        list-style: none;
        margin: 0 0 8px;
        padding: 8px 10px;
        background: var(--bg-secondary);
        border-radius: 6px;
        display: flex;
        flex-direction: column;
        gap: 4px;
        font-size: 12px;
        color: var(--text-secondary);
      }

      .role-legend li.active {
        color: var(--text-primary);
      }

      .role-legend strong {
        color: inherit;
      }

      .error-note {
        font-size: 12px;
        color: var(--status-error);
        margin: 4px 0 0;
      }
    `,
  ],
})
export class UserDialogComponent {
  private readonly api = inject(ApiService);
  private readonly dialogRef = inject(MatDialogRef<UserDialogComponent, User | undefined>);

  readonly isEdit: boolean;
  readonly roleHelp = ROLE_HELP;

  username = '';
  email = '';
  password = '';
  description = '';
  role: UserRole = 'viewer';
  isActive = true;

  readonly submitting = signal(false);
  readonly error = signal<string | null>(null);

  private readonly userId: number | null;

  constructor(@Inject(MAT_DIALOG_DATA) data: UserDialogData) {
    this.isEdit = !!data.user;
    this.userId = data.user?.id ?? null;
    if (data.user) {
      this.username = data.user.username;
      this.email = data.user.email;
      this.description = data.user.description ?? '';
      this.role = data.user.role;
      this.isActive = data.user.is_active;
    }
  }

  valid(): boolean {
    if (!this.username.trim() || !this.email.trim()) return false;
    if (!this.isEdit && this.password.length < 8) return false;
    return true;
  }

  submit(): void {
    if (!this.valid()) return;
    this.submitting.set(true);
    this.error.set(null);

    if (this.isEdit && this.userId !== null) {
      const body: UpdateUserRequest = {
        username: this.username.trim(),
        email: this.email.trim(),
        description: this.description.trim() || null,
        role: this.role,
        is_active: this.isActive,
      };
      this.api.updateUser(this.userId, body).subscribe({
        next: (user) => {
          this.submitting.set(false);
          this.dialogRef.close(user);
        },
        error: (err: unknown) => {
          this.submitting.set(false);
          this.error.set(extractErrorMessage(err));
        },
      });
    } else {
      const body: CreateUserRequest = {
        username: this.username.trim(),
        email: this.email.trim(),
        password: this.password,
        description: this.description.trim() || null,
        role: this.role,
      };
      this.api.createUser(body).subscribe({
        next: (user) => {
          this.submitting.set(false);
          this.dialogRef.close(user);
        },
        error: (err: unknown) => {
          this.submitting.set(false);
          this.error.set(extractErrorMessage(err));
        },
      });
    }
  }
}
