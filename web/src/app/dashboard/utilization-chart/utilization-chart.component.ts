import {
  ChangeDetectionStrategy,
  Component,
  ElementRef,
  EventEmitter,
  Output,
  computed,
  input,
  signal,
  viewChild,
} from '@angular/core';

import { ChartPoint } from '../../shared/models';
import { EmptyStateComponent } from '../../shared/components/empty-state/empty-state.component';

export type ChartRange = '24h' | '7d' | '30d';

const RANGES: ChartRange[] = ['24h', '7d', '30d'];
const RANGE_LABEL: Record<ChartRange, string> = { '24h': '24h', '7d': '7d', '30d': '30d' };

// viewBox units — the SVG scales to its container via preserveAspectRatio, so
// these are just a convenient coordinate space, not pixels.
const VB_WIDTH = 640;
const VB_HEIGHT = 200;
const PAD_TOP = 12;
const PAD_BOTTOM = 24;
const PAD_X = 8;
const PLOT_WIDTH = VB_WIDTH - PAD_X * 2;
const PLOT_HEIGHT = VB_HEIGHT - PAD_TOP - PAD_BOTTOM;
const GRID_STEPS = [0, 25, 50, 75, 100];

function formatTimestamp(iso: string, range: ChartRange): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return range === '24h'
    ? d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

/** Hand-rolled SVG line/area chart for the dashboard-wide utilization trend
 * (per A9: no charting library). Presentation-only: the parent fetches
 * `ChartPoint[]` from `ApiService.dashboardUtilizationChart()` and passes
 * them in; a range toggle (24h/7d/30d) emits `rangeChange` so the parent can
 * refetch. Ships a crosshair + tooltip on hover/focus and a visually-hidden
 * data table so the trend is reachable without a mouse. */
