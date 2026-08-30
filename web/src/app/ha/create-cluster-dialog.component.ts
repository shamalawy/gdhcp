import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatTooltipModule } from '@angular/material/tooltip';

import { ApiService } from '../shared/api.service';
import { Cluster, CreateClusterRequest } from '../shared/models';
import { extractErrorMessage } from './http-error.util';

/** Create-cluster dialog: name, VIP address + interface, and the three HA
 * timers with inline help text (thresholds per docs/design/ha-cluster.md —
 * `heartbeat_ttl_ms` should be >= 2x `heartbeat_interval_ms`, and
 * `election_interval_ms` should be <= `heartbeat_ttl_ms / 2`). The dialog
 * performs the create call itself and closes with the created Cluster. */
@Component({
  selector: 'app-create-cluster-dialog',
  standalone: true,
  imports: [FormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatIconModule, MatInputModule, MatTooltipModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>New cluster</h2>
    <mat-dialog-content class="form">
      <mat-form-field appearance="outline">
        <mat-label>Name</mat-label>
        <input matInput [(ngModel)]="name" required maxlength="255" />
      </mat-form-field>

      <mat-form-field appearance="outline">
        <mat-label>Description</mat-label>
        <textarea matInput rows="2" [(ngModel)]="description" placeholder="Optional"></textarea>
      </mat-form-field>

      <div class="field-row">
        <mat-form-field appearance="outline">
          <mat-label>VIP address</mat-label>
          <input matInput [(ngModel)]="vipAddress" placeholder="10.0.0.10/24" />
          <mat-hint>Virtual IP the primary node holds — CIDR or bare IP</mat-hint>
        </mat-form-field>

        <mat-form-field appearance="outline">
          <mat-label>VIP interface</mat-label>
          <input matInput [(ngModel)]="vipInterface" placeholder="eth0" />
          <mat-hint>Network interface the VIP is assigned to on each node</mat-hint>
        </mat-form-field>
      </div>

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
          These timers don't follow the recommended thresholds above — the cluster will still be created, but failover
          may be slower or spurious.
        </p>
      }

      @if (error()) {
        <p class="error-note">{{ error() }}</p>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="undefined" [disabled]="submitting()">Cancel</button>
      <button mat-flat-button color="primary" [disabled]="!name.trim() || submitting()" (click)="submit()">
        {{ submitting() ? 'Creating…' : 'Create cluster' }}
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
export class CreateClusterDialogComponent {
  private readonly api = inject(ApiService);
  private readonly dialogRef = inject(MatDialogRef<CreateClusterDialogComponent, Cluster | undefined>);

  name = '';
  description = '';
  vipAddress = '';
  vipInterface = '';
  heartbeatIntervalMs = 1000;
  heartbeatTtlMs = 5000;
  electionIntervalMs = 2000;

  readonly submitting = signal(false);
  readonly error = signal<string | null>(null);

  ttlValid(): boolean {
    return this.heartbeatTtlMs >= this.heartbeatIntervalMs * 2;
  }

  electionValid(): boolean {
    return this.electionIntervalMs <= this.heartbeatTtlMs / 2;
  }

  submit(): void {
    const name = this.name.trim();
    if (!name) return;

    const body: CreateClusterRequest = {
      name,
      description: this.description.trim() || null,
      vip_address: this.vipAddress.trim() || null,
      vip_interface: this.vipInterface.trim() || null,
      heartbeat_interval_ms: this.heartbeatIntervalMs,
      heartbeat_ttl_ms: this.heartbeatTtlMs,
      election_interval_ms: this.electionIntervalMs,
    };

    this.submitting.set(true);
    this.error.set(null);
    this.api.createCluster(body).subscribe({
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
