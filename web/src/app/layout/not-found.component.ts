import { ChangeDetectionStrategy, Component } from '@angular/core';
import { RouterLink } from '@angular/router';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';

@Component({
  selector: 'app-not-found',
  standalone: true,
  imports: [RouterLink, MatButtonModule, MatIconModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="not-found">
      <mat-icon class="icon">error_outline</mat-icon>
      <h1>Page not found</h1>
      <p>The page you're looking for doesn't exist or has moved.</p>
      <a mat-flat-button color="primary" routerLink="/dashboard">Back to dashboard</a>
    </div>
  `,
  styles: [
    `
      .not-found {
        min-height: 100vh;
        display: flex;
        flex-direction: column;
        align-items: center;
        justify-content: center;
        text-align: center;
        gap: 8px;
        background: var(--bg-primary);
        color: var(--text-primary);
      }

      .icon {
        font-size: 48px;
        width: 48px;
        height: 48px;
        color: var(--text-secondary);
        margin-bottom: 8px;
      }

      p {
        color: var(--text-secondary);
        margin-bottom: 16px;
      }
    `,
  ],
})
export class NotFoundComponent {}
