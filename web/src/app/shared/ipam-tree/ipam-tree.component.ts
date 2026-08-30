import { CollectionViewer, DataSource, SelectionChange } from '@angular/cdk/collections';
import { FlatTreeControl } from '@angular/cdk/tree';
import {
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  Component,
  DestroyRef,
  effect,
  inject,
  input,
  output,
  signal,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { NgTemplateOutlet } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { MatTreeModule } from '@angular/material/tree';
import { Router } from '@angular/router';
import { BehaviorSubject, Observable, Subject } from 'rxjs';
import { debounceTime, distinctUntilChanged, switchMap } from 'rxjs/operators';

import { ApiService } from '../api.service';
import { EntityStatus, IpamTreeNode, PrefixType, SearchResult, VRF } from '../models';
import { StatusDotComponent } from '../components/status-dot/status-dot.component';
import { UtilizationBarComponent, levelFor } from '../components/utilization-bar/utilization-bar.component';
import { PREFIX_TYPE_META, PREFIX_TYPE_ORDER } from '../../ipam/util/prefix-type.util';

/** What a node's bar is measuring. Aggregates and containers have no hosts
 * of their own, so the only meaningful number at those levels is how much of
 * the block has been carved up; subnets report real DHCP lease usage. */
type BarKind = 'allocated' | 'dhcp' | 'none';

interface FlatIpamNode {
  id: string;
  name: string;
  cidr: string;
  prefixType: PrefixType;
  barKind: BarKind;
  barPct: number;
  status: EntityStatus;
  hasScope: boolean;
  scopeId: string | null;
  vlanVid: number | null;
  vlanName: string | null;
  level: number;
  expandable: boolean;
  loading: boolean;
}

const VRF_STORAGE_KEY = 'gdhcp_ipam_tree_vrf';

/** Derive the dot from the same cutoffs the bar beside it uses — these used
 * to disagree (75/90 here vs 60/80 in the bar), so a row could show an amber
 * bar next to a green dot. */
function statusFor(kind: BarKind, pct: number): EntityStatus {
  if (kind === 'none') return 'unknown';
  switch (levelFor(pct)) {
    case 'crit':
      return 'error';
    case 'warn':
      return 'warn';
    default:
      return 'ok';
  }
}

function barFor(node: IpamTreeNode): { kind: BarKind; pct: number } {
  if (node.prefix.prefix_type === 'subnet') {
    const dhcp = node.prefix.utilization_percentage;
    // A subnet with no scope anywhere beneath it has nothing to measure —
    // its own space_pct is about child prefixes, which subnets rarely have.
    return dhcp === null ? { kind: 'none', pct: 0 } : { kind: 'dhcp', pct: dhcp };
  }
  return { kind: 'allocated', pct: node.space_pct ?? 0 };
}

function toFlat(node: IpamTreeNode, level: number): FlatIpamNode {
  const bar = barFor(node);
  return {
    id: node.prefix.id,
    name: node.prefix.name || node.prefix.prefix,
    cidr: node.prefix.prefix,
    prefixType: node.prefix.prefix_type,
    barKind: bar.kind,
    barPct: bar.pct,
    status: statusFor(bar.kind, bar.pct),
    hasScope: node.has_scope,
    scopeId: node.scope_id ?? null,
    vlanVid: node.prefix.vlan_vid ?? null,
    vlanName: node.prefix.vlan_name ?? null,
    level,
    // depth=1 fetches (every call this data source makes) put the "does
    // this node have more below it" answer in child_count, not a
    // has_children field the real API never sends.
    expandable: node.child_count > 0,
    loading: false,
  };
}

/**
 * Backs the mat-tree with a flat node array, splicing children in/out on
 * expand/collapse (the standard Angular Material "dynamic data source"
 * pattern) so each level is fetched from `GET /api/v1/ipam/tree` only when
 * the user actually expands it.
 */
class IpamTreeDataSource implements DataSource<FlatIpamNode> {
  private readonly dataChange = new BehaviorSubject<FlatIpamNode[]>([]);

  get data(): FlatIpamNode[] {
    return this.dataChange.value;
  }
  set data(value: FlatIpamNode[]) {
    this.dataChange.next(value);
    // The tree's own dataSource subscription re-renders row structure, but
    // in-place mutations on existing node objects (e.g. `node.loading`)
    // need the declaring (OnPush) component explicitly marked dirty too.
    this.onChange();
  }

  constructor(
    private readonly treeControl: FlatTreeControl<FlatIpamNode>,
    private readonly api: ApiService,
    private readonly getVrfId: () => string | null,
    private readonly onChange: () => void,
  ) {}

  connect(_viewer: CollectionViewer): Observable<FlatIpamNode[]> {
    this.treeControl.expansionModel.changed.subscribe((change: SelectionChange<FlatIpamNode>) => {
      if (change.added?.length) {
        change.added.forEach((node) => this.toggleNode(node, true));
      }
      if (change.removed?.length) {
        change.removed
          .slice()
          .reverse()
          .forEach((node) => this.toggleNode(node, false));
      }
    });
    return this.dataChange;
  }

  disconnect(): void {
    this.dataChange.complete();
  }

  async loadRoot(): Promise<void> {
    const vrfId = this.getVrfId();
    if (!vrfId) {
      this.data = [];
      return;
    }
    const children = await this.fetch(vrfId, undefined);
    this.data = children.map((n) => toFlat(n, 0));
  }

  /** Expands down to `id`, fetching each level, so a deep link can reveal
   * and select a node the user never clicked their way to. */
  async revealPath(ancestorIds: string[]): Promise<void> {
    for (const ancestorId of ancestorIds) {
      const node = this.data.find((n) => n.id === ancestorId);
      if (!node || !node.expandable || this.treeControl.isExpanded(node)) continue;
      await this.expandNode(node);
    }
  }

  private toggleNode(node: FlatIpamNode, expand: boolean): void {
    const index = this.data.indexOf(node);
    if (index < 0) return;

    if (!expand) {
      let count = 0;
      for (let i = index + 1; i < this.data.length && this.data[i].level > node.level; i++) {
        count++;
      }
      const next = this.data.slice();
      next.splice(index + 1, count);
      this.data = next;
      return;
    }
    void this.expandNode(node);
  }

  private async expandNode(node: FlatIpamNode): Promise<void> {
    const vrfId = this.getVrfId();
    if (!vrfId) return;

    node.loading = true;
    this.onChange();
    const children = await this.fetch(vrfId, node.id);
    node.loading = false;

    const insertAt = this.data.indexOf(node);
    if (insertAt < 0) return;
    // Already expanded by a concurrent call — don't splice a second copy in.
    if (this.data[insertAt + 1]?.level === node.level + 1) {
      this.onChange();
      return;
    }
    const next = this.data.slice();
    next.splice(insertAt + 1, 0, ...children.map((n) => toFlat(n, node.level + 1)));
    this.data = next;
    if (!this.treeControl.isExpanded(node)) {
      this.treeControl.expansionModel.select(node);
    }
  }

  private fetch(vrfId: string, parentId: string | undefined): Promise<IpamTreeNode[]> {
    return new Promise((resolve) => {
      this.api.ipamTree({ vrf_id: vrfId, parent_id: parentId, depth: 1 }).subscribe({
        // A parent with no children (or a VRF with no top-level prefixes)
        // comes back as JSON `null` (a nil Go slice), not `[]` — normalize
        // here so every caller can treat the result as a plain array.
        next: (nodes) => resolve(nodes ?? []),
        error: () => resolve([]),
      });
    });
  }
}

@Component({
  selector: 'app-ipam-tree',
  standalone: true,
  imports: [
    NgTemplateOutlet,
    FormsModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatProgressBarModule,
    MatSelectModule,
    MatTooltipModule,
    MatTreeModule,
    StatusDotComponent,
    UtilizationBarComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <div class="ipam-tree">
      <div class="controls">
        <mat-form-field appearance="outline" class="vrf-select" subscriptSizing="dynamic">
          <mat-label>VRF</mat-label>
          <mat-select [(ngModel)]="selectedVrfId" (selectionChange)="onVrfChange()">
            @for (vrf of vrfs(); track vrf.id) {
              <mat-option [value]="vrf.id">{{ vrf.name }}</mat-option>
            }
          </mat-select>
        </mat-form-field>

        <mat-form-field appearance="outline" class="filter-field" subscriptSizing="dynamic">
          <mat-icon matPrefix>search</mat-icon>
          <input
            matInput
            placeholder="Search VRF / prefix / scope / pool…"
            [(ngModel)]="filterText"
            (ngModelChange)="onFilterChange($event)"
          />
        </mat-form-field>
      </div>

      <!-- The three prefix types are the whole mental model of this tree and
           were previously explained only in a hover tooltip. -->
      <div class="legend" [class.open]="legendOpen()">
        <button type="button" class="legend-toggle" (click)="toggleLegend()">
          <mat-icon>{{ legendOpen() ? 'expand_more' : 'chevron_right' }}</mat-icon>
          <span>Hierarchy</span>
          <span class="legend-summary">Aggregate → Container → Subnet</span>
        </button>
        @if (legendOpen()) {
          <dl class="legend-body">
            @for (type of prefixTypes; track type) {
              <div class="legend-row">
                <dt><span class="type-chip" [class]="meta(type).chipClass">{{ meta(type).label }}</span></dt>
                <dd>{{ meta(type).help }}</dd>
              </div>
            }
            <div class="legend-row">
              <dt><mat-icon class="scope-icon">dns</mat-icon></dt>
              <dd>A DHCP scope is carved from this subnet — it hands out leases.</dd>
            </div>
          </dl>
        }
      </div>

      @if (searching()) {
        <div class="search-results">
          @if (searchLoading()) {
            <mat-progress-bar mode="indeterminate" />
          }
          @if (searchResults().length === 0 && !searchLoading()) {
            <p class="empty">No matches for "{{ filterText }}"</p>
          }
          @for (result of searchResults(); track result.entity_id) {
            <button type="button" class="search-row" (click)="navigateToPath(result.url)">
              <span class="type">{{ result.type }}</span>
              <span class="title">{{ result.title }}</span>
              @if (result.subtitle) {
                <span class="subtitle">{{ result.subtitle }}</span>
              }
            </button>
          }
        </div>
      } @else {
        <mat-tree [dataSource]="dataSource" [treeControl]="treeControl" class="tree">
          <mat-tree-node
            *matTreeNodeDef="let node"
            matTreeNodeToggle
            class="tree-node leaf"
            [class.selected]="node.id === selectedId()"
            [style.--depth]="node.level"
            (click)="select(node)"
          >
            <span class="indent" [style.width.px]="node.level * 16"></span>
            <span class="spacer"></span>
            <ng-container *ngTemplateOutlet="row; context: { $implicit: node }"></ng-container>
          </mat-tree-node>

          <mat-tree-node
            *matTreeNodeDef="let node; when: isExpandable"
            class="tree-node parent"
            [class.selected]="node.id === selectedId()"
            [style.--depth]="node.level"
          >
            <span class="indent" [style.width.px]="node.level * 16"></span>
            <button
              mat-icon-button
              class="toggle"
              [attr.aria-label]="'Toggle ' + node.name"
              (click)="treeControl.toggle(node)"
            >
              <mat-icon>
                {{ treeControl.isExpanded(node) ? 'expand_more' : 'chevron_right' }}
              </mat-icon>
            </button>
            <span class="node-clickable" (click)="select(node)">
              <ng-container *ngTemplateOutlet="row; context: { $implicit: node }"></ng-container>
            </span>
            @if (node.loading) {
              <mat-progress-bar mode="indeterminate" class="node-progress" />
            }
          </mat-tree-node>
        </mat-tree>

        @if (!selectedVrfId) {
          <p class="empty">Select a VRF to browse its IPAM tree.</p>
        }
      }
    </div>

    <ng-template #row let-node>
      <app-status-dot [status]="node.status" [tooltip]="statusTooltip(node)" />
      <span class="type-chip mini" [class]="meta(node.prefixType).chipClass" [matTooltip]="meta(node.prefixType).help">
        {{ typeInitial(node.prefixType) }}
      </span>
      <span class="node-name">{{ node.name }}</span>
      <span class="node-detail gdhcp-mono">{{ node.cidr }}</span>
      @if (node.vlanVid !== null) {
        <span class="vlan-chip" [matTooltip]="'VLAN ' + node.vlanVid + (node.vlanName ? ' — ' + node.vlanName : '')">
          VLAN {{ node.vlanVid }}
        </span>
      }
      @if (node.hasScope) {
        <mat-icon class="scope-icon" matTooltip="A DHCP scope is carved from this subnet">dns</mat-icon>
      }
      @if (node.barKind !== 'none') {
        <app-utilization-bar
          class="mini-bar"
          [matTooltip]="statusTooltip(node)"
          [used]="node.barPct"
          [total]="100"
          [compact]="true"
        />
      }
    </ng-template>
  `,
  styles: [
    `
      .ipam-tree {
        display: flex;
        flex-direction: column;
        gap: 8px;
        height: 100%;
        overflow: hidden;
        /* The VRF select's floating label sits above its box; without this
         * the pane's overflow:hidden shears the top off it. */
        padding-top: 8px;
      }

      .controls {
        display: flex;
        flex-direction: column;
        gap: 8px;
      }

      .vrf-select,
      .filter-field {
        width: 100%;
      }

      .legend {
        border: 1px solid var(--border-color);
        border-radius: 6px;
        background: var(--bg-secondary);
        flex: 0 0 auto;
      }

      .legend-toggle {
        display: flex;
        align-items: center;
        gap: 4px;
        width: 100%;
        border: none;
        background: none;
        font: inherit;
        color: var(--text-primary);
        padding: 6px 8px;
        cursor: pointer;
        text-align: left;
      }

      .legend-summary {
        margin-left: auto;
        font-size: 11px;
        color: var(--text-secondary);
        white-space: nowrap;
      }

      .legend.open .legend-summary {
        display: none;
      }

      .legend-body {
        margin: 0;
        padding: 0 10px 10px;
        display: flex;
        flex-direction: column;
        gap: 8px;
      }

      .legend-row {
        display: grid;
        grid-template-columns: 84px 1fr;
        gap: 8px;
        align-items: start;
      }

      .legend-row dt {
        margin: 0;
      }

      .legend-row dd {
        margin: 0;
        font-size: 11px;
        line-height: 1.45;
        color: var(--text-secondary);
      }

      .tree {
        overflow-y: auto;
        flex: 1;
      }

      .tree-node {
        display: flex;
        align-items: center;
        gap: 6px;
        min-height: 32px;
        cursor: pointer;
        border-radius: 4px;
        position: relative;
      }

      .tree-node:hover {
        background: var(--hover-bg);
      }

      .tree-node.selected {
        background: var(--active-bg);
      }

      /* Vertical guide per level, so nesting reads without counting pixels. */
      .indent {
        flex: 0 0 auto;
        align-self: stretch;
        background-image: repeating-linear-gradient(
          to right,
          var(--divider-color) 0 1px,
          transparent 1px 16px
        );
        background-position: 7px 0;
      }

      .spacer {
        width: 28px;
        flex: 0 0 auto;
      }

      .toggle {
        width: 28px;
        height: 28px;
        line-height: 28px;
        flex: 0 0 auto;
      }

      .node-clickable {
        display: flex;
        align-items: center;
        gap: 6px;
        flex: 1;
        min-width: 0;
      }

      .node-name {
        font-size: 13px;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }

      .node-detail {
        font-size: 11px;
        color: var(--text-secondary);
        white-space: nowrap;
      }

      .type-chip {
        display: inline-block;
        font-size: 11px;
        font-weight: 600;
        letter-spacing: 0.02em;
        text-transform: uppercase;
        padding: 2px 8px;
        border-radius: 10px;
        cursor: help;
      }

      /* One letter (A/C/S) in the tree — the full word is in the legend and
         the tooltip, and horizontal room is scarce here. */
      .type-chip.mini {
        flex: 0 0 auto;
        width: 16px;
        padding: 1px 0;
        text-align: center;
        font-size: 9px;
        line-height: 14px;
        border-radius: 3px;
      }

      .chip-aggregate {
        background: var(--bg-highlight);
        color: var(--primary-color);
      }

      .chip-container {
        background: color-mix(in srgb, var(--accent-color) 20%, transparent);
        color: var(--accent-color);
      }

      .chip-subnet {
        background: color-mix(in srgb, var(--status-ok) 20%, transparent);
        color: var(--status-ok);
      }

      .vlan-chip {
        flex: 0 0 auto;
        font-size: 10px;
        padding: 1px 5px;
        border-radius: 3px;
        border: 1px solid var(--border-color);
        color: var(--text-secondary);
        white-space: nowrap;
        cursor: help;
      }

      .scope-icon {
        flex: 0 0 auto;
        width: 16px;
        height: 16px;
        font-size: 16px;
        color: var(--text-secondary);
      }

      .mini-bar {
        margin-left: auto;
        width: 44px;
        flex: 0 0 auto;
      }

      .node-progress {
        position: absolute;
        bottom: 0;
        left: 0;
        width: 100%;
      }

      .search-results {
        overflow-y: auto;
        flex: 1;
        display: flex;
        flex-direction: column;
      }

      .search-row {
        display: flex;
        flex-direction: column;
        align-items: flex-start;
        text-align: left;
        gap: 2px;
        padding: 8px;
        border: none;
        background: none;
        cursor: pointer;
        border-radius: 4px;
        font: inherit;
        color: var(--text-primary);
      }

      .search-row:hover {
        background: var(--hover-bg);
      }

      .search-row .type {
        font-size: 10px;
        text-transform: uppercase;
        letter-spacing: 0.04em;
        color: var(--text-secondary);
      }

      .search-row .title {
        font-size: 13px;
      }

      .search-row .subtitle {
        font-size: 11px;
        color: var(--text-secondary);
      }

      .empty {
        color: var(--text-secondary);
        font-size: 12px;
        padding: 16px 8px;
        text-align: center;
      }
    `,
  ],
})
export class IpamTreeComponent {
  private readonly api = inject(ApiService);
  private readonly router = inject(Router);
  private readonly destroyRef = inject(DestroyRef);
  private readonly cdr = inject(ChangeDetectorRef);

  /** Highlights the node the surrounding page is showing. */
  readonly selectedId = input<string | null>(null);
  /** Emitted instead of navigating when the host page drives routing. */
  readonly nodeSelected = output<string>();
  /** When false the component routes on click itself (standalone use). */
  readonly emitOnly = input(false);

  readonly vrfs = signal<VRF[]>([]);
  selectedVrfId: string | null = null;
  filterText = '';

  readonly searching = signal(false);
  readonly searchLoading = signal(false);
  readonly searchResults = signal<SearchResult[]>([]);
  readonly legendOpen = signal(false);

  readonly prefixTypes = PREFIX_TYPE_ORDER;

  readonly treeControl = new FlatTreeControl<FlatIpamNode>(
    (node) => node.level,
    (node) => node.expandable,
  );
  readonly dataSource = new IpamTreeDataSource(this.treeControl, this.api, () => this.selectedVrfId, () =>
    this.cdr.markForCheck(),
  );

  private readonly filter$ = new Subject<string>();

  constructor() {
    this.api.listVRFs({ limit: 200 }).subscribe((res) => {
      this.vrfs.set(res.items);
      const remembered = res.items.find((v) => v.id === readStoredVrf());
      const defaultVrf = remembered ?? res.items.find((v) => v.is_default) ?? res.items[0];
      if (defaultVrf) {
        this.selectedVrfId = defaultVrf.id;
        void this.dataSource.loadRoot().then(() => this.revealSelected());
      }
    });

    // Reveal a node the page navigated to directly (deep link, or a click
    // through from somewhere outside the tree).
    effect(() => {
      const id = this.selectedId();
      if (id) void this.revealSelected();
    });

    this.filter$
      .pipe(
        debounceTime(250),
        distinctUntilChanged(),
        switchMap((query) => {
          if (!query.trim()) {
            this.searching.set(false);
            return [];
          }
          this.searching.set(true);
          this.searchLoading.set(true);
          return this.api.search(query.trim(), undefined, 50);
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((results) => {
        this.searchLoading.set(false);
        this.searchResults.set(results);
      });
  }

  isExpandable = (_index: number, node: FlatIpamNode): boolean => node.expandable;

  meta(type: PrefixType) {
    return PREFIX_TYPE_META[type];
  }

  typeInitial(type: PrefixType): string {
    return PREFIX_TYPE_META[type].label.charAt(0);
  }

  statusTooltip(node: FlatIpamNode): string {
    const pct = node.barPct.toFixed(1);
    switch (node.barKind) {
      case 'allocated':
        return `${pct}% of this block's address space is carved into child prefixes`;
      case 'dhcp':
        return `${pct}% of this subnet's DHCP addresses are in use`;
      default:
        return 'No DHCP scope on this subnet yet';
    }
  }

  toggleLegend(): void {
    this.legendOpen.update((open) => !open);
  }

  onVrfChange(): void {
    this.treeControl.collapseAll();
    if (this.selectedVrfId) storeVrf(this.selectedVrfId);
    void this.dataSource.loadRoot();
  }

  onFilterChange(value: string): void {
    this.filter$.next(value);
  }

  /**
   * A node with a scope opens its own prefix page, which links on to the
   * scope. It used to jump straight to `/dhcp/scopes/:id`, which threw the
   * user out of IPAM entirely on a click inside the IPAM tree.
   */
  select(node: FlatIpamNode): void {
    if (this.emitOnly()) {
      this.nodeSelected.emit(node.id);
      return;
    }
    void this.router.navigate(['/ipam', 'prefixes', node.id]);
  }

  navigateToPath(path: string): void {
    void this.router.navigateByUrl(path);
  }

  /** Makes the selected prefix visible: switches to its VRF if the tree is
   * showing a different one, then walks its ancestry expanding each level,
   * so a deep link or a cross-link from the DHCP section lands on a node the
   * user can actually see in context. */
  private async revealSelected(): Promise<void> {
    const id = this.selectedId();
    if (!id) return;
    if (this.dataSource.data.some((n) => n.id === id)) return;

    const prefix = await new Promise<{ vrf_id: string } | null>((resolve) => {
      this.api.getPrefix(id).subscribe({ next: resolve, error: () => resolve(null) });
    });
    if (!prefix) return;

    if (prefix.vrf_id !== this.selectedVrfId) {
      this.selectedVrfId = prefix.vrf_id;
      storeVrf(prefix.vrf_id);
      this.treeControl.collapseAll();
      await this.dataSource.loadRoot();
      if (this.dataSource.data.some((n) => n.id === id)) {
        this.cdr.markForCheck();
        return;
      }
    }

    const ancestors = await new Promise<string[]>((resolve) => {
      this.api.prefixAncestors(id).subscribe({
        next: (items) => resolve(items.map((p) => p.id)),
        error: () => resolve([]),
      });
    });
    if (ancestors.length) await this.dataSource.revealPath(ancestors);
    this.cdr.markForCheck();
  }
}

function readStoredVrf(): string | null {
  try {
    return localStorage.getItem(VRF_STORAGE_KEY);
  } catch {
    return null;
  }
}

function storeVrf(id: string): void {
  try {
    localStorage.setItem(VRF_STORAGE_KEY, id);
  } catch {
    // non-fatal
  }
}
