# API Overview

The Sharko REST API is the single interface that every consumer uses: the CLI, the dashboard UI, Backstage plugins, Terraform providers, and CI/CD pipelines.

## Base URL

```
https://sharko.your-domain.com/api/v1
```

In demo mode: `http://localhost:8080/api/v1`

## Interactive Documentation

The full API is documented in Swagger UI, available at `/swagger/index.html` on any running Sharko instance:

```
https://sharko.your-domain.com/swagger/index.html
```

The Swagger UI lets you explore all endpoints, view request/response schemas, and execute API calls directly from the browser.

## Authentication

All endpoints except `/api/v1/health` require authentication.

### Session Token (UI / CLI)

Obtain a session token via login:

```bash
POST /api/v1/auth/login
Content-Type: application/json

{"username": "admin", "password": "your-password"}
```

Response includes a JWT token. Pass it as a Bearer token:

```
Authorization: Bearer <token>
```

Session tokens expire after 24 hours. Re-authenticate to get a new token.

### API Key (CI/CD / Integrations)

API keys do not expire and are suitable for non-interactive consumers:

```
Authorization: Bearer sharko_a1b2c3d4...
```

Create API keys via the CLI (`sharko token create`) or the Settings UI.

## Roles

| Role | Permissions |
|------|------------|
| `admin` | Full read/write access, manage users, API keys, and connections |
| `viewer` | Read-only access to clusters, addons, and health data |

Write endpoints require the `admin` role.

## Response Format

All responses are JSON. Successful responses use HTTP 2xx. Errors use standard HTTP error codes with a JSON body:

```json
{
  "error": "cluster not found",
  "code": "NOT_FOUND"
}
```

## Health Check

No authentication required:

```bash
GET /api/v1/health
# Response: {"status": "ok"}
```

Use this endpoint for liveness probes and external monitoring.

## Rate Limiting

Authentication endpoints are rate-limited. Other endpoints are not currently rate-limited. See [Security](../operator/security.md#rate-limiting) for trusted proxy configuration.
