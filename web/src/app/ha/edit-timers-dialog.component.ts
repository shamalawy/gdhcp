import { ChangeDetectionStrategy, Component, Inject, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatTooltipModule } from '@angular/material/tooltip';

import { ApiService } from '../shared/api.service';
import { Cluster, UpdateClusterRequest } from '../shared/models';
import { extractErrorMessage } from './http-error.util';

export interface EditTimersDialogData {
  cluster: Cluster;
}

/** Edit-cluster dialog: name, description, and the three HA timers (the VIP
 * address/interface are immutable after creation — `UpdateClusterRequest`
 * has no VIP fields — so this dialog is scoped to what the PUT endpoint
 * actually accepts). Same threshold help text as the create dialog. */
@Component({
  selector: 'app-edit-timers-dialog',
  standalone: true,
  imports: [FormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatIconModule, MatInputModule, MatTooltipModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>Edit cluster</h2>
    <mat-dialog-content class="form">
      <mat-form-field appearance="outline">
        <mat-label>Name</mat-label>
        <input matInput [(ngModel)]="name" required maxlength="255" />
      </mat-form-field>

      <mat-form-field appearance="outline">
        <mat-label>Description</mat-label>
        <textarea matInput rows="2" [(ngModel)]="description" placeholder="Optional"></textarea>
      </mat-form-field>

      <h3 class="section-title">
        Timers
        <mat-icon
          class="help-icon"
          matTooltip="heartbeat_ttl should be at least 2x heartbeat_interval to avoid spurious failovers; election_interval should be at most heartbeat_ttl / 2 for prompt detection."
          tabindex="0"
        >help_outline</mat-icon>
      </h3>

      <div class="field-row">
        <mat-form-field appearance="outline">
          <mat-label>Heartbeat interval (ms)</mat-label>
          <input matInput type="number" min="100" [(ngModel)]="heartbeatIntervalMs" />
          <mat-hint>How often the primary refreshes its heartbeat</mat-hint>
        </mat-form-field>

        <mat-form-field appearance="outline">
          <mat-label>Heartbeat TTL (ms)</mat-label>
          <input matInput type="number" min="100" [(ngModel)]="heartbeatTtlMs" />
          <mat-hint [class.hint-warn]="!ttlValid()">A stale heartbeat older than this is considered dead</mat-hint>
        </mat-form-field>

        <mat-form-field appearance="outline">
          <mat-label>Election interval (ms)</mat-label>
          <input matInput type="number" min="100" [(ngModel)]="electionIntervalMs" />
          <mat-hint [class.hint-warn]="!electionValid()">How often each node re-checks who should be primary</mat-hint>
        </mat-form-field>
      </div>

      @if (!ttlValid() || !electionValid()) {
        <p class="warn-note">
          <mat-icon>warning</mat-icon>
          These timers don't follow the recommended thresholds above — saving will still work, but failover may be
          slower or spurious.
        </p>
      }

      @if (error()) {
        <p class="error-note">{{ error() }}</p>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="undefined" [disabled]="submitting()">Cancel</button>
      <button mat-flat-button color="primary" [disabled]="!name.trim() || submitting()" (click)="submit()">
        {{ submitting() ? 'Saving…' : 'Save changes' }}
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

      .field-row {
        display: flex;
        gap: 12px;
      }

      .field-row mat-form-field {
        flex: 1;
      }

      .section-title {
        display: flex;
        align-items: center;
        gap: 6px;
        font-size: 13px;
        font-weight: 600;
        color: var(--text-secondary);
        margin: 4px 0;
      }

      .help-icon {
        font-size: 16px;
        width: 16px;
        height: 16px;
        cursor: help;
        color: var(--text-secondary);
      }

      .hint-warn {
        color: var(--status-warn) !important;
      }

      .warn-note,
      .error-note {
        display: flex;
        align-items: flex-start;
        gap: 6px;
        font-size: 12px;
        margin: 4px 0 0;
      }

      .warn-note {
        color: var(--status-warn);
      }

      .warn-note mat-icon {
        font-size: 16px;
        width: 16px;
        height: 16px;
      }

      .error-note {
        color: var(--status-error);
      }
    `,
  ],
})
export class EditTimersDialogComponent {
  private readonly api = inject(ApiService);
  private readonly dialogRef = inject(MatDialogRef<EditTimersDialogComponent, Cluster | undefined>);

  name: string;
  description: string;
  heartbeatIntervalMs: number;
  heartbeatTtlMs: number;
  electionIntervalMs: number;

  readonly submitting = signal(false);
  readonly error = signal<string | null>(null);

  private readonly clusterId: string;

  constructor(@Inject(MAT_DIALOG_DATA) data: EditTimersDialogData) {
    this.clusterId = data.cluster.id;
    this.name = data.cluster.name;
    this.description = data.cluster.description ?? '';
    this.heartbeatIntervalMs = data.cluster.heartbeat_interval_ms;
    this.heartbeatTtlMs = data.cluster.heartbeat_ttl_ms;
    this.electionIntervalMs = data.cluster.election_interval_ms;
  }

  ttlValid(): boolean {
    return this.heartbeatTtlMs >= this.heartbeatIntervalMs * 2;
  }

  electionValid(): boolean {
    return this.electionIntervalMs <= this.heartbeatTtlMs / 2;
  }

  submit(): void {
    const name = this.name.trim();
    if (!name) return;

    const body: UpdateClusterRequest = {
      name,
      description: this.description.trim() || null,
      heartbeat_interval_ms: this.heartbeatIntervalMs,
      heartbeat_ttl_ms: this.heartbeatTtlMs,
      election_interval_ms: this.electionIntervalMs,
    };

    this.submitting.set(true);
    this.error.set(null);
    this.api.updateCluster(this.clusterId, body).subscribe({
      next: (cluster) => {
        this.submitting.set(false);
        this.dialogRef.close(cluster);
      },
      error: (err: unknown) => {
        this.submitting.set(false);
        this.error.set(extractErrorMessage(err));
      },
    });
  }
}
