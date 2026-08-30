import { Routes } from '@angular/router';
import { adminGuard } from './admin.guard';

/** Mounted at `/admin` by app.routes.ts. Every route here requires the
 * admin role (per A15) — enforced by `adminGuard` on the wrapping empty-path
 * route so it applies to the whole subtree in one place. */
export const ADMIN_ROUTES: Routes = [
  {
    path: '',
    canActivate: [adminGuard],
    canActivateChild: [adminGuard],
    children: [
      { path: '', pathMatch: 'full', redirectTo: 'users' },
      {
        path: 'users',
        loadComponent: () => import('./users/users-list.component').then((m) => m.UsersListComponent),
      },
      {
        path: 'users/:id/tokens',
        loadComponent: () => import('./users/user-tokens.component').then((m) => m.UserTokensComponent),
      },
      {
        path: 'audit',
        loadComponent: () => import('./audit/audit-log.component').then((m) => m.AuditLogComponent),
      },
      {
        path: 'settings',
        loadComponent: () => import('./settings/settings.component').then((m) => m.SettingsComponent),
      },
    ],
  },
];
