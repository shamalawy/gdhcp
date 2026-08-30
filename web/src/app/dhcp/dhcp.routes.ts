import { Routes } from '@angular/router';

/**
 * DHCP feature routes — mounted at `/dhcp` by the root route table
 * (app.routes.ts's binding contract: this file exports `DHCP_ROUTES`).
 *
 * Everything nests under DhcpShellComponent, whose tab strip
 * (Scopes | Leases | Templates | Options) is the section's navigation:
 * templates and the option catalogue were complete but unreachable before
 * it, since nothing linked to them and `/dhcp` redirected straight to
 * scopes.
 *
 * `scopes/new` is listed before `scopes/:id` so it isn't swallowed by the
 * param route.
 */
export const DHCP_ROUTES: Routes = [
  {
    path: '',
    loadComponent: () => import('./layout/dhcp-shell.component').then((m) => m.DhcpShellComponent),
    children: [
      { path: '', pathMatch: 'full', redirectTo: 'scopes' },

      {
        path: 'scopes',
        loadComponent: () => import('./scopes/scopes-list.component').then((m) => m.ScopesListComponent),
      },
      {
        path: 'scopes/new',
        loadComponent: () => import('./scopes/scope-form.component').then((m) => m.ScopeFormComponent),
      },
      {
        path: 'scopes/:id',
        loadComponent: () => import('./scopes/scope-detail.component').then((m) => m.ScopeDetailComponent),
      },

      {
        path: 'pools/:id',
        loadComponent: () => import('./pools/pool-detail.component').then((m) => m.PoolDetailComponent),
      },

      {
        path: 'leases',
        loadComponent: () => import('./leases/leases-list.component').then((m) => m.LeasesListComponent),
        title: 'Leases — DHCP',
      },
      {
        path: 'leases/:id',
        loadComponent: () => import('./leases/lease-detail.component').then((m) => m.LeaseDetailComponent),
        title: 'Lease — DHCP',
      },

      {
        path: 'templates',
        loadComponent: () => import('./templates/templates-list.component').then((m) => m.TemplatesListComponent),
      },
      {
        path: 'templates/:id',
        loadComponent: () => import('./templates/template-detail.component').then((m) => m.TemplateDetailComponent),
      },

      {
        path: 'options',
        loadComponent: () => import('./options/options-catalog.component').then((m) => m.OptionsCatalogComponent),
      },
    ],
  },
];
