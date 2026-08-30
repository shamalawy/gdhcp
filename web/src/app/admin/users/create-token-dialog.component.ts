import { ChangeDetectionStrategy, Component, Inject, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatTooltipModule } from '@angular/material/tooltip';

import { ApiService } from '../../shared/api.service';
import { CreateAPITokenRequest, CreateAPITokenResponse } from '../../shared/models';
import { extractErrorMessage } from '../http-error.util';

export interface CreateTokenDialogData {
  userId: number;
}

/** Create-token dialog for `/admin/users/:id/tokens`. Per A14 the raw token
 * is returned exactly once, at creation — this dialog shows it inline with
 * a copy button and switches the form away so it can't be re-submitted;
 * closing (`Done`) tells the parent list to refresh, since the raw value is
 * gone for good once this dialog closes. */
@Component({
  selector: 'app-create-token-dialog',
  standalone: true,
  imports: [FormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatIconModule, MatInputModule, MatTooltipModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    @if (!created()) {
      <h2 mat-dialog-title>New API token</h2>
      <mat-dialog-content class="form">
        <mat-form-field appearance="outline">
          <mat-label>Description</mat-label>
          <input matInput [(ngModel)]="description" placeholder="e.g. CI pipeline, laptop CLI" />
        </mat-form-field>

        <mat-form-field appearance="outline">
          <mat-label>Expires</mat-label>
          <input matInput type="date" [(ngModel)]="expiresAt" />
          <mat-hint>Leave blank for a token that never expires</mat-hint>
        </mat-form-field>

        @if (error()) {
          <p class="error-note">{{ error() }}</p>
        }
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button mat-button [mat-dialog-close]="false" [disabled]="submitting()">Cancel</button>
        <button mat-flat-button color="primary" [disabled]="submitting()" (click)="submit()">
          {{ submitting() ? 'Creating…' : 'Create token' }}
        </button>
      </mat-dialog-actions>
    } @else {
      <h2 mat-dialog-title>Token created</h2>
      <mat-dialog-content class="form">
        <p class="warn-note">
          <mat-icon>warning</mat-icon>
          Copy this token now — it won't be shown again. Store it somewhere safe.
        </p>
        <div class="token-row">
          <code class="token-value gdhcp-mono">{{ created()!.token }}</code>
          <button mat-icon-button matTooltip="Copy" (click)="copy(created()!.token)">
            <mat-icon>content_copy</mat-icon>
          </button>
        </div>
      </mat-dialog-content>
      <mat-dialog-actions align="end">
        <button mat-flat-button color="primary" [mat-dialog-close]="true">Done</button>
      </mat-dialog-actions>
    }
  `,
  styles: [
    `
      .form {
        display: flex;
        flex-direction: column;
        gap: 4px;
        min-width: 380px;
      }

      .error-note {
        font-size: 12px;
        color: var(--status-error);
        margin: 4px 0 0;
      }

      .warn-note {
        display: flex;
        align-items: flex-start;
        gap: 8px;
        font-size: 12px;
        color: var(--status-warn);
        margin: 0 0 12px;
      }

      .warn-note mat-icon {
        font-size: 16px;
        width: 16px;
        height: 16px;
      }

      .token-row {
        display: flex;
        align-items: center;
        gap: 8px;
        background: var(--bg-secondary);
        border: 1px solid var(--border-color);
        border-radius: 6px;
        padding: 8px 12px;
      }

      .token-value {
        flex: 1;
        font-size: 12px;
        word-break: break-all;
        color: var(--text-primary);
      }
    `,
  ],
})
export class CreateTokenDialogComponent {
  private readonly api = inject(ApiService);
  private readonly snackBar = inject(MatSnackBar);
  private readonly userId: number;

  description = '';
  expiresAt = '';

  readonly submitting = signal(false);
  readonly error = signal<string | null>(null);
  readonly created = signal<CreateAPITokenResponse | null>(null);

  constructor(@Inject(MAT_DIALOG_DATA) data: CreateTokenDialogData) {
    this.userId = data.userId;
  }

  submit(): void {
    const body: CreateAPITokenRequest = {
      description: this.description.trim() || null,
      expires_at: this.expiresAt ? new Date(this.expiresAt).toISOString() : null,
    };

    this.submitting.set(true);
    this.error.set(null);
    this.api.createUserToken(this.userId, body).subscribe({
      next: (token) => {
        this.submitting.set(false);
        this.created.set(token);
      },
      error: (err: unknown) => {
        this.submitting.set(false);
        this.error.set(extractErrorMessage(err));
      },
    });
  }

  copy(token: string): void {
    navigator.clipboard
      .writeText(token)
      .then(() => this.snackBar.open('Token copied to clipboard', 'Dismiss', { duration: 2000 }))
      .catch(() => this.snackBar.open('Could not copy — select and copy manually', 'Dismiss', { duration: 3000 }));
  }
}