@Component({
  selector: 'app-utilization-chart',
  standalone: true,
  imports: [EmptyStateComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="chart-card gdhcp-card">
      <div class="chart-header">
        <h2>Utilization trend</h2>
        <div class="range-toggle" role="group" aria-label="Chart time range">
          @for (r of ranges; track r) {
            <button
              type="button"
              class="range-btn"
              [class.active]="range() === r"
              [attr.aria-pressed]="range() === r"
              (click)="onRangeClick(r)"
            >
              {{ rangeLabel(r) }}
            </button>
          }
        </div>
      </div>

      @if (loading()) {
        <div class="chart-loading" aria-live="polite">Loading trend…</div>
      } @else if (points().length < 2) {
        <app-empty-state
          icon="show_chart"
          title="Not enough data yet"
          message="Utilization history will appear here once enough samples are collected."
        />
      } @else {
        <div
          #plotHost
          class="chart-plot"
          tabindex="0"
          role="img"
          [attr.aria-label]="'Utilization trend, ' + points().length + ' samples over ' + rangeLabel(range())"
          (pointermove)="onPointerMove($event)"
          (pointerleave)="clearHover()"
          (keydown)="onKeydown($event)"
        >
          <svg [attr.viewBox]="'0 0 ' + vbWidth + ' ' + vbHeight" preserveAspectRatio="none" class="chart-svg">
            @for (g of gridLines(); track g.value) {
              <line class="gridline" [attr.x1]="padX" [attr.x2]="vbWidth - padX" [attr.y1]="g.y" [attr.y2]="g.y" />
              <text class="gridlabel" [attr.x]="2" [attr.y]="g.y - 3">{{ g.value }}%</text>
            }

            <path class="area" [attr.d]="areaPath()"></path>
            <path class="line" [attr.d]="linePath()"></path>

            @if (hover(); as h) {
              <line class="crosshair" [attr.x1]="h.x" [attr.x2]="h.x" [attr.y1]="padTop" [attr.y2]="vbHeight - padBottom" />
              <circle class="hover-dot" [attr.cx]="h.x" [attr.cy]="h.y" r="4" />
            }

            <circle class="end-dot" [attr.cx]="lastPoint().x" [attr.cy]="lastPoint().y" r="4"></circle>
            <text class="end-label" [attr.x]="lastPoint().x - 4" [attr.y]="lastPoint().y - 10" text-anchor="end">
              {{ lastPoint().pct.toFixed(1) }}%
            </text>
          </svg>

          @if (hover(); as h) {
            <div class="tooltip" [style.left.%]="h.fraction * 100">
              <span class="tt-value">{{ h.pct.toFixed(1) }}%</span>
              <span class="tt-label">{{ h.label }}</span>
            </div>
          }
        </div>

        <div class="chart-axis">
          <span>{{ axisLabel(0) }}</span>
          <span>{{ axisLabel(points().length - 1) }}</span>
        </div>

        <table class="sr-only">
          <caption>Utilization trend data, {{ rangeLabel(range()) }}</caption>
          <thead>
            <tr>
              <th scope="col">Time</th>
              <th scope="col">Utilization</th>
            </tr>
          </thead>
          <tbody>
            @for (p of points(); track p.timestamp) {
              <tr>
                <td>{{ axisLabelFor(p.timestamp) }}</td>
                <td>{{ p.utilization_percentage.toFixed(1) }}%</td>
              </tr>
            }
          </tbody>
        </table>
      }
    </div>
  `,
  styles: [
    `
      .chart-card {
        display: flex;
        flex-direction: column;
        gap: 12px;
      }

      .chart-header {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 12px;
      }

      h2 {
        margin: 0;
        font-size: 15px;
        font-weight: 600;
      }

      .range-toggle {
        display: flex;
        border: 1px solid var(--border-color);
        border-radius: 6px;
        overflow: hidden;
      }

      .range-btn {
        border: none;
        background: var(--bg-primary);
        color: var(--text-secondary);
        font: inherit;
        font-size: 12px;
        padding: 4px 10px;
        cursor: pointer;
      }

      .range-btn + .range-btn {
        border-left: 1px solid var(--border-color);
      }

      .range-btn.active {
        background: var(--active-bg);
        color: var(--primary-color);
        font-weight: 600;
      }

      .chart-loading {
        padding: 40px 0;
        text-align: center;
        color: var(--text-secondary);
        font-size: 13px;
      }

      .chart-plot {
        position: relative;
        outline: none;
      }

      .chart-plot:focus-visible {
        outline: 2px solid var(--primary-color);
        outline-offset: 2px;
        border-radius: 4px;
      }

      .chart-svg {
        display: block;
        width: 100%;
        height: 200px;
      }

      .gridline {
        stroke: var(--divider-color);
        stroke-width: 1;
      }

      .gridlabel {
        fill: var(--text-secondary);
        font-size: 9px;
      }

      .area {
        fill: var(--primary-color);
        opacity: 0.1;
      }

      .line {
        fill: none;
        stroke: var(--primary-color);
        stroke-width: 2;
        stroke-linejoin: round;
        stroke-linecap: round;
      }

      .end-dot {
        fill: var(--primary-color);
        stroke: var(--bg-primary);
        stroke-width: 2;
      }

      .end-label {
        fill: var(--text-primary);
        font-size: 11px;
        font-weight: 600;
      }

      .crosshair {
        stroke: var(--text-secondary);
        stroke-width: 1;
        stroke-dasharray: 2 2;
      }

      .hover-dot {
        fill: var(--primary-color);
        stroke: var(--bg-primary);
        stroke-width: 2;
      }

      .tooltip {
        position: absolute;
        top: 8px;
        transform: translateX(-50%);
        display: flex;
        flex-direction: column;
        gap: 1px;
        background: var(--bg-primary);
        border: 1px solid var(--border-color);
        border-radius: 6px;
        padding: 4px 8px;
        box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
        pointer-events: none;
        white-space: nowrap;
      }

      .tt-value {
        font-size: 13px;
        font-weight: 700;
        color: var(--text-primary);
      }

      .tt-label {
        font-size: 11px;
        color: var(--text-secondary);
      }

      .chart-axis {
        display: flex;
        justify-content: space-between;
        font-size: 11px;
        color: var(--text-secondary);
      }

      .sr-only {
        position: absolute;
        width: 1px;
        height: 1px;
        padding: 0;
        margin: -1px;
        overflow: hidden;
        clip: rect(0, 0, 0, 0);
        white-space: nowrap;
        border: 0;
      }
    `,
  ],
})
export class UtilizationChartComponent {
  readonly points = input.required<ChartPoint[]>();
  readonly loading = input(false);
  readonly range = input<ChartRange>('24h');

  @Output() readonly rangeChange = new EventEmitter<ChartRange>();

  readonly ranges = RANGES;
  readonly vbWidth = VB_WIDTH;
  readonly vbHeight = VB_HEIGHT;
  readonly padTop = PAD_TOP;
  readonly padBottom = PAD_BOTTOM;
  readonly padX = PAD_X;

  private readonly plotHost = viewChild<ElementRef<HTMLDivElement>>('plotHost');

  private readonly hoverIndex = signal<number | null>(null);

  private readonly plotted = computed(() => {
    const pts = this.points();
    const n = pts.length;
    if (n === 0) return [] as { x: number; y: number; pct: number; label: string; fraction: number }[];
    return pts.map((p, i) => {
      const fraction = n === 1 ? 0 : i / (n - 1);
      const x = PAD_X + fraction * PLOT_WIDTH;
      const pct = Math.min(100, Math.max(0, p.utilization_percentage));
      const y = PAD_TOP + PLOT_HEIGHT - (pct / 100) * PLOT_HEIGHT;
      return { x, y, pct, label: formatTimestamp(p.timestamp, this.range()), fraction };
    });
  });

  readonly lastPoint = computed(() => {
    const plotted = this.plotted();
    return plotted.length ? plotted[plotted.length - 1] : { x: PAD_X, y: VB_HEIGHT - PAD_BOTTOM, pct: 0, label: '' };
  });

  readonly linePath = computed(() =>
    this.plotted()
      .map((p, i) => `${i === 0 ? 'M' : 'L'} ${p.x.toFixed(1)} ${p.y.toFixed(1)}`)
      .join(' '),
  );

  readonly areaPath = computed(() => {
    const plotted = this.plotted();
    if (!plotted.length) return '';
    const baseline = VB_HEIGHT - PAD_BOTTOM;
    const first = plotted[0];
    const last = plotted[plotted.length - 1];
    const line = plotted.map((p) => `L ${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(' ');
    return `M ${first.x.toFixed(1)} ${baseline.toFixed(1)} ${line} L ${last.x.toFixed(1)} ${baseline.toFixed(1)} Z`;
  });

  readonly gridLines = computed(() =>
    GRID_STEPS.map((value) => ({
      value,
      y: PAD_TOP + PLOT_HEIGHT - (value / 100) * PLOT_HEIGHT,
    })),
  );

  readonly hover = computed(() => {
    const idx = this.hoverIndex();
    if (idx === null) return null;
    const plotted = this.plotted();
    return plotted[idx] ?? null;
  });

  rangeLabel(r: ChartRange): string {
    return RANGE_LABEL[r];
  }

  onRangeClick(r: ChartRange): void {
    if (r === this.range()) return;
    this.rangeChange.emit(r);
  }

  axisLabel(index: number): string {
    const plotted = this.plotted();
    return plotted[index]?.label ?? '';
  }

  axisLabelFor(timestamp: string): string {
    return formatTimestamp(timestamp, this.range());
  }

  onPointerMove(event: PointerEvent): void {
    const host = this.plotHost()?.nativeElement;
    const n = this.points().length;
    if (!host || n === 0) return;
    const rect = host.getBoundingClientRect();
    if (rect.width === 0) return;
    const fraction = Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width));
    const idx = Math.round(fraction * (n - 1));
    this.hoverIndex.set(idx);
  }

  clearHover(): void {
    this.hoverIndex.set(null);
  }

  onKeydown(event: KeyboardEvent): void {
    const n = this.points().length;
    if (n === 0) return;
    if (event.key === 'ArrowRight') {
      event.preventDefault();
      this.hoverIndex.set(Math.min(n - 1, (this.hoverIndex() ?? -1) + 1));
    } else if (event.key === 'ArrowLeft') {
      event.preventDefault();
      this.hoverIndex.set(Math.max(0, (this.hoverIndex() ?? n) - 1));
    } else if (event.key === 'Escape') {
      this.clearHover();
    }
  }
}
