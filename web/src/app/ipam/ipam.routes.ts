import { Routes } from '@angular/router';

/**
 * BINDING CONTRACT (per app.routes.ts / scaffold notes): exports
 * `IPAM_ROUTES`, mounted at `/ipam` by the root route table.
 *
 * Every page nests under IpamShellComponent, which puts the prefix tree in
 * the main content area beside the routed detail view. The tree previously
 * lived in the app sidenav; it is the section's primary navigation model
 * (docs/design/frontend.md:36) and needs the room.
 *
 * Route order: `search` and `vrfs/:id` / `prefixes/:id` don't collide
 * (different first segments), so no special ordering is required here.
 */
export const IPAM_ROUTES: Routes = [
  {
    path: '',
    loadComponent: () => import('./layout/ipam-shell.component').then((m) => m.IpamShellComponent),
    children: [
      {
        path: '',
        loadComponent: () => import('./overview/ipam-overview.component').then((m) => m.IpamOverviewComponent),
        title: 'IPAM',
      },
      {
        path: 'search',
        loadComponent: () => import('./search/ipam-search.component').then((m) => m.IpamSearchComponent),
        title: 'Search — IPAM',
      },
      {
        path: 'vlans',
        loadComponent: () => import('./vlans/vlans-list.component').then((m) => m.VlansListComponent),
        title: 'VLANs — IPAM',
      },
      {
        path: 'vlans/:id',
        loadComponent: () => import('./vlans/vlan-detail.component').then((m) => m.VlanDetailComponent),
        title: 'VLAN — IPAM',
      },
      {
        path: 'vrfs/:id',
        loadComponent: () => import('./vrf-detail/vrf-detail.component').then((m) => m.VrfDetailComponent),
        title: 'VRF — IPAM',
      },
      {
        path: 'prefixes/:id',
        loadComponent: () => import('./prefix-detail/prefix-detail.component').then((m) => m.PrefixDetailComponent),
        title: 'Prefix — IPAM',
      },
    ],
  },
];
