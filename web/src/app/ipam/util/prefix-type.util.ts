import { PrefixType } from '../../shared/models';

export interface PrefixTypeMeta {
  readonly label: string;
  /** Shown as select help text and in the type legend on the add/edit
   * prefix dialog, per the "type select with help text" requirement — every
   * DHCP-adjacent concept in this UI explains itself. */
  readonly help: string;
  readonly chipClass: string;
}

export const PREFIX_TYPE_ORDER: readonly PrefixType[] = ['aggregate', 'container', 'subnet'];

export const PREFIX_TYPE_META: Readonly<Record<PrefixType, PrefixTypeMeta>> = {
  aggregate: {
    label: 'Aggregate',
    help: 'A broad address block used for high-level allocation planning, e.g. an RIR allocation or an org-wide supernet (10.0.0.0/8). Aggregates may contain containers or subnets, and may overlap with other aggregates.',
    chipClass: 'chip-aggregate',
  },
  container: {
    label: 'Container',
    help: 'A logical subdivision of an aggregate used to group related subnets — by site, department, or purpose. Containers may contain other containers or subnets.',
    chipClass: 'chip-container',
  },
  subnet: {
    label: 'Subnet',
    help: 'A leaf-level network block that hosts DHCP scopes and leases directly. Subnets cannot overlap with any other subnet in the same VRF.',
    chipClass: 'chip-subnet',
  },
};
