import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';

/** Utilization thresholds per docs/design/frontend.md: <60% ok (green),
 * 60-80% warn (amber), >80% crit (red) — matches the --util-* CSS vars in
 * src/styles.scss. */
export type UtilizationLevel = 'ok' | 'warn' | 'crit';

export const UTIL_WARN_THRESHOLD = 60;
export const UTIL_CRIT_THRESHOLD = 80;

/** The single place these cutoffs live. The tree's status dot used to carry
 * its own 75/90 pair, so a bar could render amber next to a green dot on the
 * same row; everything now imports this. */
export function levelFor(pct: number): UtilizationLevel {
  if (pct > UTIL_CRIT_THRESHOLD) return 'crit';
  if (pct >= UTIL_WARN_THRESHOLD) return 'warn';
  return 'ok';
}

@Component({
  selector: 'app-utilization-bar',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="util-bar" [class.compact]="compact()">
      <div class="track" [attr.aria-label]="label()" role="img">
        <div
          class="fill"
          [class]="level()"
          [style.width.%]="pct()"
        ></div>
      </div>

      @if (sparkline() && sparkline()!.length > 1) {
        <svg class="sparkline" [attr.viewBox]="'0 0 ' + sparklineWidth + ' ' + sparklineHeight" preserveAspectRatio="none">
          <polyline [attr.points]="sparklinePoints()" [class]="level()" />
        </svg>
      }

      @if (!compact()) {
        <span class="label">{{ label() }}</span>
      }
    </div>
  `,
  styles: [
    `
      .util-bar {
        display: flex;
        align-items: center;
        gap: 8px;
        min-width: 0;
      }

      .track {
        flex: 1 1 auto;
        min-width: 40px;
        height: 8px;
        border-radius: 4px;
        background: var(--util-track);
        overflow: hidden;
      }

      .compact .track {
        height: 6px;
      }

      .fill {
        height: 100%;
        border-radius: 4px;
        transition: width 0.25s ease;
      }

      .fill.ok, polyline.ok { background: var(--util-ok); stroke: var(--util-ok); }
      .fill.warn, polyline.warn { background: var(--util-warn); stroke: var(--util-warn); }
      .fill.crit, polyline.crit { background: var(--util-crit); stroke: var(--util-crit); }

      .sparkline {
        width: 48px;
        height: 16px;
        flex: 0 0 auto;
      }

      .sparkline polyline {
        fill: none;
        stroke-width: 6;
        vector-effect: non-scaling-stroke;
      }

      .label {
        font-size: 12px;
        color: var(--text-secondary);
        white-space: nowrap;
        flex: 0 0 auto;
      }
    `,
  ],
})
export class UtilizationBarComponent {
  readonly used = input.required<number>();
  readonly total = input.required<number>();
  /** Compact renders just the bar (no numeric label) for dense table rows. */
  readonly compact = input(false);
  /** Optional recent-history samples (0-100 values) rendered as a tiny trend line. */
  readonly sparkline = input<number[] | null>(null);

  readonly sparklineWidth = 48;
  readonly sparklineHeight = 16;

  readonly pct = computed(() => {
    const total = this.total();
    if (!total || total <= 0) return 0;
    return Math.min(100, Math.max(0, (this.used() / total) * 100));
  });

  readonly level = computed<UtilizationLevel>(() => levelFor(this.pct()));

  readonly label = computed(() => `${this.used()} / ${this.total()} (${this.pct().toFixed(1)}%)`);

  readonly sparklinePoints = computed(() => {
    const values = this.sparkline();
    if (!values || values.length < 2) return '';
    const max = Math.max(100, ...values);
    const stepX = this.sparklineWidth / (values.length - 1);
    return values
      .map((v, i) => {
        const x = i * stepX;
        const y = this.sparklineHeight - (Math.min(100, Math.max(0, v)) / max) * this.sparklineHeight;
        return `${x.toFixed(1)},${y.toFixed(1)}`;
      })
      .join(' ');
  });
}
