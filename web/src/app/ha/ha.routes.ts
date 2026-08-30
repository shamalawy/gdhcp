import { Routes } from '@angular/router';

/** Mounted at `/ha` by app.routes.ts. */
export const HA_ROUTES: Routes = [
  {
    path: '',
    loadComponent: () => import('./cluster-list.component').then((m) => m.ClusterListComponent),
  },
  {
    path: 'clusters/:id',
    loadComponent: () => import('./cluster-detail.component').then((m) => m.ClusterDetailComponent),
  },
];
