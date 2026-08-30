import { ChangeDetectionStrategy, Component, DestroyRef, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivatedRoute, NavigationEnd, Router, RouterOutlet } from '@angular/router';
import { filter, startWith } from 'rxjs/operators';

import { IpamTreeComponent } from '../../shared/ipam-tree/ipam-tree.component';

const WIDTH_STORAGE_KEY = 'gdhcp_ipam_tree_width';
const COLLAPSED_STORAGE_KEY = 'gdhcp_ipam_tree_collapsed';
const MIN_WIDTH = 240;
const MAX_WIDTH = 560;
const DEFAULT_WIDTH = 340;

function readStored(key: string, fallback: number): number {
  try {
    const raw = localStorage.getItem(key);
    if (raw === null) return fallback;
    const parsed = Number(raw);
    return Number.isFinite(parsed) ? parsed : fallback;
  } catch {
    return fallback;
  }
}

function write(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // non-fatal — the pane just won't remember its size next visit
  }
}

/**
 * Layout for the whole /ipam section: the prefix tree on the left of the
 * main content area, the routed detail page on the right.
 *
 * The tree used to live in the app sidenav, squeezed under the nav links in
 * 300px shared with everything else — the least useful space on the screen
 * for the app's primary navigation model. Here it gets full height, a
 * draggable divider, and room for type chips and utilization bars.
 */
@Component({
  selector: 'app-ipam-shell',
  standalone: true,
  imports: [RouterOutlet, IpamTreeComponent, MatIconModule, MatTooltipModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="split" [style.--tree-width.px]="collapsed() ? 0 : width()">
      @if (!collapsed()) {
        <aside class="tree-pane">
          <app-ipam-tree
            [selectedId]="selectedPrefixId()"
            [emitOnly]="true"
            (nodeSelected)="openPrefix($event)"
          />
        </aside>

        <div
          class="divider"
          role="separator"
          aria-orientation="vertical"
          tabindex="0"
          (mousedown)="startDrag($event)"
          (keydown)="onDividerKey($event)"
        >
          <button
            type="button"
            class="collapse-btn"
            matTooltip="Hide the tree"
            (click)="toggleCollapsed()"
            (mousedown)="$event.stopPropagation()"
          >
            <mat-icon>chevron_left</mat-icon>
          </button>
        </div>
      } @else {
        <button type="button" class="reveal-btn" matTooltip="Show the IPAM tree" (click)="toggleCollapsed()">
          <mat-icon>chevron_right</mat-icon>
        </button>
      }

      <section class="detail-pane">
        <router-outlet />
      </section>
    </div>
  `,
  styles: [
    `
      :host {
        display: block;
        /* Fill the shell's content area: 64px toolbar, 16px padding each side. */
        height: calc(100vh - 64px - 32px);
      }

      .split {
        display: grid;
        grid-template-columns: var(--tree-width) auto 1fr;
        height: 100%;
        min-height: 0;
      }

      .split:has(.reveal-btn) {
        grid-template-columns: auto 1fr;
      }

      .tree-pane {
        min-width: 0;
        min-height: 0;
        overflow: hidden;
        padding-right: 4px;
      }

      .divider {
        width: 9px;
        cursor: col-resize;
        position: relative;
        border-left: 1px solid var(--border-color);
        background: transparent;
      }

      .divider:hover,
      .divider:focus-visible {
        background: var(--hover-bg);
        outline: none;
      }

      .collapse-btn,
      .reveal-btn {
        position: absolute;
        top: 8px;
        left: 50%;
        transform: translateX(-50%);
        width: 20px;
        height: 20px;
        padding: 0;
        display: grid;
        place-items: center;
        border: 1px solid var(--border-color);
        border-radius: 50%;
        background: var(--bg-primary);
        color: var(--text-secondary);
        cursor: pointer;
        z-index: 1;
      }

      .collapse-btn mat-icon,
      .reveal-btn mat-icon {
        font-size: 14px;
        width: 14px;
        height: 14px;
      }

      .reveal-btn {
        position: static;
        transform: none;
        align-self: start;
        margin-right: 8px;
      }

      .detail-pane {
        min-width: 0;
        min-height: 0;
        overflow: auto;
        padding-left: 16px;
      }

      /* While dragging, stop the iframe-less page from selecting text. */
      :host(.dragging) {
        user-select: none;
      }
    `,
  ],
  host: { '[class.dragging]': 'dragging()' },
})
export class IpamShellComponent {
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  private readonly destroyRef = inject(DestroyRef);

  readonly width = signal(clamp(readStored(WIDTH_STORAGE_KEY, DEFAULT_WIDTH)));
  readonly collapsed = signal(readStored(COLLAPSED_STORAGE_KEY, 0) === 1);
  readonly dragging = signal(false);

  private readonly url = signal(this.router.url);

  /** The prefix the routed child is showing, so the tree can highlight it. */
  readonly selectedPrefixId = computed(() => {
    const match = /\/ipam\/prefixes\/([^/?#]+)/.exec(this.url());
    return match ? decodeURIComponent(match[1]) : null;
  });

  constructor() {
    this.router.events
      .pipe(
        filter((e): e is NavigationEnd => e instanceof NavigationEnd),
        startWith(null),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe(() => this.url.set(this.router.url));
  }

  openPrefix(prefixId: string): void {
    void this.router.navigate(['prefixes', prefixId], { relativeTo: this.route });
  }

  toggleCollapsed(): void {
    this.collapsed.update((c) => !c);
    write(COLLAPSED_STORAGE_KEY, this.collapsed() ? '1' : '0');
  }

  startDrag(event: MouseEvent): void {
    event.preventDefault();
    this.dragging.set(true);
    const startX = event.clientX;
    const startWidth = this.width();

    const onMove = (e: MouseEvent) => this.width.set(clamp(startWidth + e.clientX - startX));
    const onUp = () => {
      this.dragging.set(false);
      write(WIDTH_STORAGE_KEY, String(this.width()));
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }

  /** Keyboard resize, so the divider isn't mouse-only. */
  onDividerKey(event: KeyboardEvent): void {
    const step = event.shiftKey ? 48 : 16;
    if (event.key === 'ArrowLeft') {
      this.width.set(clamp(this.width() - step));
    } else if (event.key === 'ArrowRight') {
      this.width.set(clamp(this.width() + step));
    } else {
      return;
    }
    event.preventDefault();
    write(WIDTH_STORAGE_KEY, String(this.width()));
  }
}

function clamp(px: number): number {
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(px)));
}
