import { ChangeDetectionStrategy, Component, ElementRef, HostListener, Signal, computed, effect, inject, signal, viewChild } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatListModule } from '@angular/material/list';
import { MatMenuModule } from '@angular/material/menu';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSidenavModule } from '@angular/material/sidenav';
import { MatToolbarModule } from '@angular/material/toolbar';
import { Router, RouterLink, RouterLinkActive, RouterOutlet } from '@angular/router';
import { Subject } from 'rxjs';
import { debounceTime, distinctUntilChanged, switchMap } from 'rxjs/operators';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';

import { ApiService } from '../shared/api.service';
import { AuthService } from '../auth/auth.service';
import { SearchResult } from '../shared/models';

interface NavLink {
  label: string;
  path: string;
  icon: string;
}

const NAV_LINKS: NavLink[] = [
  { label: 'Dashboard', path: '/dashboard', icon: 'dashboard' },
  { label: 'IPAM', path: '/ipam', icon: 'lan' },
  { label: 'DHCP', path: '/dhcp', icon: 'dns' },
  { label: 'Clusters', path: '/ha', icon: 'hub' },
  { label: 'Admin', path: '/admin', icon: 'admin_panel_settings' },
];

const THEME_STORAGE_KEY = 'gdhcp_theme';
type ThemePreference = 'light' | 'dark' | 'system';

function loadThemePreference(): ThemePreference {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY);
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored;
  } catch {
    // ignore — default below
  }
  return 'system';
}

/** Main authenticated app shell: toolbar (global search, theme toggle, user
 * menu), a sidenav of top-level nav links, and the routed feature content.
 *
 * The IPAM tree used to be crammed into this sidenav below the nav links;
 * it now lives in the IPAM section's own split layout
 * (ipam/layout/ipam-shell.component.ts), where it has full height and room
 * for type chips and utilization bars. */
