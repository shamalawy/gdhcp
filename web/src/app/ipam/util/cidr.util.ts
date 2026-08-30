/**
 * Pure IPv4 CIDR helpers — no external dependency, matching AMENDMENTS.md A9
 * ("no chart library"; the frontend generally hand-rolls small pieces of
 * domain logic rather than pulling in dependencies). gdhcp is DHCPv4-only
 * per docs/design/dhcp-engine.md, so IPv6 is intentionally out of scope.
 */

export interface ParsedCidr {
  /** Network address, as an unsigned 32-bit integer (already masked). */
  readonly network: number;
  readonly prefixLen: number;
}

const OCTET = '(25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)';
const IPV4_RE = new RegExp(`^${OCTET}\\.${OCTET}\\.${OCTET}\\.${OCTET}$`);

/** Parses a dotted-quad IPv4 address into an unsigned 32-bit integer, or
 * `null` if `value` is not a well-formed IPv4 address. */
export function ipv4ToInt(value: string): number | null {
  const match = IPV4_RE.exec(value.trim());
  if (!match) return null;
  const [, a, b, c, d] = match;
  return ((Number(a) << 24) | (Number(b) << 16) | (Number(c) << 8) | Number(d)) >>> 0;
}

function maskFor(prefixLen: number): number {
  return prefixLen === 0 ? 0 : (0xffffffff << (32 - prefixLen)) >>> 0;
}

/** Parses `"a.b.c.d/n"` into a masked network address + prefix length, or
 * `null` if malformed (bad octets, prefix length outside 0-32, missing
 * slash). Does NOT require the input to already be on a network boundary —
 * the address is masked down to one, matching how most IPAM tools treat
 * "10.1.0.5/24" as shorthand for "10.1.0.0/24". */
export function parseCidr(cidr: string): ParsedCidr | null {
  const trimmed = cidr.trim();
  const slash = trimmed.indexOf('/');
  if (slash < 0) return null;
  const ipPart = trimmed.slice(0, slash);
  const lenPart = trimmed.slice(slash + 1);
  if (!/^\d{1,2}$/.test(lenPart)) return null;
  const prefixLen = Number(lenPart);
  if (prefixLen < 0 || prefixLen > 32) return null;
  const ip = ipv4ToInt(ipPart);
  if (ip === null) return null;
  return { network: (ip & maskFor(prefixLen)) >>> 0, prefixLen };
}

export function isValidCidr(cidr: string): boolean {
  return parseCidr(cidr) !== null;
}

/** True if `childCidr` is fully contained within (or equal to) `parentCidr`.
 * Used only for a client-side heads-up in the prefix form — actual nesting
 * is computed server-side from real containment (see
 * notes_for_integration: `CreatePrefixRequest` has no `parent_id` field). */
export function cidrContains(parentCidr: string, childCidr: string): boolean {
  const parent = parseCidr(parentCidr);
  const child = parseCidr(childCidr);
  if (!parent || !child) return false;
  if (child.prefixLen < parent.prefixLen) return false;
  return (child.network & maskFor(parent.prefixLen)) >>> 0 === parent.network;
}

/** Total address count for a given prefix length (e.g. /24 -> 256). */
export function addressCountForPrefixLen(prefixLen: number): number {
  return 2 ** (32 - prefixLen);
}

/** Total address count for a CIDR string, or `null` if malformed. */
export function addressCountForCidr(cidr: string): number | null {
  const parsed = parseCidr(cidr);
  return parsed ? addressCountForPrefixLen(parsed.prefixLen) : null;
}
