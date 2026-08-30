import { Injectable, computed, inject, signal } from '@angular/core';
import { shareReplay, tap } from 'rxjs';

import { ApiService } from '../../shared/api.service';
import { OptionCatalogEntry } from '../../shared/models';

/** Client-side grouping of the IANA option catalog for the "add option"
 * picker (spec: "option picker grouped by category"). `OptionCatalogEntry`
 * carries no category field from the API (docs/CONTRACTS.md's DHCPOption /
 * OptionCatalogEntry shapes don't define one), so categories here are a
 * presentation-only heuristic keyed on well-known IANA option codes; any
 * code not matched falls into "Other". See notes_for_integration for the
 * suggestion to add a real `category` column upstream. */
export type OptionCategory =
  | 'Addressing & Routing'
  | 'DNS & Naming'
  | 'Time & NTP'
  | 'Boot & PXE'
  | 'Client Identification'
  | 'Vendor & Site-Specific'
  | 'Other';

const CATEGORY_BY_CODE: Record<number, OptionCategory> = {
  1: 'Addressing & Routing',
  3: 'Addressing & Routing',
  21: 'Addressing & Routing',
  28: 'Addressing & Routing',
  33: 'Addressing & Routing',
  121: 'Addressing & Routing',
  249: 'Addressing & Routing',
  6: 'DNS & Naming',
  15: 'DNS & Naming',
  119: 'DNS & Naming',
  12: 'DNS & Naming',
  81: 'DNS & Naming',
  4: 'Time & NTP',
  2: 'Time & NTP',
  42: 'Time & NTP',
  100: 'Time & NTP',
  101: 'Time & NTP',
  67: 'Boot & PXE',
  66: 'Boot & PXE',
  60: 'Boot & PXE',
  93: 'Boot & PXE',
  94: 'Boot & PXE',
  97: 'Boot & PXE',
  55: 'Client Identification',
  50: 'Client Identification',
  51: 'Client Identification',
  53: 'Client Identification',
  54: 'Client Identification',
  57: 'Client Identification',
  58: 'Client Identification',
  59: 'Client Identification',
  61: 'Client Identification',
  82: 'Client Identification',
  43: 'Vendor & Site-Specific',
  125: 'Vendor & Site-Specific',
  224: 'Vendor & Site-Specific',
};

export function categoryForCode(code: number): OptionCategory {
  return CATEGORY_BY_CODE[code] ?? 'Other';
}

/** Fetches and caches `GET /options/catalog` (all IANA options, no paging
 * per endpoints.md) for the lifetime of the app — used by the option-picker
 * dialog, the effective-options table (name/description lookups), and the
 * `/dhcp/options` catalog browser page. */
@Injectable({ providedIn: 'root' })
export class OptionCatalogService {
  private readonly api = inject(ApiService);

  private readonly entriesSignal = signal<OptionCatalogEntry[]>([]);
  private readonly loadedSignal = signal(false);
  readonly entries = this.entriesSignal.asReadonly();
  readonly loaded = this.loadedSignal.asReadonly();

  private readonly byCode = computed(() => {
    const map = new Map<number, OptionCatalogEntry>();
    for (const entry of this.entriesSignal()) map.set(entry.option_code, entry);
    return map;
  });

  private request$ = this.api.optionCatalog().pipe(
    tap((entries) => {
      this.entriesSignal.set(entries);
      this.loadedSignal.set(true);
    }),
    shareReplay({ bufferSize: 1, refCount: false }),
  );

  /** Triggers (once) and returns the shared catalog-load observable; safe to
   * call from multiple components — they all share the one HTTP request. */
  ensureLoaded() {
    if (!this.loadedSignal()) this.request$.subscribe({ error: () => this.loadedSignal.set(true) });
    return this.request$;
  }

  lookup(code: number): OptionCatalogEntry | null {
    return this.byCode().get(code) ?? null;
  }

  nameFor(code: number): string {
    return this.lookup(code)?.name ?? `Option ${code}`;
  }
}
