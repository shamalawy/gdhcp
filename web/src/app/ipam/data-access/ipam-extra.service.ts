import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiService } from '../../shared/api.service';
import { ChartPoint, PrefixUtilization } from '../../shared/models';

export interface NextAvailableSubnetResponse {
  subnet: string;
  parent_prefix_id: string;
  available: boolean;
}

/**
 * Wraps two endpoints this unit's task explicitly requires (a
 * next-available-subnet helper and a prefix utilization panel) that are
 * NOT in docs/api/endpoints.md's binding REST surface table:
 *
 * - `next-available-subnet`: documented only in docs/design/api-cli.md
 *   §5.18 ("Helper Endpoints (Next Available)") as
 *   `GET /api/v1/ipam/next-available-subnet?prefix_id=&size=` ->
 *   `{subnet, parent_prefix_id, available}`. design/*.md is reference
 *   material, not binding, but this is the only spec for the feature the
 *   task asks for, so this unit codes against it directly. See
 *   notes_for_integration.
 * - `/prefixes/{id}/utilization`: -> `PrefixUtilization` (shared/models.ts):
 *   a DHCP view in the same shape as the documented `/scopes/{id}/
 *   utilization` and `/pools/{id}/utilization` endpoints, plus an
 *   address-space view specific to prefixes.
 *
 * Both go through `ApiService`'s existing generic `get<T>()` (no changes
 * to shared/api.service.ts were needed).
 */
@Injectable({ providedIn: 'root' })
export class IpamExtraService {
  private readonly api = inject(ApiService);

  nextAvailableSubnet(prefixId: string, size: number): Observable<NextAvailableSubnetResponse> {
    const query = `prefix_id=${encodeURIComponent(prefixId)}&size=${size}`;
    return this.api.get<NextAvailableSubnetResponse>(`/ipam/next-available-subnet?${query}`);
  }

  prefixUtilization(prefixId: string): Observable<PrefixUtilization> {
    return this.api.get<PrefixUtilization>(`/prefixes/${prefixId}/utilization`);
  }

  prefixUtilizationHistory(prefixId: string, days = 14): Observable<ChartPoint[]> {
    return this.api.dashboardUtilizationChart('prefix', prefixId, days);
  }
}
