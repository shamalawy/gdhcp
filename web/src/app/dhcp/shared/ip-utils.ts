/** IPv4/MAC parsing & validation helpers shared across the DHCP feature
 * (pool range validation, reservation-IP-in-range validation, CIDR checks
 * for scope/pool creation forms). Pure functions, no Angular dependency —
 * addresses are represented as unsigned 32-bit numbers via multiplication
 * (not bitwise ops) so values above 2^31 don't wrap through JS's signed
 * 32-bit bitwise operators. */

const IPV4_OCTET = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/;

/** Parses a dotted-quad IPv4 string to an unsigned 32-bit integer, or null
 * if the string isn't a valid IPv4 address. */
export function parseIPv4(ip: string): number | null {
  const parts = ip.trim().split('.');
  if (parts.length !== 4) return null;
  let value = 0;
  for (const part of parts) {
    if (!IPV4_OCTET.test(part)) return null;
    value = value * 256 + Number(part);
  }
  return value;
}

export function isValidIPv4(ip: string): boolean {
  return parseIPv4(ip) !== null;
}

export function formatIPv4(value: number): string {
  return [
    Math.floor(value / 16777216) % 256,
    Math.floor(value / 65536) % 256,
    Math.floor(value / 256) % 256,
    value % 256,
  ].join('.');
}

export interface ParsedCidr {
  network: number;
  prefixLen: number;
  /** Inclusive lowest address in the block (network address). */
  rangeStart: number;
  /** Inclusive highest address in the block (broadcast address). */
  rangeEnd: number;
}

export function parseCIDR(cidr: string): ParsedCidr | null {
  const [addr, lenStr] = cidr.trim().split('/');
  if (!addr || lenStr === undefined) return null;
  const prefixLen = Number(lenStr);
  if (!Number.isInteger(prefixLen) || prefixLen < 0 || prefixLen > 32) return null;
  const ip = parseIPv4(addr);
  if (ip === null) return null;
  const hostBits = 32 - prefixLen;
  const blockSize = hostBits === 32 ? 4294967296 : 2 ** hostBits;
  const network = Math.floor(ip / blockSize) * blockSize;
  return {
    network,
    prefixLen,
    rangeStart: network,
    rangeEnd: network + blockSize - 1,
  };
}

/** True if `ip` falls within the (inclusive) numeric range [start, end]. */
export function ipInNumericRange(ip: number, start: number, end: number): boolean {
  return ip >= start && ip <= end;
}

export function ipInRange(ip: string, rangeStart: string, rangeEnd: string): boolean {
  const ipN = parseIPv4(ip);
  const startN = parseIPv4(rangeStart);
  const endN = parseIPv4(rangeEnd);
  if (ipN === null || startN === null || endN === null) return false;
  return ipInNumericRange(ipN, startN, endN);
}

/** True if the [start, end] range is a non-empty, correctly-ordered range
 * that falls entirely inside the given CIDR block. */
export function rangeWithinCidr(rangeStart: string, rangeEnd: string, cidr: string): boolean {
  const startN = parseIPv4(rangeStart);
  const endN = parseIPv4(rangeEnd);
  const block = parseCIDR(cidr);
  if (startN === null || endN === null || !block) return false;
  if (startN > endN) return false;
  return startN >= block.rangeStart && endN <= block.rangeEnd;
}

export function compareIPv4(a: string, b: string): number {
  const aN = parseIPv4(a) ?? 0;
  const bN = parseIPv4(b) ?? 0;
  return aN - bN;
}

const MAC_RE = /^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$/;

export function isValidMac(mac: string): boolean {
  return MAC_RE.test(mac.trim());
}

export function normalizeMac(mac: string): string {
  return mac.trim().toLowerCase();
}

/** Number of usable addresses in a CIDR block (for display; includes
 * network/broadcast — the pool allocation logic excludes those per A6). */
export function cidrAddressCount(cidr: string): number {
  const block = parseCIDR(cidr);
  if (!block) return 0;
  return block.rangeEnd - block.rangeStart + 1;
}

/** Number of addresses in an inclusive [start, end] range; 0 if invalid or
 * inverted. */
export function rangeAddressCount(rangeStart: string, rangeEnd: string): number {
  const startN = parseIPv4(rangeStart);
  const endN = parseIPv4(rangeEnd);
  if (startN === null || endN === null || startN > endN) return 0;
  return endN - startN + 1;
}

/** Decode a base64 client identifier into lowercase hex.
 *
 * `client_id` is Go `[]byte` on the wire, so JSON carries it as base64. Hex is
 * what the reservation API canonicalises to and what operators recognise, so
 * base64 must never reach the UI or be submitted as a binding value — doing so
 * would hex-encode the base64 text itself and produce a key that never matches.
 * Returns the input unchanged if it is not decodable base64. */
export function clientIdToHex(clientId: string): string {
  try {
    const binary = atob(clientId);
    let hex = '';
    for (let i = 0; i < binary.length; i++) {
      hex += binary.charCodeAt(i).toString(16).padStart(2, '0');
    }
    return hex;
  } catch {
    return clientId;
  }
}
