import { Routes } from '@angular/router';

/** Mounted at `/dashboard` by app.routes.ts. Single page — see
 * dashboard.component.ts. */
export const DASHBOARD_ROUTES: Routes = [
  {
    path: '',
    loadComponent: () => import('./dashboard.component').then((m) => m.DashboardComponent),
  },
];
