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

**Session tokens last 24 hours.** That is the default and it is not
configurable. Once the 24 hours are up the session is gone — the next request
comes back `401` and the only way in is to log in again. There is no refresh
token and no "remember me".

### API Key (CI/CD / Integrations)

API keys are for non-interactive consumers — CI pipelines, Terraform, internal
tools:

```
Authorization: Bearer sharko_a1b2c3d4...
```

**API keys expire after 90 days by default.** Ask for a different window with
`expires_in_days` when you create one (anything from 1 to 365 days). A key that
has run out is refused with a `401` naming the key and saying it expired — you
can renew it or make a new one. Full details in
[Endpoints → API keys](endpoints.md#api-keys-tokens).

Create API keys via the CLI (`sharko token create`) or the Settings UI.

## Roles

| Role | Permissions |
|------|------------|
| `admin` | Everything: manage users, other people's API keys, connections, the AI provider, and the actions that remove things |
| `operator` | Day-to-day changes: register and adopt clusters, enable and disable addons, edit the catalog, create API keys. Cannot manage users or remove clusters and addons. |
| `viewer` | Read-only access to clusters, addons, health data, the activity history, and the list of users and API keys |

Each endpoint has a minimum role. Writes need `operator` or `admin`, and a few reach past the caller's own work and need `admin`. The full table is in [Security → Application Roles](../operator/security.md#application-roles). The server checks the role on every request, and a refusal is a `403` with a message that names the action and the role you have.

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

Authentication endpoints are rate-limited. Calls that change something are limited to 30 per minute per address; over that, Sharko answers HTTP 429 with the code "sharko_rate_limit", `Retry-After: 60` and "Sharko is getting too many requests from your address right now. Try again in a moment." When a Git host limits Sharko, the answer is HTTP 429 with the code "upstream_rate_limit" and "GitHub, or another service Sharko uses, says Sharko has asked too often. Try again in a moment." See [Security](../operator/security.md#rate-limiting) for trusted proxy configuration.