@Component({
  selector: 'app-shell',
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    RouterLinkActive,
    RouterOutlet,
    MatButtonModule,
    MatIconModule,
    MatListModule,
    MatMenuModule,
    MatProgressBarModule,
    MatSidenavModule,
    MatToolbarModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <mat-sidenav-container class="shell">
      <mat-sidenav mode="side" [opened]="true" class="sidenav">
        <mat-nav-list class="nav-links">
          @for (link of navLinks; track link.path) {
            <a mat-list-item [routerLink]="link.path" routerLinkActive="active-link">
              <mat-icon matListItemIcon>{{ link.icon }}</mat-icon>
              <span matListItemTitle>{{ link.label }}</span>
            </a>
          }
        </mat-nav-list>
      </mat-sidenav>

      <mat-sidenav-content>
        <mat-toolbar class="toolbar">
          <span class="brand">gdhcp</span>

          <button
            type="button"
            class="search-trigger"
            (click)="openSearch()"
            aria-label="Global search"
          >
            <mat-icon>search</mat-icon>
            <span>Search…</span>
            <span class="kbd">Ctrl K</span>
          </button>

          <span class="spacer"></span>

          <button mat-icon-button (click)="cycleTheme()" [attr.aria-label]="'Theme: ' + theme()">
            <mat-icon>{{ themeIcon() }}</mat-icon>
          </button>

          <button mat-icon-button [matMenuTriggerFor]="userMenu" aria-label="User menu">
            <mat-icon>account_circle</mat-icon>
          </button>
          <mat-menu #userMenu="matMenu">
            <div class="menu-username">{{ username() ?? 'Signed in' }}</div>
            <button mat-menu-item routerLink="/admin/users">
              <mat-icon>manage_accounts</mat-icon>
              <span>Users</span>
            </button>
            <button mat-menu-item (click)="logout()">
              <mat-icon>logout</mat-icon>
              <span>Sign out</span>
            </button>
          </mat-menu>
        </mat-toolbar>

        <main class="content">
          <router-outlet />
        </main>
      </mat-sidenav-content>
    </mat-sidenav-container>

    @if (searchOpen()) {
      <div class="search-overlay" (click)="closeSearch()">
        <div class="search-panel" (click)="$event.stopPropagation()">
          <div class="search-input-row">
            <mat-icon>search</mat-icon>
            <input
              #searchInput
              type="text"
              placeholder="Search VRFs, prefixes, scopes, pools, templates…"
              [(ngModel)]="searchQuery"
              (ngModelChange)="onQueryChange($event)"
            />
            <button mat-icon-button (click)="closeSearch()" aria-label="Close search">
              <mat-icon>close</mat-icon>
            </button>
          </div>

          @if (searchLoading()) {
            <mat-progress-bar mode="indeterminate" />
          }

          <div class="search-groups">
            @for (group of groupedResults(); track group.type) {
              <div class="search-group">
                <div class="group-label">{{ group.type }}</div>
                @for (result of group.results; track result.entity_id) {
                  <button type="button" class="result-row" (click)="goTo(result.url)">
                    <span class="title">{{ result.title }}</span>
                    @if (result.subtitle) {
                      <span class="subtitle">{{ result.subtitle }}</span>
                    }
                  </button>
                }
              </div>
            }
            @if (searchQuery.trim() && !searchLoading() && groupedResults().length === 0) {
              <p class="empty">No results for "{{ searchQuery }}"</p>
            }
          </div>
        </div>
      </div>
    }
  `,
  styles: [
    `
      .shell {
        height: 100vh;
      }

      .sidenav {
        width: 220px;
        display: flex;
        flex-direction: column;
        background: var(--bg-sidebar);
        border-right: 1px solid var(--border-color);
        padding: 8px;
        box-sizing: border-box;
      }

      .nav-links {
        flex: 0 0 auto;
      }

      .active-link {
        background: var(--active-bg);
      }

      .toolbar {
        display: flex;
        align-items: center;
        gap: 12px;
        border-bottom: 1px solid var(--border-color);
        background: var(--bg-primary);
        color: var(--text-primary);
      }

      .brand {
        font-weight: 600;
        letter-spacing: 0.02em;
      }

      .search-trigger {
        display: flex;
        align-items: center;
        gap: 8px;
        border: 1px solid var(--border-color);
        border-radius: 6px;
        background: var(--bg-secondary);
        color: var(--text-secondary);
        padding: 6px 10px;
        cursor: pointer;
        font: inherit;
        max-width: 320px;
        flex: 1;
      }

      .kbd {
        margin-left: auto;
        font-size: 11px;
        border: 1px solid var(--border-color);
        border-radius: 3px;
        padding: 0 4px;
      }

      .spacer {
        flex: 1;
      }

      .menu-username {
        padding: 8px 16px;
        font-size: 12px;
        color: var(--text-secondary);
      }

      .content {
        padding: 16px 24px;
        min-height: calc(100vh - 64px);
        box-sizing: border-box;
      }

      .search-overlay {
        position: fixed;
        inset: 0;
        background: rgba(0, 0, 0, 0.4);
        display: flex;
        align-items: flex-start;
        justify-content: center;
        padding-top: 96px;
        z-index: 1000;
      }

      .search-panel {
        width: 100%;
        max-width: 560px;
        max-height: 70vh;
        display: flex;
        flex-direction: column;
        background: var(--bg-primary);
        border-radius: 8px;
        box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
        overflow: hidden;
      }

      .search-input-row {
        display: flex;
        align-items: center;
        gap: 8px;
        padding: 12px 16px;
        border-bottom: 1px solid var(--border-color);
      }

      .search-input-row input {
        flex: 1;
        border: none;
        outline: none;
        background: transparent;
        font: inherit;
        color: var(--text-primary);
      }

      .search-groups {
        overflow-y: auto;
        padding: 8px 0;
      }

      .group-label {
        font-size: 11px;
        text-transform: uppercase;
        letter-spacing: 0.05em;
        color: var(--text-secondary);
        padding: 8px 16px 4px;
      }

      .result-row {
        width: 100%;
        display: flex;
        flex-direction: column;
        align-items: flex-start;
        text-align: left;
        gap: 2px;
        border: none;
        background: none;
        font: inherit;
        color: var(--text-primary);
        padding: 6px 16px;
        cursor: pointer;
      }

      .result-row:hover {
        background: var(--hover-bg);
      }

      .result-row .subtitle {
        font-size: 12px;
        color: var(--text-secondary);
      }

      .empty {
        text-align: center;
        color: var(--text-secondary);
        font-size: 13px;
        padding: 24px;
      }
    `,
  ],
})
export class ShellComponent {
  private readonly api = inject(ApiService);
  private readonly auth = inject(AuthService);
  private readonly router = inject(Router);

  readonly navLinks = NAV_LINKS;
  readonly username: Signal<string | null> = this.auth.username;

  readonly searchOpen = signal(false);
  readonly searchLoading = signal(false);
  readonly searchResults = signal<SearchResult[]>([]);
  searchQuery = '';

  readonly searchInputRef = viewChild<ElementRef<HTMLInputElement>>('searchInput');

  readonly groupedResults = computed(() => {
    const results = this.searchResults();
    const byType = new Map<string, SearchResult[]>();
    for (const result of results) {
      const bucket = byType.get(result.type) ?? [];
      bucket.push(result);
      byType.set(result.type, bucket);
    }
    return Array.from(byType.entries()).map(([type, items]) => ({ type, results: items }));
  });

  private readonly themeSignal = signal<ThemePreference>(loadThemePreference());
  readonly theme = this.themeSignal.asReadonly();
  readonly themeIcon = computed(() => {
    switch (this.themeSignal()) {
      case 'dark':
        return 'dark_mode';
      case 'light':
        return 'light_mode';
      default:
        return 'brightness_auto';
    }
  });

  private readonly query$ = new Subject<string>();

  constructor() {
    effect(() => {
      const pref = this.themeSignal();
      const root = document.documentElement;
      if (pref === 'system') {
        root.removeAttribute('data-gdhcp-theme');
      } else {
        root.setAttribute('data-gdhcp-theme', pref);
      }
      try {
        localStorage.setItem(THEME_STORAGE_KEY, pref);
      } catch {
        // non-fatal
      }
    });

    this.query$
      .pipe(
        debounceTime(200),
        distinctUntilChanged(),
        switchMap((q) => {
          if (!q.trim()) {
            this.searchResults.set([]);
            return [];
          }
          this.searchLoading.set(true);
          return this.api.search(q.trim(), undefined, 50);
        }),
        takeUntilDestroyed(),
      )
      .subscribe((results) => {
        this.searchLoading.set(false);
        this.searchResults.set(results);
      });
  }

  @HostListener('window:keydown', ['$event'])
  onGlobalKeydown(event: KeyboardEvent): void {
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
      event.preventDefault();
      this.openSearch();
    } else if (event.key === 'Escape' && this.searchOpen()) {
      this.closeSearch();
    }
  }

  openSearch(): void {
    this.searchOpen.set(true);
    queueMicrotask(() => this.searchInputRef()?.nativeElement.focus());
  }

  closeSearch(): void {
    this.searchOpen.set(false);
    this.searchQuery = '';
    this.searchResults.set([]);
  }

  onQueryChange(value: string): void {
    this.query$.next(value);
  }

  goTo(path: string): void {
    this.closeSearch();
    this.router.navigateByUrl(path);
  }

  cycleTheme(): void {
    const order: ThemePreference[] = ['system', 'light', 'dark'];
    const next = order[(order.indexOf(this.themeSignal()) + 1) % order.length];
    this.themeSignal.set(next);
  }

  logout(): void {
    this.auth.logout().subscribe({
      complete: () => this.router.navigate(['/login']),
    });
  }
}
