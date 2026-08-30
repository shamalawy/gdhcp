import { ChangeDetectionStrategy, Component } from '@angular/core';

import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { LeasesTableComponent } from './leases-table.component';

/**
 * `/dhcp/leases` — every lease in the deployment in one searchable table.
 *
 * Leases used to be reachable only through a pool's detail page, which made
 * "which client has 10.10.1.65?" a navigation puzzle and left global search
 * with nowhere to send a lease hit. The table itself is
 * LeasesTableComponent, shared with that pool tab.
 */
@Component({
  selector: 'app-leases-list',
  standalone: true,
  imports: [PageHeaderComponent, LeasesTableComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header title="Leases" />
    <app-leases-table />
  `,
})
export class LeasesListComponent {}
