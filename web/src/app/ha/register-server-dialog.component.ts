import { ChangeDetectionStrategy, Component, Inject, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MAT_DIALOG_DATA, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';

import { ApiService } from '../shared/api.service';
import { ClusterServer, RegisterServerRequest } from '../shared/models';
import { extractErrorMessage } from './http-error.util';

export interface RegisterServerDialogData {
  clusterId: string;
}

/** Manual server registration. Servers normally self-register on boot (each
 * node calls `POST /clusters/{id}/servers` itself using its own config) —
 * this dialog exists for the documented manual path (endpoints.md marks it
 * "Register server (manual)"): pre-provisioning a node before it first
 * boots, or re-registering one after a `node_id` change. */
@Component({
  selector: 'app-register-server-dialog',
  standalone: true,
  imports: [FormsModule, MatButtonModule, MatDialogModule, MatFormFieldModule, MatInputModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <h2 mat-dialog-title>Register server manually</h2>
    <mat-dialog-content class="form">
      <p class="help">
        Servers normally register themselves the first time they boot with this cluster's ID in their config — you
        shouldn't need this for a normal node. Use it to pre-provision a node ahead of time, or to fix a server whose
        registration was removed.
      </p>

      <mat-form-field appearance="outline">
        <mat-label>Node name</mat-label>
        <input matInput [(ngModel)]="nodeName" required placeholder="dhcp-03" />
      </mat-form-field>

      <mat-form-field appearance="outline">
        <mat-label>Node ID</mat-label>
        <input matInput type="number" [(ngModel)]="nodeId" required />
        <mat-hint>Used for election tie-breaking — lowest node_id wins</mat-hint>
      </mat-form-field>

      <mat-form-field appearance="outline">
        <mat-label>API address</mat-label>
        <input matInput [(ngModel)]="apiAddress" required placeholder="192.168.1.12:8080" />
      </mat-form-field>

      <mat-form-field appearance="outline">
        <mat-label>DHCP address</mat-label>
        <input matInput [(ngModel)]="dhcpAddress" required placeholder="0.0.0.0:67" />
      </mat-form-field>

      @if (error()) {
        <p class="error-note">{{ error() }}</p>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button [mat-dialog-close]="undefined" [disabled]="submitting()">Cancel</button>
      <button mat-flat-button color="primary" [disabled]="!valid() || submitting()" (click)="submit()">
        {{ submitting() ? 'Registering…' : 'Register server' }}
      </button>
    </mat-dialog-actions>
  `,
  styles: [
    `
      .form {
        display: flex;
        flex-direction: column;
        gap: 4px;
        min-width: 380px;
      }

      .help {
        font-size: 12px;
        color: var(--text-secondary);
        margin: 0 0 8px;
      }

      .error-note {
        font-size: 12px;
        color: var(--status-error);
        margin: 4px 0 0;
      }
    `,
  ],
})
export class RegisterServerDialogComponent {
  private readonly api = inject(ApiService);
  private readonly dialogRef = inject(MatDialogRef<RegisterServerDialogComponent, ClusterServer | undefined>);
  private readonly clusterId: string;

  nodeName = '';
  nodeId: number | null = null;
  apiAddress = '';
  dhcpAddress = '';

  readonly submitting = signal(false);
  readonly error = signal<string | null>(null);

  constructor(@Inject(MAT_DIALOG_DATA) data: RegisterServerDialogData) {
    this.clusterId = data.clusterId;
  }

  valid(): boolean {
    return !!this.nodeName.trim() && this.nodeId !== null && !!this.apiAddress.trim() && !!this.dhcpAddress.trim();
  }

  submit(): void {
    if (!this.valid()) return;

    const body: RegisterServerRequest = {
      node_name: this.nodeName.trim(),
      node_id: this.nodeId as number,
      api_address: this.apiAddress.trim(),
      dhcp_address: this.dhcpAddress.trim(),
    };

    this.submitting.set(true);
    this.error.set(null);
    this.api.registerClusterServer(this.clusterId, body).subscribe({
      next: (server) => {
        this.submitting.set(false);
        this.dialogRef.close(server);
      },
      error: (err: unknown) => {
        this.submitting.set(false);
        this.error.set(extractErrorMessage(err));
      },
    });
  }
}
