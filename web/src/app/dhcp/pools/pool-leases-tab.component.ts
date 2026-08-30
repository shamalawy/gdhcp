import { ChangeDetectionStrategy, Component, input } from '@angular/core';

import { DHCPPool } from '../../shared/models';
import { LeasesTableComponent } from '../leases/leases-table.component';

/**
 * The pool detail page's Leases tab. It is LeasesTableComponent scoped to
 * one pool, so this tab and the standalone `/dhcp/leases` page share one
 * implementation of search, selection, pinning and the lease actions —
 * previously this tab carried its own reduced copy with a state filter and
 * a single "reserve" icon and nothing else.
 */
@Component({
  selector: 'app-pool-leases-tab',
  standalone: true,
  imports: [LeasesTableComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `<app-leases-table [poolId]="pool().id" [pool]="pool()" />`,
})
export class PoolLeasesTabComponent {
  readonly pool = input.required<DHCPPool>();
}
