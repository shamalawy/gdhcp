import { ChangeDetectionStrategy, Component } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatTabsModule } from '@angular/material/tabs';
import { RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';

interface DhcpTab {
  label: string;
  path: string;
  icon: string;
  hint: string;
}

/**
 * Section navigation for /dhcp. Templates and the option catalogue were
 * fully built but unreachable — nothing linked to them and /dhcp redirected
 * straight to scopes, so they existed only for anyone who typed the URL or
 * found them through Ctrl-K. This strip is the entry point.
 */
const DHCP_TABS: DhcpTab[] = [
  { label: 'Scopes', path: 'scopes', icon: 'dns', hint: 'DHCP scopes and their address pools' },
  { label: 'Templates', path: 'templates', icon: 'layers', hint: 'Reusable option bundles attached to scopes and pools' },
  { label: 'Options', path: 'options', icon: 'list_alt', hint: 'The IANA DHCPv4 option catalogue' },
];

@Component({
  selector: 'app-dhcp-shell',
  standalone: true,
  imports: [RouterLink, RouterLinkActive, RouterOutlet, MatIconModule, MatTabsModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <nav mat-tab-nav-bar [tabPanel]="panel" class="dhcp-tabs">
      @for (tab of tabs; track tab.path) {
        <a
          mat-tab-link
          [routerLink]="tab.path"
          routerLinkActive
          #rla="routerLinkActive"
          [active]="rla.isActive"
          [title]="tab.hint"
        >
          <mat-icon class="tab-icon">{{ tab.icon }}</mat-icon>
          {{ tab.label }}
        </a>
      }
    </nav>

    <mat-tab-nav-panel #panel>
      <div class="tab-body">
        <router-outlet />
      </div>
    </mat-tab-nav-panel>
  `,
  styles: [
    `
      .dhcp-tabs {
        margin-bottom: 16px;
      }

      .tab-icon {
        margin-right: 6px;
        width: 18px;
        height: 18px;
        font-size: 18px;
        vertical-align: middle;
      }

      .tab-body {
        min-width: 0;
      }
    `,
  ],
})
export class DhcpShellComponent {
  readonly tabs = DHCP_TABS;
}
