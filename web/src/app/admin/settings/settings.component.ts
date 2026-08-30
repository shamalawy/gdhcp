import { ChangeDetectionStrategy, Component, OnInit, computed, inject, signal } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatSnackBar } from '@angular/material/snack-bar';
import { catchError, of } from 'rxjs';

import { ApiService } from '../../shared/api.service';
import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { StatusDotComponent } from '../../shared/components/status-dot/status-dot.component';
import { EntityStatus, HealthResponse, RunningConfig } from '../../shared/models';
import { extractErrorMessage } from '../http-error.util';

function statusFor(status: HealthResponse['status'] | null): EntityStatus {
  switch (status) {
    case 'ok':
      return 'ok';
    case 'degraded':
      return 'warn';
    case 'down':
      return 'error';
    default:
      return 'unknown';
  }
}

function formatConfigValue(v: unknown): string {
  if (v === null || v === undefined) return '—';
  if (typeof v === 'boolean') return v ? 'true' : 'false';
  if (typeof v === 'string' || typeof v === 'number') return String(v);
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}

/** `/admin/settings` — read-only running config (per endpoints.md `GET
 * /config` is read-only; changes are made through config files / CLI, not
 * this UI) and server version/health, sourced from `GET /health`. */
@Component({
  selector: 'app-settings',
  standalone: true,
  imports: [MatIconModule, PageHeaderComponent, StatusDotComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="Settings" />

    <section class="gdhcp-card overview">
      <h2>Server</h2>
      @if (loadingHealth()) {
        <p class="hint">Loading…</p>
      } @else {
        @if (health(); as h) {
          <div class="overview-row">
            <div class="overview-field">
              <span class="field-label">Version</span>
              <span class="field-value gdhcp-mono">{{ h.version }}</span>
            </div>
            <div class="overview-field">
              <span class="field-label">Status</span>
              <span class="field-value">
                <app-status-dot [status]="statusOf(h.status)" [tooltip]="h.status" />
                {{ h.status }}
              </span>
            </div>
            <div class="overview-field">
              <span class="field-label">Database</span>
              <span class="field-value">
                <mat-icon [class.ok-icon]="h.db" [class.bad-icon]="!h.db">{{ h.db ? 'check_circle' : 'cancel' }}</mat-icon>
                {{ h.db ? 'Reachable' : 'Unreachable' }}
              </span>
            </div>
            <div class="overview-field">
              <span class="field-label">HA</span>
              <span class="field-value">
                <mat-icon [class.ok-icon]="h.ha" [class.bad-icon]="!h.ha">{{ h.ha ? 'check_circle' : 'cancel' }}</mat-icon>
                {{ h.ha ? 'Healthy' : 'Not healthy / disabled' }}
              </span>
            </div>
          </div>
        } @else {
          <p class="hint">Could not load server health.</p>
        }
      }
    </section>

    <section class="gdhcp-card">
      <h2>Running configuration</h2>
      <p class="hint plain">Read-only — reflects the config this node loaded at startup (file + env + defaults).</p>
      @if (loadingConfig()) {
        <p class="hint">Loading…</p>
      } @else if (configEntries().length === 0) {
        <p class="hint">Could not load running configuration.</p>
      } @else {
        <table class="config-table">
          <tbody>
            @for (entry of configEntries(); track entry.key) {
              <tr>
                <td class="config-key gdhcp-mono">{{ entry.key }}</td>
                <td class="config-value gdhcp-mono">{{ entry.value }}</td>
              </tr>
            }
          </tbody>
        </table>
      }
    </section>
  `,
  styles: [
    `
      section.gdhcp-card {
        margin-bottom: 16px;
      }

      h2 {
        margin: 0 0 8px;
        font-size: 15px;
        font-weight: 600;
      }

      .hint {
        color: var(--text-secondary);
        font-size: 13px;
      }

      .hint.plain {
        margin: 0 0 12px;
      }

      .overview-row {
        display: flex;
        flex-wrap: wrap;
        gap: 24px;
      }

      .overview-field {
        display: flex;
        flex-direction: column;
        gap: 2px;
        min-width: 120px;
      }

      .field-label {
        font-size: 11px;
        color: var(--text-secondary);
        text-transform: uppercase;
        letter-spacing: 0.03em;
      }

      .field-value {
        display: flex;
        align-items: center;
        gap: 6px;
        font-size: 14px;
        color: var(--text-primary);
      }

      .field-value mat-icon {
        font-size: 18px;
        width: 18px;
        height: 18px;
      }

      .ok-icon {
        color: var(--status-ok);
      }

      .bad-icon {
        color: var(--status-error);
      }

      .config-table {
        width: 100%;
        border-collapse: collapse;
        font-size: 12px;
      }

      .config-table td {
        padding: 6px 8px;
        border-bottom: 1px solid var(--divider-color);
        vertical-align: top;
      }

      .config-key {
        color: var(--text-secondary);
        white-space: nowrap;
        width: 1%;
        padding-right: 24px;
      }

      .config-value {
        color: var(--text-primary);
        white-space: pre-wrap;
        word-break: break-all;
      }
    `,
  ],
})
export class SettingsComponent implements OnInit {
  private readonly api = inject(ApiService);
  private readonly snackBar = inject(MatSnackBar);

  readonly health = signal<HealthResponse | null>(null);
  readonly loadingHealth = signal(true);

  private readonly config = signal<RunningConfig | null>(null);
  readonly loadingConfig = signal(true);

  readonly configEntries = computed(() => {
    const config = this.config();
    if (!config) return [];
    return Object.entries(config).map(([key, value]) => ({ key, value: formatConfigValue(value) }));
  });

  ngOnInit(): void {
    this.api
      .health()
      .pipe(catchError((err: unknown) => {
        this.snackBar.open(extractErrorMessage(err), 'Dismiss', { duration: 4000 });
        return of(null);
      }))
      .subscribe((h) => {
        this.health.set(h);
        this.loadingHealth.set(false);
      });

    this.api
      .runningConfig()
      .pipe(catchError(() => of(null)))
      .subscribe((c) => {
        this.config.set(c);
        this.loadingConfig.set(false);
      });
  }

  statusOf(status: HealthResponse['status']): EntityStatus {
    return statusFor(status);
  }
}
