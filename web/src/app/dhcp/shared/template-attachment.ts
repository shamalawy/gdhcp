import { Observable, forkJoin, of } from 'rxjs';
import { catchError, map, switchMap } from 'rxjs/operators';

import { ApiService } from '../../shared/api.service';
import { DHCPTemplate, TemplateScopeAssoc } from '../../shared/models';

export interface AttachedTemplate {
  template: DHCPTemplate;
  assoc: TemplateScopeAssoc;
}

/** Resolves the templates attached to a scope, sorted ascending by
 * priority. ApiService has no `GET /scopes/{id}/templates` — only the
 * reverse `GET /templates/{id}/scopes` exists per endpoints.md — so this
 * fans out across every template and filters client-side. Shared by the
 * scope Templates/Options tabs and the pool Options tab (which needs its
 * owning scope's attached templates as an inherited option layer). See
 * notes_for_integration for the suggestion to add a direct endpoint. */
export function fetchAttachedTemplates(api: ApiService, scopeId: string): Observable<AttachedTemplate[]> {
  return api.listTemplates({ limit: 500 }).pipe(
    switchMap((page) => {
      if (page.items.length === 0) return of([] as (AttachedTemplate | null)[]);
      return forkJoin(
        page.items.map((template: DHCPTemplate) =>
          api.getTemplateScopes(template.id).pipe(
            catchError(() => of<TemplateScopeAssoc[]>([])),
            map((assocs) => {
              const assoc = assocs.find((a) => a.scope_id === scopeId);
              return assoc ? { template, assoc } : null;
            }),
          ),
        ),
      );
    }),
    map((results) =>
      results
        .filter((r): r is AttachedTemplate => r !== null)
        .sort((a, b) => a.assoc.priority - b.assoc.priority),
    ),
  );
}
