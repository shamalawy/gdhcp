# gdhcp Frontend Design

**Status**: Design Phase  
**Stack**: Angular v18+ (standalone components, signals), TypeScript, Angular Material  
**Target**: Static dist embedded in Go binary via go:embed

---

## Table of Contents

1. [Overview](#overview)
2. [Technology Stack](#technology-stack)
3. [Route Map](#route-map)
4. [Component Tree](#component-tree)
5. [Folder Structure](#folder-structure)
6. [State Management](#state-management)
7. [IPAM Tree Navigation](#ipam-tree-navigation)
8. [Reusable Components](#reusable-components)
9. [Visual Language](#visual-language)
10. [Key Features & Flows](#key-features--flows)
11. [API Integration](#api-integration)

---

## Overview

The gdhcp frontend is a single-page application that drives all backend features through an intuitive, tree-based navigation centered on IPAM. The UI must be:

- **Simplistic**: Minimal visual complexity, focus on clarity
- **Complete**: Every backend feature accessible without drilling through dialogs
- **Adaptive**: Views adapt based on tree position (global / subnet / pool level)
- **Descriptive**: Every DHCP option, template, entity carries documentation inline

### Key Design Principles

- **IPAM as the spine**: Left-panel tree is the primary navigation model (VRF → Aggregate → Subnet → Scope → Pool)
- **Lazy-loaded trees**: Expand on demand; searchable at any level
- **Contextual details**: Right panels / detail views sized for the entity in focus
- **Embedded documentation**: DHCP option descriptions fetched from API; tooltips on all controls
- **Status at a glance**: Color-coded utilization bars and status dots on all entity rows

---

## Technology Stack

### Frontend

- **Framework**: Angular v18+ (standalone components, signals API)
- **Component Library**: Angular Material v18+
  - Provides: buttons, inputs, dropdowns, dialogs, tables, tree, stepper, chips, tabs, cards
  - Dark/light theme support built-in
  - Accessibility (a11y) standards met
- **Styling**: SCSS with Material tokens
- **State**: Signals + service layer (no NgRx; complexity not warranted)
- **HTTP**: Angular HttpClient with interceptors for auth/errors
- **Forms**: Reactive Forms (FormControl, FormGroup)
- **Routing**: Angular Router (lazy-loaded feature modules)

### Charts & Visualization

- **Utilization bars**: Custom SVG + CSS (no library needed)
- **Gauges**: CSS-based radial progress or custom SVG (or lightweight `ngx-gauge` if UI demands it)
- **History graphs**: Optional `ngx-charts` (lightweight, Angular-native) if dashboard shows historical trends
- **Decision**: Start custom; add library only if dashboard requires complex interactions

### Build & Deployment

- **Build tool**: Angular CLI with Esbuild (default)
- **Output**: `dist/web` compiled as static files
- **Embedding**: Go's `go:embed` reads `//go:embed web/dist` to embed into binary
- **Served**: Static file handler in Go backend at `/` (SPA routing handled by 404 → index.html)

---

## Route Map

### Root Routes

```
/
├── /login                        (PublicComponent)
├── /dashboard                    (DashboardComponent)
├── /ipam                         (LayoutComponent with sidebar)
│   ├── /ipam/vrf/:vrfId          (VrfDetailComponent)
│   ├── /ipam/aggregate/:aggId    (AggregateDetailComponent)
│   ├── /ipam/subnet/:subnetId    (SubnetDetailComponent)
│   ├── /ipam/scope/:scopeId      (ScopeDetailComponent)
│   └── /ipam/pool/:poolId        (PoolDetailComponent)
├── /templates                    (TemplatesComponent)
│   └── /templates/:id            (TemplateDetailComponent)
├── /settings                     (SettingsComponent)
│   ├── /settings/general         (SettingsGeneralComponent)
│   ├── /settings/options         (SettingsOptionsComponent, DHCP option catalog)
│   └── /settings/cluster         (SettingsClusterComponent, HA config)
├── /search-results/:query        (SearchResultsComponent)
└── /404                          (NotFoundComponent)
```

### API Endpoint Patterns (assumed, based on typical DHCP/IPAM APIs)

```
GET /api/v1/vrf
GET /api/v1/vrf/:vrfId
GET /api/v1/vrf/:vrfId/aggregates
GET /api/v1/aggregate/:aggId
GET /api/v1/aggregate/:aggId/subnets
GET /api/v1/subnet/:subnetId
GET /api/v1/subnet/:subnetId/scopes
GET /api/v1/scope/:scopeId
GET /api/v1/scope/:scopeId/pools
GET /api/v1/pool/:poolId
GET /api/v1/pool/:poolId/reservations
GET /api/v1/pool/:poolId/available-ips
GET /api/v1/templates
GET /api/v1/templates/:id
GET /api/v1/options/catalog       (DHCP option definitions with descriptions)
GET /api/v1/search?q=<query>      (Global search)
GET /api/v1/utilization/summary   (Overall utilization stats)
GET /api/v1/utilization/:entityId (Entity-specific utilization)

POST/PUT/DELETE endpoints follow RESTful patterns.
```

---

## Component Tree

### Root Level

```
AppComponent
├── AuthGuard (interceptor + route guard)
├── HttpErrorInterceptor (401/403/5xx → toast/modal)
├── GlobalSearchComponent (always visible; Cmd+K shortcut)
├── LayoutComponent (main app shell; only shown when authenticated)
│   ├── SidebarComponent (IPAM tree + search)
│   ├── TopNavComponent (breadcrumb, user menu, theme toggle)
│   └── RouterOutlet (feature components)
└── PublicComponent (login page)
```

### Feature Components

#### Dashboard (`/dashboard`)
```
DashboardComponent
├── UtilizationSummaryComponent
│   ├── UtilizationBarComponent (custom SVG; global totals)
│   └── StatusDotsComponent (overall health)
├── TopUtilizedPoolsComponent (list with inline bars)
├── PoolsNearCapacityComponent (list with warning indicators)
├── RecentActivityComponent (audit log / event stream)
└── QuickActionsComponent (buttons: new pool, reserve IP, etc.)
```

#### IPAM & Entity Details
```
VrfDetailComponent
├── EntityHeaderComponent (name, description, tags/labels, edit button)
├── UtilizationSummaryComponent (VRF-level rollup of all children)
├── AggregateListComponent (table with edit/delete actions)
├── UtilizationByAggregateComponent (bar chart: each aggregate's usage)
└── TemplateApplicationComponent (attach template picker)

AggregateDetailComponent
├── EntityHeaderComponent
├── UtilizationSummaryComponent (rollup of child subnets)
├── SubnetListComponent (table: prefixes, utilization bars, drill-down)
└── TemplateApplicationComponent

SubnetDetailComponent
├── EntityHeaderComponent
├── UtilizationSummaryComponent (rollup of child scopes)
├── ScopeListComponent (table: scope names, status dots, utilization)
└── ActionsComponent (create scope, etc.)

ScopeDetailComponent
├── EntityHeaderComponent
├── DHCPOptionsComponent (table of inherited + scope-specific options, each with description)
├── PoolListComponent (table with utilization bars, status, actions)
├── TemplateApplicationComponent (attach template picker)
└── ScopeEditFormComponent (inline or modal)

PoolDetailComponent
├── EntityHeaderComponent
├── UtilizationDetailsComponent (detailed breakdown: used, available, reserved)
│   ├── UtilizationBarComponent (visual bar with segments)
│   └── IpRangeBreakdownComponent (color-coded ranges)
├── ReservationListComponent (table: IP, MAC, hostname, description, actions)
├── ReservationFormComponent (create/edit reservations inline or in modal)
├── DHCPOptionsComponent (pool-specific options with descriptions)
├── TemplateApplicationComponent
└── AvailableIpsComponent (paginated list or "next available" helper)
```

#### Templates (`/templates`)
```
TemplatesComponent
├── TemplateListComponent (table: name, description, applied-to-count, actions)
├── TemplateDetailComponent
│   ├── EntityHeaderComponent
│   ├── DHCPOptionsEditorComponent (add/remove/edit options with descriptions)
│   ├── TagEditorComponent
│   └── UsageListComponent (shows which scopes/pools use this template)
└── TemplateFormComponent (create/edit modal or side panel)
```

#### Settings
```
SettingsComponent (tabbed)
├── SettingsGeneralComponent (server name, logging, branding)
├── SettingsOptionsComponent (DHCP option catalog viewer; descriptions, types, defaults)
└── SettingsClusterComponent (HA config: cluster name, VIP, server roles, heartbeat settings)
```

#### Search
```
SearchResultsComponent
├── ResultsCategorizedComponent (groups results by type: VRF, Subnet, Pool, Template, etc.)
├── ResultPreviewComponent (snippet + inline action buttons)
└── SearchFiltersComponent (filter by entity type, utilization, etc.)
```

### Reusable/Shared Components

```
SharedModule
├── UtilizationBarComponent
├── UtilizationGaugeComponent (optional; for pool details)
├── StatusDotComponent
├── DescriptionTooltipComponent (displays DHCP option description)
├── TagChipsComponent (display + edit tags)
├── ConfirmDialogComponent
├── ToastService (notifications)
├── EntityHeaderComponent (reusable for all detail views)
├── DHCPOptionsEditorComponent (reusable for scopes/pools/templates)
├── DHCPOptionDescriptionComponent (displays option metadata + description)
└── SearchHighlightPipe (highlights search terms in results)
```

---

## Folder Structure

```
web/
├── src/
│   ├── app/
│   │   ├── app.config.ts              (Angular config, providers)
│   │   ├── app.component.ts           (root component)
│   │   ├── app.routes.ts              (route definitions)
│   │   │
│   │   ├── core/
│   │   │   ├── auth/
│   │   │   │   ├── auth.service.ts    (login, logout, token management)
│   │   │   │   ├── auth.guard.ts      (route protection)
│   │   │   │   └── auth.interceptor.ts (inject auth token, handle 401)
│   │   │   ├── http/
│   │   │   │   ├── api.service.ts     (base HTTP client with default headers)
│   │   │   │   └── error.interceptor.ts (handle 5xx, show toast)
│   │   │   ├── search/
│   │   │   │   └── search.service.ts  (global search API calls)
│   │   │   └── models/
│   │   │       ├── vrf.ts             (VRF type definition)
│   │   │       ├── aggregate.ts       (Aggregate type)
│   │   │       ├── subnet.ts
│   │   │       ├── scope.ts
│   │   │       ├── pool.ts
│   │   │       ├── reservation.ts
│   │   │       ├── template.ts
│   │   │       ├── dhcp-option.ts     (DHCP option with metadata)
│   │   │       └── utilization.ts     (utilization stats structure)
│   │   │
│   │   ├── shared/
│   │   │   ├── components/
│   │   │   │   ├── utilization-bar/
│   │   │   │   │   ├── utilization-bar.component.ts
│   │   │   │   │   ├── utilization-bar.component.scss
│   │   │   │   │   └── utilization-bar.component.spec.ts
│   │   │   │   ├── utilization-gauge/
│   │   │   │   │   ├── utilization-gauge.component.ts
│   │   │   │   │   ├── utilization-gauge.component.scss
│   │   │   │   │   └── utilization-gauge.component.spec.ts
│   │   │   │   ├── status-dot/
│   │   │   │   │   ├── status-dot.component.ts
│   │   │   │   │   ├── status-dot.component.scss
│   │   │   │   │   └── status-dot.component.spec.ts
│   │   │   │   ├── description-tooltip/
│   │   │   │   │   ├── description-tooltip.component.ts
│   │   │   │   │   └── description-tooltip.component.scss
│   │   │   │   ├── tag-chips/
│   │   │   │   │   ├── tag-chips.component.ts
│   │   │   │   │   └── tag-chips.component.scss
│   │   │   │   ├── entity-header/
│   │   │   │   │   ├── entity-header.component.ts
│   │   │   │   │   ├── entity-header.component.scss
│   │   │   │   │   └── entity-header.component.spec.ts
│   │   │   │   ├── dhcp-options-editor/
│   │   │   │   │   ├── dhcp-options-editor.component.ts
│   │   │   │   │   ├── dhcp-options-editor.component.scss
│   │   │   │   │   └── dhcp-options-editor.component.spec.ts
│   │   │   │   ├── dhcp-option-description/
│   │   │   │   │   ├── dhcp-option-description.component.ts
│   │   │   │   │   └── dhcp-option-description.component.scss
│   │   │   │   ├── confirm-dialog/
│   │   │   │   │   ├── confirm-dialog.component.ts
│   │   │   │   │   └── confirm-dialog.component.spec.ts
│   │   │   │   └── global-search/
│   │   │   │       ├── global-search.component.ts
│   │   │   │       └── global-search.component.scss
│   │   │   ├── services/
│   │   │   │   ├── toast.service.ts   (show notifications)
│   │   │   │   ├── dialog.service.ts  (wrapper around MatDialog)
│   │   │   │   └── entity-edit.service.ts (common edit/save logic)
│   │   │   └── pipes/
│   │   │       ├── search-highlight.pipe.ts
│   │   │       ├── bytes.pipe.ts      (format IP counts as human-readable)
│   │   │       └── percent.pipe.ts    (format percentages)
│   │   │
│   │   ├── layout/
│   │   │   ├── layout.component.ts    (main app shell)
│   │   │   ├── layout.component.scss
│   │   │   ├── sidebar/
│   │   │   │   ├── sidebar.component.ts (IPAM tree + search)
│   │   │   │   ├── sidebar.component.scss
│   │   │   │   ├── tree-node.component.ts (single tree node)
│   │   │   │   └── tree-node.component.scss
│   │   │   └── topnav/
│   │   │       ├── topnav.component.ts
│   │   │       └── topnav.component.scss
│   │   │
│   │   ├── features/
│   │   │   ├── login/
│   │   │   │   ├── login.component.ts
│   │   │   │   ├── login.component.scss
│   │   │   │   └── login.component.html
│   │   │   ├── dashboard/
│   │   │   │   ├── dashboard.component.ts
│   │   │   │   ├── dashboard.component.scss
│   │   │   │   ├── utilization-summary/
│   │   │   │   │   ├── utilization-summary.component.ts
│   │   │   │   │   └── utilization-summary.component.scss
│   │   │   │   ├── top-utilized-pools/
│   │   │   │   │   ├── top-utilized-pools.component.ts
│   │   │   │   │   └── top-utilized-pools.component.scss
│   │   │   │   └── recent-activity/
│   │   │   │       ├── recent-activity.component.ts
│   │   │   │       └── recent-activity.component.scss
│   │   │   ├── ipam/
│   │   │   │   ├── ipam.routes.ts     (lazy-loaded routes for IPAM views)
│   │   │   │   ├── vrf/
│   │   │   │   │   ├── vrf-detail.component.ts
│   │   │   │   │   ├── vrf-detail.component.scss
│   │   │   │   │   ├── vrf-form.component.ts
│   │   │   │   │   └── aggregate-list.component.ts
│   │   │   │   ├── aggregate/
│   │   │   │   │   ├── aggregate-detail.component.ts
│   │   │   │   │   ├── aggregate-detail.component.scss
│   │   │   │   │   ├── aggregate-form.component.ts
│   │   │   │   │   └── subnet-list.component.ts
│   │   │   │   ├── subnet/
│   │   │   │   │   ├── subnet-detail.component.ts
│   │   │   │   │   ├── subnet-detail.component.scss
│   │   │   │   │   ├── subnet-form.component.ts
│   │   │   │   │   └── scope-list.component.ts
│   │   │   │   ├── scope/
│   │   │   │   │   ├── scope-detail.component.ts
│   │   │   │   │   ├── scope-detail.component.scss
│   │   │   │   │   ├── scope-form.component.ts
│   │   │   │   │   ├── scope-edit-inline.component.ts
│   │   │   │   │   ├── pool-list.component.ts
│   │   │   │   │   └── dhcp-options-list.component.ts
│   │   │   │   └── pool/
│   │   │   │       ├── pool-detail.component.ts
│   │   │   │       ├── pool-detail.component.scss
│   │   │   │       ├── pool-form.component.ts
│   │   │   │       ├── pool-utilization.component.ts
│   │   │   │       ├── reservation-list.component.ts
│   │   │   │       ├── reservation-form.component.ts
│   │   │   │       ├── available-ips.component.ts
│   │   │   │       └── pool-options.component.ts
│   │   │   ├── templates/
│   │   │   │   ├── templates.component.ts
│   │   │   │   ├── templates.component.scss
│   │   │   │   ├── template-list.component.ts
│   │   │   │   ├── template-detail.component.ts
│   │   │   │   ├── template-form.component.ts
│   │   │   │   ├── template-usage.component.ts
│   │   │   │   └── attach-template-picker.component.ts
│   │   │   ├── settings/
│   │   │   │   ├── settings.component.ts
│   │   │   │   ├── settings.component.scss
│   │   │   │   ├── settings-general.component.ts
│   │   │   │   ├── settings-options.component.ts (option catalog viewer)
│   │   │   │   └── settings-cluster.component.ts
│   │   │   └── search/
│   │   │       ├── search-results.component.ts
│   │   │       ├── search-results.component.scss
│   │   │       ├── result-card.component.ts
│   │   │       └── search-filters.component.ts
│   │   │
│   │   └── styles/
│   │       ├── styles.scss           (global styles)
│   │       ├── theme.scss            (material theme, colors)
│   │       ├── colors.scss           (utilization thresholds, status colors)
│   │       ├── typography.scss       (font definitions)
│   │       └── layout.scss           (grid, spacing utilities)
│   │
│   ├── index.html                    (Angular entry point)
│   ├── main.ts                       (bootstrap Angular)
│   ├── polyfills.ts                  (if needed)
│   └── environment/
│       ├── environment.ts            (dev)
│       └── environment.prod.ts       (production)
│
├── angular.json                      (CLI config)
├── tsconfig.json
├── tsconfig.app.json
├── tsconfig.spec.json
├── package.json
└── README.md                         (dev setup instructions)
```

---

## State Management

### Strategy: Signals + Services (No NgRx)

Use Angular's Signal API (v16+) for reactive state. Services maintain signals and expose methods to update them.

#### Example Service Pattern

```typescript
// core/services/vrf.service.ts
import { Injectable, signal, computed } from '@angular/core';
import { HttpClient } from '@angular/common/http';

@Injectable({ providedIn: 'root' })
export class VrfService {
  private vrfList = signal<Vrf[]>([]);
  private selectedVrfId = signal<string | null>(null);
  private isLoading = signal(false);
  private error = signal<string | null>(null);

  readonly vrfs = this.vrfList.asReadonly();
  readonly selectedVrf = computed(() => {
    const id = this.selectedVrfId();
    return this.vrfList().find(v => v.id === id) || null;
  });
  readonly isLoading$ = this.isLoading.asReadonly();
  readonly error$ = this.error.asReadonly();

  constructor(private http: HttpClient) {
    this.loadVrfs();
  }

  loadVrfs() {
    this.isLoading.set(true);
    this.error.set(null);
    this.http.get<Vrf[]>('/api/v1/vrf').subscribe({
      next: (data) => {
        this.vrfList.set(data);
        this.isLoading.set(false);
      },
      error: (err) => {
        this.error.set(err.message);
        this.isLoading.set(false);
      }
    });
  }

  selectVrf(id: string) {
    this.selectedVrfId.set(id);
  }

  createVrf(vrf: Vrf) {
    return this.http.post<Vrf>('/api/v1/vrf', vrf).pipe(
      tap((newVrf) => {
        this.vrfList.update(list => [...list, newVrf]);
      })
    );
  }

  updateVrf(id: string, vrf: Partial<Vrf>) {
    return this.http.put<Vrf>(`/api/v1/vrf/${id}`, vrf).pipe(
      tap((updated) => {
        this.vrfList.update(list =>
          list.map(v => v.id === id ? updated : v)
        );
      })
    );
  }

  deleteVrf(id: string) {
    return this.http.delete(`/api/v1/vrf/${id}`).pipe(
      tap(() => {
        this.vrfList.update(list => list.filter(v => v.id !== id));
      })
    );
  }
}
```

#### Usage in Component

```typescript
// features/ipam/vrf/vrf-detail.component.ts
import { Component, inject, computed } from '@angular/core';
import { VrfService } from '../../../core/services/vrf.service';

@Component({
  selector: 'app-vrf-detail',
  standalone: true,
  template: `
    @if (vrf(); as vrf) {
      <div>{{ vrf.name }}</div>
    } @else if (isLoading()) {
      <p>Loading...</p>
    }
  `
})
export class VrfDetailComponent {
  private vrfService = inject(VrfService);
  
  vrf = this.vrfService.selectedVrf;
  isLoading = this.vrfService.isLoading$;
}
```

#### Justification

- **Signals**: Built-in, performant, zero-config reactivity
- **Services**: Single source of truth; HTTP calls centralized
- **No NgRx**: Extra boilerplate (reducers, actions, effects) not justified for this domain; signals are simpler
- **Computed**: Derived state (e.g., selected entity) recomputed automatically
- **Unsubscribe**: Services stay alive for app lifetime; no memory leaks

---

## IPAM Tree Navigation

### Sidebar Component Design

The sidebar displays a hierarchical, searchable tree. Nodes are lazy-loaded.

```typescript
// layout/sidebar/sidebar.component.ts
@Component({
  selector: 'app-sidebar',
  standalone: true,
  imports: [CommonModule, MatTreeModule, MatInputModule, MatIconModule],
  template: `
    <div class="sidebar">
      <mat-form-field>
        <input matInput placeholder="Search VRF/Subnet/Pool..." 
               (input)="searchTree($event)" />
        <mat-icon matSuffix>search</mat-icon>
      </mat-form-field>
      
      <mat-tree [dataSource]="dataSource" [treeControl]="treeControl">
        <!-- VRF level -->
        <mat-tree-node *matTreeNodeDef="let node" 
                       [ngClass]="{'selected': isSelected(node)}"
                       (click)="selectNode(node)">
          <span class="node-label">
            <mat-icon class="status-dot" [ngClass]="getStatus(node)"></mat-icon>
            {{ node.name }}
            <span class="utilization" *ngIf="node.utilization">
              {{ node.utilization }}%
            </span>
          </span>
        </mat-tree-node>

        <!-- Parent node (expandable) -->
        <mat-tree-node *matTreeNodeDef="let node; when: hasChildren"
                       [ngClass]="{'selected': isSelected(node)}"
                       (click)="selectNode(node)">
          <button mat-icon-button 
                  (click)="toggleExpand(node); $event.stopPropagation()">
            <mat-icon class="expand-icon">
              {{ treeControl.isExpanded(node) ? 'expand_more' : 'expand_less' }}
            </mat-icon>
          </button>
          <span class="node-label">
            <span class="status-dot" [ngClass]="getStatus(node)"></span>
            {{ node.name }}
            <span class="utilization" *ngIf="node.utilization">
              {{ node.utilization }}%
            </span>
          </span>
        </mat-tree-node>
      </mat-tree>
    </div>
  `,
  styles: [`
    .sidebar {
      width: 300px;
      height: 100vh;
      overflow-y: auto;
      padding: 16px;
      background: var(--bg-sidebar);
      border-right: 1px solid var(--border-color);
    }

    .node-label {
      display: flex;
      align-items: center;
      gap: 8px;
      font-size: 14px;
    }

    .status-dot {
      width: 8px;
      height: 8px;
      border-radius: 50%;
      display: inline-block;
    }

    .status-dot.ok { background: var(--status-ok); }
    .status-dot.warn { background: var(--status-warn); }
    .status-dot.error { background: var(--status-error); }

    .utilization {
      margin-left: auto;
      font-size: 12px;
      color: var(--text-secondary);
    }

    mat-tree-node.selected {
      background: var(--highlight-bg);
      font-weight: 500;
    }
  `]
})
export class SidebarComponent {
  treeControl = new FlatTreeControl<TreeNode>(
    node => node.level,
    node => node.expandable
  );
  dataSource: MatTreeFlatDataSource<TreeNode, TreeNode>;
  searchTerm = signal('');

  ngOnInit() {
    this.loadTree();
  }

  loadTree() {
    // Fetch VRFs; populate tree lazily
    this.ipamService.getVrfs().subscribe(vrfs => {
      // Map to tree nodes
    });
  }

  toggleExpand(node: TreeNode) {
    if (!node.children.length && node.expandable) {
      // Lazy-load children
      this.ipamService.getChildren(node.id).subscribe(children => {
        node.children = children;
        this.treeControl.expand(node);
      });
    } else {
      this.treeControl.toggle(node);
    }
  }

  searchTree(event: Event) {
    const term = (event.target as HTMLInputElement).value;
    this.searchTerm.set(term);
    // Filter tree nodes, or API call for search results
  }

  selectNode(node: TreeNode) {
    this.router.navigate([`/ipam/${node.type}/${node.id}`]);
  }
}
```

### Tree Node Structure

```typescript
interface TreeNode {
  id: string;
  name: string;
  type: 'vrf' | 'aggregate' | 'subnet' | 'scope' | 'pool';
  level: number;
  expandable: boolean;
  children: TreeNode[];
  utilization?: number;        // percentage
  status: 'ok' | 'warn' | 'error';
  description?: string;
}
```

---

## Reusable Components

### 1. UtilizationBarComponent

Displays a horizontal bar showing utilization as a percentage with color-coded segments.

```typescript
// shared/components/utilization-bar/utilization-bar.component.ts
@Component({
  selector: 'app-utilization-bar',
  standalone: true,
  template: `
    <div class="utilization-bar" [style.--total]="total">
      <svg [attr.width]="width" [attr.height]="height" class="bar-svg">
        <!-- Used (filled) segment -->
        <rect x="0" y="0" [attr.width]="(used / total) * width" [attr.height]="height"
              class="segment used" />
        <!-- Reserved (overlay) segment -->
        <rect [attr.x]="(used / total) * width" y="0" 
              [attr.width]="(reserved / total) * width" [attr.height]="height"
              class="segment reserved" />
        <!-- Available (light) segment -->
        <rect [attr.x]="((used + reserved) / total) * width" y="0" 
              [attr.width]="(available / total) * width" [attr.height]="height"
              class="segment available" />
      </svg>
      <span class="label">{{ used }} / {{ total }} ({{ (used / total * 100).toFixed(1) }}%)</span>
    </div>
  `,
  styles: [`
    .utilization-bar {
      display: flex;
      align-items: center;
      gap: 8px;
    }

    .bar-svg {
      border-radius: 4px;
      border: 1px solid var(--border-color);
    }

    .segment {
      transition: opacity 0.2s;
    }

    .segment.used { fill: var(--util-used); }       /* 70% threshold color (orange/red) */
    .segment.reserved { fill: var(--util-reserved); } /* reserved/pending (yellow) */
    .segment.available { fill: var(--util-available); } /* free space (green) */

    .label {
      font-size: 12px;
      white-space: nowrap;
      color: var(--text-secondary);
    }
  `]
})
export class UtilizationBarComponent {
  @Input() used!: number;
  @Input() reserved!: number;
  @Input() available!: number;
  @Input() width = 160;
  @Input() height = 20;

  total = computed(() => this.used + this.reserved + this.available);
}
```

### 2. StatusDotComponent

Small colored indicator showing entity health.

```typescript
// shared/components/status-dot/status-dot.component.ts
@Component({
  selector: 'app-status-dot',
  standalone: true,
  template: `
    <span class="status-dot" [ngClass]="status" [title]="tooltip"></span>
  `,
  styles: [`
    .status-dot {
      display: inline-block;
      width: 8px;
      height: 8px;
      border-radius: 50%;
      flex-shrink: 0;
    }

    .status-dot.ok { background: var(--status-ok); }
    .status-dot.warn { background: var(--status-warn); }
    .status-dot.error { background: var(--status-error); }
  `]
})
export class StatusDotComponent {
  @Input() status: 'ok' | 'warn' | 'error' = 'ok';
  @Input() tooltip = '';
}
```

### 3. DescriptionTooltipComponent

Displays DHCP option metadata in a Material tooltip.

```typescript
// shared/components/description-tooltip/description-tooltip.component.ts
@Component({
  selector: 'app-description-tooltip',
  standalone: true,
  imports: [MatTooltipModule],
  template: `
    <span [matTooltip]="tooltipContent" matTooltipPosition="above">
      <ng-content></ng-content>
      <mat-icon class="info-icon" *ngIf="showIcon">info</mat-icon>
    </span>
  `,
  styles: [`
    .info-icon {
      font-size: 14px;
      width: 14px;
      height: 14px;
      margin-left: 4px;
      opacity: 0.6;
    }
  `]
})
export class DescriptionTooltipComponent {
  @Input() tooltipContent!: string;
  @Input() showIcon = true;
}
```

### 4. DHCPOptionsEditorComponent

Reusable form for adding/editing DHCP options on scopes, pools, and templates.

```typescript
// shared/components/dhcp-options-editor/dhcp-options-editor.component.ts
@Component({
  selector: 'app-dhcp-options-editor',
  standalone: true,
  imports: [CommonModule, ReactiveFormsModule, MatFormFieldModule, MatSelectModule, ...],
  template: `
    <div class="options-editor">
      <div *ngFor="let option of options; let i = index" class="option-row">
        <mat-form-field>
          <mat-label>Option Name</mat-label>
          <mat-select [formControl]="getOptionControl(i)">
            <mat-option *ngFor="let opt of optionCatalog" [value]="opt.code">
              {{ opt.name }} ({{ opt.code }})
            </mat-option>
          </mat-select>
        </mat-form-field>

        <mat-form-field>
          <mat-label>Value</mat-label>
          <input matInput [formControl]="getValueControl(i)" />
        </mat-form-field>

        <app-description-tooltip [tooltipContent]="getOptionDescription(i)">
          <mat-icon class="info-icon">info</mat-icon>
        </app-description-tooltip>

        <button mat-icon-button (click)="removeOption(i)">
          <mat-icon>delete</mat-icon>
        </button>
      </div>

      <button mat-stroked-button (click)="addOption()">
        <mat-icon>add</mat-icon> Add Option
      </button>
    </div>
  `,
  styles: [`
    .options-editor {
      display: flex;
      flex-direction: column;
      gap: 12px;
    }

    .option-row {
      display: flex;
      gap: 12px;
      align-items: flex-end;
    }

    mat-form-field {
      flex: 1;
    }

    .info-icon {
      cursor: help;
    }
  `]
})
export class DHCPOptionsEditorComponent {
  @Input() options: DHCPOption[] = [];
  @Output() optionsChange = new EventEmitter<DHCPOption[]>();

  private optionCatalog = inject(OptionService).catalog;
  form = new FormGroup({});

  addOption() {
    // Add empty option row
  }

  removeOption(index: number) {
    const updated = this.options.filter((_, i) => i !== index);
    this.optionsChange.emit(updated);
  }

  getOptionDescription(index: number): string {
    const opt = this.options[index];
    const catEntry = this.optionCatalog().find(o => o.code === opt.code);
    return catEntry?.description || 'No description available.';
  }
}
```

---

## Visual Language

### Color Scheme

#### Light Theme (Default)

```scss
// styles/colors.scss

:root {
  // Background colors
  --bg-primary: #ffffff;
  --bg-secondary: #f5f5f5;
  --bg-sidebar: #fafafa;
  --bg-highlight: #e3f2fd;

  // Border & divider
  --border-color: #e0e0e0;
  --divider-color: #f0f0f0;

  // Text
  --text-primary: #212121;
  --text-secondary: #757575;
  --text-disabled: #bdbdbd;

  // Status indicators
  --status-ok: #4caf50;       /* green */
  --status-warn: #ff9800;     /* orange */
  --status-error: #f44336;    /* red */

  // Utilization thresholds
  --util-available: #4caf50;  /* <60% utilization: green */
  --util-used: #ff9800;       /* 60-80%: orange */
  --util-full: #f44336;       /* >80%: red */
  --util-reserved: #fdd835;   /* reserved/pending: yellow */

  // Interactive
  --primary-color: #1976d2;
  --accent-color: #ff4081;
  --hover-bg: #f5f5f5;
  --active-bg: #e3f2fd;
}

@media (prefers-color-scheme: dark) {
  :root {
    --bg-primary: #121212;
    --bg-secondary: #1e1e1e;
    --bg-sidebar: #0f0f0f;
    --bg-highlight: #1e3a5f;

    --border-color: #333333;
    --divider-color: #262626;

    --text-primary: #e0e0e0;
    --text-secondary: #b0b0b0;
    --text-disabled: #686868;

    --status-ok: #66bb6a;
    --status-warn: #ffb74d;
    --status-error: #ef5350;

    --util-available: #66bb6a;
    --util-used: #ffb74d;
    --util-full: #ef5350;
    --util-reserved: #ffd54f;

    --primary-color: #64b5f6;
    --accent-color: #ff80ab;
    --hover-bg: #272727;
    --active-bg: #1e3a5f;
  }
}
```

#### Utilization Thresholds

```
< 60%: Green (available space, healthy)
60-80%: Orange (caution, approaching limit)
> 80%: Red (critical, urgent expansion needed)
Reserved/Pending: Yellow (held but not yet assigned)
```

#### Status Indicators

- **OK (Green)**: All resources healthy, no issues
- **Warn (Orange)**: Approaching limits (>60% utilization), or minor issues
- **Error (Red)**: Critical (>80% utilization), or configuration issues

### Typography

```scss
// styles/typography.scss

$font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
$font-size-body: 14px;
$font-size-small: 12px;
$font-size-heading-1: 24px;
$font-size-heading-2: 20px;
$font-size-heading-3: 16px;

body {
  font-family: $font-family;
  font-size: $font-size-body;
  line-height: 1.5;
  color: var(--text-primary);
  background: var(--bg-primary);
}

h1 { font-size: $font-size-heading-1; font-weight: 600; }
h2 { font-size: $font-size-heading-2; font-weight: 600; }
h3 { font-size: $font-size-heading-3; font-weight: 500; }

.caption { font-size: $font-size-small; color: var(--text-secondary); }
```

### Layout & Spacing

```scss
// styles/layout.scss

$spacing-unit: 4px;
$spacing-xs: $spacing-unit;           // 4px
$spacing-sm: $spacing-unit * 2;       // 8px
$spacing-md: $spacing-unit * 3;       // 12px
$spacing-lg: $spacing-unit * 4;       // 16px
$spacing-xl: $spacing-unit * 6;       // 24px

// Layout breakpoints
$breakpoint-sm: 600px;
$breakpoint-md: 960px;
$breakpoint-lg: 1280px;

.container {
  max-width: 1200px;
  margin: 0 auto;
  padding: $spacing-lg;
}

.card {
  background: var(--bg-primary);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  padding: $spacing-lg;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
}
```

---

## Key Features & Flows

### 1. Global Search (Cmd+K)

- **Trigger**: `Cmd+K` (macOS) or `Ctrl+K` (Windows/Linux)
- **Search scope**: VRF, Aggregate, Subnet, Scope, Pool, Template, IP address (in reservations)
- **Result display**: Categorized list with preview snippets
- **Actions**: Click to navigate; inline "reserve" button on IP results

### 2. Dashboard

**Path**: `/dashboard`

**Sections**:
- **Summary Stats**: Total pools, subnets, utilization % (global)
- **Utilization Gauge**: Large circular gauge (CSS or SVG) showing global % used
- **Top Utilized Pools**: Table of top 10 pools by utilization; each row has inline bar
- **Pools Near Capacity**: Alert-style list for pools >80% utilized
- **Recent Activity**: Audit log (last 20 events)
- **Quick Actions**: Buttons (New VRF, New Subnet, New Pool, Reserve IP)

### 3. VRF Detail (`/ipam/vrf/:vrfId`)

**Sections**:
- **Header**: VRF name, description, tags/labels, edit/delete buttons
- **Utilization**: Rollup bar (used / total) across all aggregates
- **Aggregates Table**: List aggregates; each row: name, CIDR, util%, status dot
- **Actions**: Create Aggregate, Attach Template

### 4. Subnet Detail (`/ipam/subnet/:subnetId`)

**Sections**:
- **Header**: Subnet CIDR, description, tags, edit
- **Utilization**: Rollup across scopes
- **Scopes Table**: Each row: scope name, status (ok/warn/error), util%
- **Actions**: Create Scope

### 5. Pool Detail (`/ipam/pool/:poolId`) — **Key Feature**

**Sections**:

1. **Header**: Pool CIDR, description, tags, edit/delete
2. **Utilization Details**:
   - Large bar with 3 segments: used, reserved, available
   - Below bar: "X IPs used, Y reserved, Z available"
   - IP range breakdown (optional): color-coded ranges showing which IPs are in which state
3. **Reservations Table** (main section):
   - Columns: IP Address | MAC | Hostname | Description | Actions
   - Inline edit/delete buttons
   - Add Reservation button (opens form or inline row)
4. **Reservation Form**:
   - IP (or "next available" button)
   - MAC address (validated)
   - Hostname (optional)
   - Description (optional)
   - Save / Cancel
5. **DHCP Options Table**:
   - Inherited options (from scope, template) in gray/disabled
   - Pool-specific options: editable rows
   - Each option: name, value, description (hover tooltip)
   - Add Option button
6. **Template Application**:
   - "Attach Template" button → modal picker
   - List attached templates with remove buttons

### 6. Scope Detail (`/ipam/scope/:scopeId`)

Similar to pool, but:
- **DHCP Options**: Scope-specific + inherited from aggregate/VRF
- **Pools Table**: List pools in this scope; each with util bar
- **No Reservations**: Managed per pool only

### 7. Template Management (`/templates`)

**Template List**:
- Table: Name | Description | Applied to (count) | Actions (Edit, Delete, Preview)

**Template Detail**:
- Header: Name, description, tags
- DHCP Options Editor (same as pool/scope)
- Usage List: Shows all scopes/pools using this template
- Attach Policy: Option to "set as default for new X"

### 8. Settings

**Tabs**:

1. **General**: Server name, logging level, branding (logo URL, title)
2. **DHCP Options Catalog**: Read-only viewer of all standard DHCP options
   - Table: Code | Name | Type | Description
   - Searchable/filterable
3. **Cluster**: HA config
   - Cluster name
   - Virtual IP (VIP)
   - Server list (primary, secondary, witness)
   - Heartbeat interval

---

## API Integration

### Expected Backend Endpoints

The frontend assumes these RESTful endpoints. (Backend team defines exact paths.)

```
Authentication
POST   /api/v1/auth/login          { username, password } → { token, user }
POST   /api/v1/auth/logout
GET    /api/v1/auth/me             → { user info }
POST   /api/v1/auth/refresh        (if using JWT refresh)

VRF
GET    /api/v1/vrf
GET    /api/v1/vrf/:id
POST   /api/v1/vrf                 { name, description, tags }
PUT    /api/v1/vrf/:id             { name, description, tags }
DELETE /api/v1/vrf/:id

Aggregate
GET    /api/v1/vrf/:vrfId/aggregates
GET    /api/v1/aggregate/:id
POST   /api/v1/vrf/:vrfId/aggregates  { name, cidr, description, tags }
PUT    /api/v1/aggregate/:id
DELETE /api/v1/aggregate/:id

Subnet (child of Aggregate)
GET    /api/v1/aggregate/:aggId/subnets
GET    /api/v1/subnet/:id
POST   /api/v1/aggregate/:aggId/subnets
PUT    /api/v1/subnet/:id
DELETE /api/v1/subnet/:id

Scope (child of Subnet)
GET    /api/v1/subnet/:subnetId/scopes
GET    /api/v1/scope/:id
POST   /api/v1/subnet/:subnetId/scopes
PUT    /api/v1/scope/:id
DELETE /api/v1/scope/:id

Pool (child of Scope)
GET    /api/v1/scope/:scopeId/pools
GET    /api/v1/pool/:id
POST   /api/v1/scope/:scopeId/pools   { name, cidr, description, ... }
PUT    /api/v1/pool/:id
DELETE /api/v1/pool/:id

Reservation (child of Pool)
GET    /api/v1/pool/:poolId/reservations
GET    /api/v1/reservation/:id
POST   /api/v1/pool/:poolId/reservations  { ipAddress, mac, hostname, description }
PUT    /api/v1/reservation/:id
DELETE /api/v1/reservation/:id

Available IPs (in pool)
GET    /api/v1/pool/:poolId/available-ips?limit=100&offset=0

Template
GET    /api/v1/templates
GET    /api/v1/templates/:id
POST   /api/v1/templates           { name, description, options, tags }
PUT    /api/v1/templates/:id
DELETE /api/v1/templates/:id
POST   /api/v1/templates/:id/attach-scope/:scopeId
POST   /api/v1/templates/:id/attach-pool/:poolId
DELETE /api/v1/templates/:id/detach-scope/:scopeId
DELETE /api/v1/templates/:id/detach-pool/:poolId

DHCP Options Catalog
GET    /api/v1/options/catalog     → { code, name, type, description, format }[]

Utilization
GET    /api/v1/utilization/summary
GET    /api/v1/utilization/:entityType/:entityId

Search
GET    /api/v1/search?q=<query>    → { results: SearchResult[] }

Audit Log
GET    /api/v1/audit/events?limit=50&offset=0
```

---

## Implementation Priorities

### Phase 1 (MVP)

1. Login & auth guard
2. IPAM tree navigation + sidebar
3. VRF, Aggregate, Subnet detail views
4. Pool detail with basic reservation CRUD
5. Global search (basic)
6. Utilization bar component
7. Dashboard (summary stats only)

### Phase 2

1. DHCP options editor + catalog
2. Template management
3. Detailed utilization breakdown (IP ranges, history)
4. Settings pages
5. Advanced search filters

### Phase 3

1. HA cluster management UI
2. Audit log viewer
3. Bulk operations (import/export)
4. Mobile-responsive layout

---

## Testing Strategy

- **Unit Tests**: Services, components (signal-based logic)
- **Integration Tests**: Component + Service interaction
- **E2E Tests**: Cypress or Playwright (login → CRUD flow for key entities)

---

## Build & Deployment

1. **Build**: `npm run build` → outputs to `dist/web`
2. **Embed**: Go binary includes `//go:embed web/dist` → serves at `/`
3. **SPA Routing**: Fallback route serves `index.html` for all unknown paths
4. **Environment**: `environment.prod.ts` points to `/api/v1` (relative, no hardcoded domain)

---

## Notes for Implementers

- **Use Signals API** throughout; avoid .subscribe() where possible; use async pipe in templates
- **Angular Material** for all UI controls (buttons, inputs, dialogs, tables, tree)
- **Reactive Forms** for all data entry; never use template-driven forms
- **Type safety**: Every API response must have a TypeScript interface
- **No console.log** in production; use Angular logger service
- **Lazy-load** feature modules (dashboard, ipam, templates, settings)
- **SCSS** for styles; use CSS custom properties for theme switching
- **Accessibility**: ARIA labels, keyboard navigation, color contrast
- **Dark mode**: Test thoroughly; use CSS custom properties (tokens) for theme-aware colors
