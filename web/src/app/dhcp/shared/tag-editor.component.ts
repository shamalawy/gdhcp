import { ChangeDetectionStrategy, Component, effect, inject, input, signal } from '@angular/core';
import { switchMap } from 'rxjs';

import { ApiService } from '../../shared/api.service';
import { TagChipsComponent } from '../../shared/components/tag-chips/tag-chips.component';
import { Tag, TaggableEntityType } from '../../shared/models';
import { NotifyService } from './notify.service';

/**
 * Wraps `TagChipsComponent` with the add/remove API calls for one entity.
 *
 * NOTE (see notes_for_integration): docs/api/endpoints.md and CONTRACTS.md
 * define `POST /tags/{id}/entities` and `DELETE /tags/{id}/entities/{type}/
 * {id}` (attach/detach) but no `GET` that returns the tags currently on a
 * given entity — `DHCPScope`/`DHCPPool`/etc. responses don't embed a `tags`
 * field either. So this component's attached-tags list is session-local: it
 * starts empty and reflects adds/removes made in this view, but can't
 * recover what was attached on a previous visit. Functionally this makes
 * tagging write-mostly until a query endpoint (or embedded `tags[]` on the
 * entity response) exists upstream.
 */
@Component({
  selector: 'app-tag-editor',
  standalone: true,
  imports: [TagChipsComponent],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `<app-tag-chips [tags]="tags()" [editable]="true" (tagAdd)="onAdd($event)" (tagRemove)="onRemove($event)" />`,
})
export class TagEditorComponent {
  private readonly api = inject(ApiService);
  private readonly notify = inject(NotifyService);

  readonly entityType = input.required<TaggableEntityType>();
  readonly entityId = input.required<string>();
  /** Optional seed (e.g. if a parent already loaded some tags some other
   * way); defaults to empty per the limitation noted above. */
  readonly initialTags = input<Tag[]>([]);

  private readonly tagsSignal = signal<Tag[]>([]);
  readonly tags = this.tagsSignal.asReadonly();

  constructor() {
    effect(() => this.tagsSignal.set(this.initialTags()), { allowSignalWrites: true });
  }

  onAdd(name: string): void {
    const trimmed = name.trim();
    if (!trimmed) return;

    this.api
      .listTags({ search: trimmed, limit: 50 })
      .pipe(
        switchMap((page) => {
          const existing = page.items.find((t) => t.name.toLowerCase() === trimmed.toLowerCase());
          return existing ? [existing] : this.api.createTag({ name: trimmed });
        }),
        switchMap((tag) =>
          this.api.addTagToEntity(tag.id, { entity_type: this.entityType(), entity_id: this.entityId() }).pipe(
            switchMap(() => [tag]),
          ),
        ),
      )
      .subscribe({
        next: (tag) => {
          if (!this.tagsSignal().some((t) => t.id === tag.id)) {
            this.tagsSignal.update((list) => [...list, tag]);
          }
        },
        error: (err) => this.notify.error(err, 'Could not add tag.'),
      });
  }

  onRemove(tagId: string): void {
    this.api.removeTagFromEntity(tagId, this.entityType(), this.entityId()).subscribe({
      next: () => this.tagsSignal.update((list) => list.filter((t) => t.id !== tagId)),
      error: (err) => this.notify.error(err, 'Could not remove tag.'),
    });
  }
}
