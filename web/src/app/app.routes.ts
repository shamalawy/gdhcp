import { Routes } from '@angular/router';
import { authGuard } from './auth/auth.guard';

/**
 * Root route table — BINDING CONTRACT for feature agents.
 *
 * Each feature area is lazy-loaded from `<area>/<area>.routes.ts`, which
 * MUST export a `Routes` array named `<AREA>_ROUTES` (e.g. `DASHBOARD_ROUTES`
 * from `./dashboard/dashboard.routes`). Paths are relative to the feature's
 * own route prefix (e.g. `dhcp/dhcp.routes.ts`'s routes are mounted at
 * `/dhcp`, so an internal route `scopes/:scopeId` resolves to
 * `/dhcp/scopes/:scopeId`).
 */
export const routes: Routes = [
  {
    path: 'login',
    loadComponent: () => import('./auth/login.component').then((m) => m.LoginComponent),
  },
  {
    path: '',
    loadComponent: () => import('./layout/shell.component').then((m) => m.ShellComponent),
    canActivate: [authGuard],
    children: [
      {
        path: 'dashboard',
        loadChildren: () =>
          import('./dashboard/dashboard.routes').then((m) => m.DASHBOARD_ROUTES),
      },
      {
        path: 'ipam',
        loadChildren: () => import('./ipam/ipam.routes').then((m) => m.IPAM_ROUTES),
      },
      {
        path: 'dhcp',
        loadChildren: () => import('./dhcp/dhcp.routes').then((m) => m.DHCP_ROUTES),
      },
      {
        path: 'ha',
        loadChildren: () => import('./ha/ha.routes').then((m) => m.HA_ROUTES),
      },
      {
        path: 'admin',
        loadChildren: () => import('./admin/admin.routes').then((m) => m.ADMIN_ROUTES),
      },
      { path: '', pathMatch: 'full', redirectTo: 'dashboard' },
    ],
  },
  {
    path: '**',
    loadComponent: () => import('./layout/not-found.component').then((m) => m.NotFoundComponent),
  },
];
