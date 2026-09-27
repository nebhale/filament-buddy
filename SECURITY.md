# Security

Use this application on a trusted LAN or behind an authenticated HTTPS reverse
proxy. Optional Basic authentication is configured with environment variables;
without it, anyone who can reach the HTTP port can edit consumption. Mutation
forms use CSRF protection in either mode. Preserve the application at URL root.

UDP printer markers are not authenticated. Filter the original printer source at
the relay and expose consumer ports only on loopback. If setting `source_ip`,
use the source actually observed by that consumer, not the printer's original IP.

Keep YAML and environment files containing credentials private. Do not expose
Spoolman credentials to browsers or commit them to Git. Report vulnerabilities
privately through [GitHub security advisories](https://github.com/nebhale/filament-buddy/security/advisories/new).
Include the affected version, a minimal reproduction, and the observed impact.
Do not include credentials, printer addresses, or personal inventory data in
public issues. There is no guaranteed response time.

Security fixes target the latest stable release. Dependabot monitors Go modules,
Docker base-image references, and GitHub Actions. Updates are reviewed manually.
