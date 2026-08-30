# Security Policy

## Supported versions

| Version | Supported | EOL |
| --- | --- | --- |
| 0.1.x | Yes | TBD (post-1.0 release) |
| < 0.1.0 | No | N/A |

This is a pre-1.0 release. API and database schema may change before 1.0. Security updates will be available as 0.1.x patch releases.

## Reporting a vulnerability

**Do not open a public GitHub issue for security vulnerabilities.** Instead, use GitHub Security Advisories to report privately.

1. Visit https://github.com/shamalawy/gdhcp/security/advisories
2. Click "Report a vulnerability"
3. Provide:
   - Description of the vulnerability
   - Affected version(s)
   - Steps to reproduce (if applicable)
   - Impact assessment
   - Any suggested fixes

Expected response time: within 7 days of report submission. We will work with you to confirm, assess, and develop a fix. You will be credited in the advisory unless you prefer anonymity.

## Security model

### Authentication and authorization

- **API access** requires JWT session or long-lived API token (bearer token)
- **RBAC roles**: admin (full access), operator (all CRUD except users/tokens/cluster config), viewer (read-only)
- **Default admin credential** (`admin`/`admin`) is seeded in migration 0003_seed_admin.sql for first login
  - **There is currently no in-product way to change this password**
  - See [README.md "Security note: default credentials"](README.md#security-note-default-credentials) for mitigation strategies

### DHCP surface

- DHCP server listens on configured interfaces and responds to unauthenticated broadcast DHCP traffic (standard DHCPv4)
- DHCP protocol compliance is limited to RFC 2131 discovery and standard option handling; no protocol-level authentication is available
- Client identity (MAC, client-ID, hostname, circuit-ID, remote-ID, vendor class, user class) is extracted from wire traffic and used for:
  - Lease allocation (with deterministic precedence per AMENDMENTS.md A17.3)
  - Reservation matching
  - Audit logging

Mitigate DHCP spoofing and client misidentification at the network layer:

- Restrict DHCP listeners to trusted subnets via interface/VRF binding in config
- Use DHCP snooping on network switches to prevent rogue DHCP servers
- Rely on relay agents with circuit-ID/remote-ID for cross-subnet identification (more trustworthy than MAC or client-ID alone)

### API and UI exposure

- API listens on `server.api_addr` (default `0.0.0.0:8080`)
- Web UI is served from the same address at `/` with SPA routing fallback
- **Protect `server.api_addr` from untrusted networks:**
  - Firewall to localhost or trusted management subnets
  - Place behind a reverse proxy with additional auth (e.g. LDAP, SAML, OAuth2)
  - Use VPN for remote access
  - Enable TLS (api_tls_enabled + certificates) for encrypted transport

### Metrics endpoint

- Prometheus metrics are exposed at `GET /metrics` on the configured metrics port (separate from API)
- Intentionally **not bearer-token protected** to allow scraping by Prometheus without credential handling
- IP-allowlist to loopback (127.0.0.1, ::1) and private ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) by default
- If metrics need to be exposed to untrusted networks, place them behind a firewall rule or reverse proxy that filters by source IP

### Database

- PostgreSQL connection uses plaintext passwords in the DSN by default (suitable for local/private networks)
- Enable `sslmode=require` in the DSN for encrypted database transport over untrusted networks
- Database user (`gdhcp` by default) should have minimal permissions:
  - Tables: full CRUD on gdhcp schema
  - No access to other databases or PostgreSQL roles
- Connection pooling enforces `max_open_conns` and `max_idle_conns` (see config.yaml.example)

### Configuration secrets

- Sensitive values in config (database DSN, JWT secret, TLS keys) must never be committed to git
- Use environment variables (GDHCP_* prefix) or external secret injection (Kubernetes Secrets, systemd Environment files)
- See `.gitignore` and deployment guides (docs/deploy/*.md) for examples

### Deployment-specific mitigations

- **Docker**: runs as non-root, has capabilities for network operations (CAP_NET_ADMIN, CAP_NET_RAW for VIP/ARP)
- **Kubernetes**: DaemonSet with hostNetwork=true (required for DHCP), secrets injected via overlay kustomization
- **Bare metal**: systemd service runs as `gdhcp` user (unprivileged), requires CAP_NET_ADMIN/CAP_NET_RAW at bind time

See the relevant deployment guide in docs/deploy/ for hardening instructions.

## Known limitations

- **Password reset**: there is no password-change endpoint. `PUT /api/v1/users/{id}` accepts no `password` field, so an existing account's password cannot be changed in-product. The supported mitigation for the seeded `admin`/`admin` account is to create a replacement admin (`POST /api/v1/users` does take a password) and then deactivate user 1 — see [docs/MANUAL.md](docs/MANUAL.md) section 3.1
- **Multi-tenancy**: gdhcp is single-tenant (one VRF hierarchy, one admin scope); multi-org isolation must be provided at deployment level (separate instances or reverse proxy routing)
- **Audit logging**: audit trail exists for RBAC-protected mutations but is not cryptographically signed or tamper-evident; suitable for compliance audits on trusted infrastructure only

## Security audits

This is a pre-1.0 release with limited production mileage. No third-party security audit has been conducted. If you plan to deploy gdhcp in a sensitive environment, consider:

- Source code review for correctness and attack surface
- Penetration testing in your deployment context
- Static analysis (go vet, go security scanner)

Report findings to the maintainers via the Security Advisory process above.
