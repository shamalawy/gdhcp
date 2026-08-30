import { Injectable, inject } from '@angular/core';
import { Observable, map, of, switchMap } from 'rxjs';

import { ApiService } from '../../shared/api.service';
import { MessageResponse, Tag, TaggableEntityType } from '../../shared/models';

/**
 * IPAM prefixes are a first-class taggable entity per the product quality
 * bar ("every user-facing entity supports description + tags"). 'prefix'
 * is now a real member of `TaggableEntityType` and is accepted by the
 * server's validEntityType (api/tags/handler.go), so no cast is needed —
 * this constant just keeps the literal in one place.
 */
export const PREFIX_ENTITY_TYPE: TaggableEntityType = 'prefix';

/**
 * Thin wrapper around the tag-related `ApiService` methods, shared by every
 * IPAM page that shows/edits tags (VRF cards, VRF detail, prefix detail,
 * prefix tables).
 *
 * NOTE: `docs/api/endpoints.md`'s Tag Endpoints section documents `GET
 * /tags` accepts `entity_type`/`entity_id` to filter down to one entity's
 * attached tags (passed through `ApiService.listTags`'s existing
 * `ListParams` index signature, so no change to shared/api.service.ts was
 * needed). The server ignored those two params until they were wired to
 * ListTagsForEntity, which is why every entity used to display the whole
 * global tag catalogue as its own.
 */
@Injectable({ providedIn: 'root' })
export class EntityTagsService {
  private readonly api = inject(ApiService);

  list(entityType: TaggableEntityType, entityId: string): Observable<Tag[]> {
    return this.api
      .listTags({ entity_type: entityType, entity_id: entityId, limit: 100 })
      .pipe(map((res) => res.items));
  }

  /** Attaches a tag by name to the entity, creating a new tag first if no
   * existing tag matches the name case-insensitively. Resolves with the
   * attached `Tag`. */
  addByName(entityType: TaggableEntityType, entityId: string, name: string): Observable<Tag> {
    const trimmed = name.trim();
    return this.api.listTags({ search: trimmed, limit: 50 }).pipe(
      switchMap((res) => {
        const existing = res.items.find((tag) => tag.name.toLowerCase() === trimmed.toLowerCase());
        const tag$ = existing ? of(existing) : this.api.createTag({ name: trimmed });
        return tag$.pipe(
          switchMap((tag) =>
            this.api
              .addTagToEntity(tag.id, { entity_type: entityType, entity_id: entityId })
              .pipe(map(() => tag)),
          ),
        );
      }),
    );
  }

  remove(tagId: string, entityType: TaggableEntityType, entityId: string): Observable<MessageResponse> {
    return this.api.removeTagFromEntity(tagId, entityType, entityId);
  }
}
