import { ChangeDetectionStrategy, Component, computed, inject, signal } from '@angular/core';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatSelectModule } from '@angular/material/select';
import { MatStepperModule } from '@angular/material/stepper';
import { ActivatedRoute, Router } from '@angular/router';

import { PageHeaderComponent } from '../../shared/components/page-header/page-header.component';
import { ApiService } from '../../shared/api.service';
import { Cluster, IPAMPrefix, VRF } from '../../shared/models';
import { NotifyService } from '../shared/notify.service';

/** Create-scope wizard: VRF → cluster → subnet prefix → name/description,
 * each step with inline help text (per spec). Supports `?prefix_id=` to
 * preselect a subnet prefix (and its VRF) when arriving from the IPAM
 * tree/prefix detail's "Create scope" action. */
@Component({
  selector: 'app-scope-form',
  standalone: true,
  imports: [
    ReactiveFormsModule,
    PageHeaderComponent,
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatProgressSpinnerModule,
    MatSelectModule,
    MatStepperModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <app-page-header
      title="New DHCP Scope"
      [breadcrumbs]="[{ label: 'Scopes', link: '/dhcp/scopes' }, { label: 'New' }]"
    />

    <mat-stepper orientation="vertical" linear #stepper class="wizard">
      <mat-step [stepControl]="vrfForm" label="VRF">
        <p class="help">
          A VRF is the routing domain this scope belongs to. Most deployments only use the
          default VRF; pick another one if this subnet lives in an isolated routing context
          (e.g. a customer or tenant network).
        </p>
        <form [formGroup]="vrfForm">
          <mat-form-field appearance="outline" class="full">
            <mat-label>VRF</mat-label>
            <mat-select formControlName="vrf_id" (selectionChange)="onVrfChange()">
              @for (vrf of vrfs(); track vrf.id) {
                <mat-option [value]="vrf.id">{{ vrf.name }}{{ vrf.is_default ? ' (default)' : '' }}</mat-option>
              }
            </mat-select>
          </mat-form-field>
        </form>
        <div class="step-actions">
          <button mat-flat-button color="primary" matStepperNext [disabled]="vrfForm.invalid">Next</button>
        </div>
      </mat-step>

      <mat-step [stepControl]="clusterForm" label="HA cluster">
        <p class="help">
          The HA cluster is the pair (or group) of gdhcp servers that will actually answer
          DHCP requests for this scope. Each server in the cluster shares leases and options
          for this scope via the shared database.
        </p>
        <form [formGroup]="clusterForm">
          <mat-form-field appearance="outline" class="full">
            <mat-label>Cluster</mat-label>
            <mat-select formControlName="cluster_id">
              @for (cluster of clusters(); track cluster.id) {
                <mat-option [value]="cluster.id">{{ cluster.name }}</mat-option>
              }
            </mat-select>
            @if (clusters().length === 0) {
              <mat-hint>No clusters registered yet — create one under HA before continuing.</mat-hint>
            }
          </mat-form-field>
        </form>
        <div class="step-actions">
          <button mat-button matStepperPrevious>Back</button>
          <button mat-flat-button color="primary" matStepperNext [disabled]="clusterForm.invalid">Next</button>
        </div>
      </mat-step>

      <mat-step [stepControl]="prefixForm" label="Subnet prefix">
        <p class="help">
          Pick the IPAM subnet-type prefix this scope will hand out addresses from. The
          scope's network is fixed to this prefix's CIDR; only subnet-type prefixes in the
          chosen VRF are shown.
        </p>
        <form [formGroup]="prefixForm">
          <mat-form-field appearance="outline" class="full">
            <mat-label>Subnet prefix</mat-label>
            <mat-select formControlName="ipam_prefix_id">
              @for (prefix of prefixes(); track prefix.id) {
                <mat-option [value]="prefix.id">
                  {{ prefix.prefix }}{{ prefix.name ? ' — ' + prefix.name : '' }}
                </mat-option>
              }
            </mat-select>
            @if (prefixes().length === 0) {
              <mat-hint>No subnet prefixes in this VRF yet — create one under IPAM first.</mat-hint>
            }
          </mat-form-field>
        </form>
        <div class="step-actions">
          <button mat-button matStepperPrevious>Back</button>
          <button mat-flat-button color="primary" matStepperNext [disabled]="prefixForm.invalid">Next</button>
        </div>
      </mat-step>

      <mat-step [stepControl]="detailsForm" label="Name &amp; description">
        <p class="help">This name identifies the scope throughout the UI and CLI — pick something operators will recognize (e.g. the site or VLAN it serves).</p>
        <form [formGroup]="detailsForm">
          <mat-form-field appearance="outline" class="full">
            <mat-label>Name</mat-label>
            <input matInput formControlName="name" placeholder="e.g. site-a-workstations" />
            <mat-hint>Shown everywhere this scope is referenced.</mat-hint>
          </mat-form-field>
          <mat-form-field appearance="outline" class="full">
            <mat-label>Description</mat-label>
            <textarea matInput formControlName="description" rows="3" placeholder="Optional — what this scope is for"></textarea>
            <mat-hint>Optional, but helps the next operator understand this scope's purpose.</mat-hint>
          </mat-form-field>
        </form>

        @if (errorMessage()) {
          <p class="error" role="alert">{{ errorMessage() }}</p>
        }

        <div class="step-actions">
          <button mat-button matStepperPrevious>Back</button>
          <button mat-flat-button color="primary" [disabled]="detailsForm.invalid || submitting()" (click)="submit()">
            @if (submitting()) {
              <mat-spinner diameter="18" />
            } @else {
              Create scope
            }
          </button>
        </div>
      </mat-step>
    </mat-stepper>
  `,
  styles: [
    `
      .wizard {
        max-width: 640px;
      }
      .help {
        color: var(--text-secondary);
        font-size: 13px;
        margin: 0 0 16px;
        max-width: 480px;
      }
      .full {
        width: 100%;
        max-width: 420px;
      }
      .step-actions {
        display: flex;
        gap: 8px;
        margin-top: 8px;
      }
      .error {
        color: var(--status-error);
        font-size: 13px;
      }
    `,
  ],
})
export class ScopeFormComponent {
  private readonly fb = inject(FormBuilder);
  private readonly api = inject(ApiService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);
  private readonly notify = inject(NotifyService);

  readonly vrfs = signal<VRF[]>([]);
  readonly clusters = signal<Cluster[]>([]);
  readonly allPrefixes = signal<IPAMPrefix[]>([]);
  readonly submitting = signal(false);
  readonly errorMessage = signal<string | null>(null);

  readonly vrfForm = this.fb.nonNullable.group({ vrf_id: ['', Validators.required] });
  readonly clusterForm = this.fb.nonNullable.group({ cluster_id: ['', Validators.required] });
  readonly prefixForm = this.fb.nonNullable.group({ ipam_prefix_id: ['', Validators.required] });
  readonly detailsForm = this.fb.nonNullable.group({
    name: ['', [Validators.required, Validators.maxLength(255)]],
    description: [''],
  });

  readonly prefixes = computed(() => {
    const vrfId = this.vrfForm.controls.vrf_id.value;
    return this.allPrefixes().filter((p) => p.vrf_id === vrfId && p.prefix_type === 'subnet');
  });

  constructor() {
    this.api.listVRFs({ limit: 200 }).subscribe((res) => {
      this.vrfs.set(res.items);
      const defaultVrf = res.items.find((v) => v.is_default);
      if (defaultVrf && !this.vrfForm.controls.vrf_id.value) {
        this.vrfForm.controls.vrf_id.setValue(defaultVrf.id);
      }
      this.loadPrefixesForVrf();
    });
    this.api.listClusters({ limit: 200 }).subscribe((res) => this.clusters.set(res.items));

    const prefixId = this.route.snapshot.queryParamMap.get('prefix_id');
    if (prefixId) {
      this.api.getPrefix(prefixId).subscribe((prefix) => {
        this.vrfForm.controls.vrf_id.setValue(prefix.vrf_id);
        this.loadPrefixesForVrf();
        this.prefixForm.controls.ipam_prefix_id.setValue(prefix.id);
      });
    }
  }

  onVrfChange(): void {
    this.prefixForm.controls.ipam_prefix_id.setValue('');
    this.loadPrefixesForVrf();
  }

  private loadPrefixesForVrf(): void {
    const vrfId = this.vrfForm.controls.vrf_id.value;
    if (!vrfId) return;
    this.api.listPrefixes({ vrf_id: vrfId, type: 'subnet', limit: 500 }).subscribe((res) => this.allPrefixes.set(res.items));
  }

  submit(): void {
    if (this.vrfForm.invalid || this.clusterForm.invalid || this.prefixForm.invalid || this.detailsForm.invalid) return;
    this.submitting.set(true);
    this.errorMessage.set(null);

    const details = this.detailsForm.getRawValue();
    this.api
      .createScope({
        vrf_id: this.vrfForm.controls.vrf_id.value,
        cluster_id: this.clusterForm.controls.cluster_id.value,
        ipam_prefix_id: this.prefixForm.controls.ipam_prefix_id.value,
        name: details.name,
        description: details.description || null,
      })
      .subscribe({
        next: (scope) => {
          this.notify.success(`Scope "${scope.name}" created.`);
          this.router.navigate(['/dhcp/scopes', scope.id]);
        },
        error: (err) => {
          this.submitting.set(false);
          this.errorMessage.set(this.notify.describe(err, 'Could not create scope.'));
        },
      });
  }
}
