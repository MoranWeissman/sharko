# Full API Reference (OpenAPI)

This is the complete, interactive reference for the Sharko REST API, rendered
straight from the live OpenAPI specification. It is the same surface you get
from the running server's Swagger UI at `/swagger/`, so every endpoint, request
body, and response shape below matches what your Sharko instance actually
serves.

!!! note "Always in sync"
    This page is generated from the OpenAPI spec that `swag` produces from the
    server's own annotations (`docs/swagger/swagger.json`). It is never a
    hand-maintained copy, so it can't drift from the real API.

Looking for hands-on, copy-paste examples instead of the raw schema? See the
[API Walkthrough](api-walkthrough.md), which drives the same endpoints with
ready-to-run `curl` commands.

<redoc spec-url="../openapi.json"></redoc>
<script src="https://cdn.redoc.ly/redoc/latest/bundles/redoc.standalone.js"></script>
