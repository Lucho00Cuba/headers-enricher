# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly.

**Do NOT report security vulnerabilities through public GitHub Issues.**

Instead, please email the maintainer directly or use GitHub's private vulnerability reporting.

## Scope

This project is a Traefik middleware plugin. Security considerations:

- **Environment Variables**: Only explicitly listed variables in `allowedEnv` are exposed to templates. No secrets are exposed by default.
- **Header Processing**: The plugin processes HTTP headers. Ensure proper network isolation in production.
- **Template Execution**: Templates run in a sandboxed context with limited access to data.

## Best Practices

- Keep `allowedEnv` list minimal
- Do not expose sensitive values in headers
- Use TLS/SSL in production
