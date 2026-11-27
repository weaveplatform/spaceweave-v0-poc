# Product and operator REST APIs

SpaceWeave exposes two independently versioned HTTP contracts on the same
listener. The **product API** serves the browser, remote CLI, Go SDK and other
business consumers. The **operator API** serves installation diagnostics; it
has no floorplan approval, upload or other business mutation operations.

| Surface | Base path | Current operations | Contract bundle |
| --- | --- | --- | --- |
| Product | `/api/v1` | Floorplan import, review, files, artifacts, processing state and layer profiles. | [api.openapi.yaml](../api/api.openapi.yaml) |
| Operator | `/operator/v1` | `GET /runtime`: version, tracer, configured worker concurrency and actual counts by plan state. | [operator.openapi.yaml](../api/operator.openapi.yaml) |

The implementation remains the local Floorplans workbench. Staff SSO, Entra,
scoped enterprise roles, distributed jobs and the wider workplace-booking
platform have not been implemented by these contracts.

## Credentials and local mode

The local browser workbench runs without a product bearer token by default.
The server accepts credentials only through its environment:

| Server environment variable | Meaning |
| --- | --- |
| `SPACEWEAVE_API_TOKEN` | Optional product bearer token. When configured, product requests, legacy API routes and all portal file reads require it. |
| `SPACEWEAVE_OPERATOR_TOKEN` | Separate operator bearer token. Operator access always requires its configured credential; tokenless product mode does not enable operator access. |

Use separate values for the two credentials. Product and operator clients send
`Authorization: Bearer …` only to their corresponding surface. Tokens are not
CLI flags or YAML settings. The host allowlist still applies to both surfaces;
listening on another interface requires an explicitly allowed host name.
Product writes also carry `X-Spaceweave: 1`. The browser applies this header;
the SDK and remote CLI apply it automatically. It is a local browser mutation
guard, not a user identity or replacement for bearer authentication.

With a product token configured, the static file-serving fallback is protected too, including encoded or filesystem-aliased generated paths.

The browser currently uses local tokenless mode; it does not provide a bearer-token
login UI. Token-protected installations can use the SDK, remote CLI or another
client that supplies the configured credential. These installation tokens do
not implement staff identity, office scopes or Entra authentication.

## Product endpoints

Paths below are relative to `/api/v1`.

| Method and path | Behaviour |
| --- | --- |
| `GET /info` | Runtime version/tracer, accepted formats, upload limit and layer-profile capability. |
| `GET /floorplans?limit=200&cursor=…` | Page of `{plans, nextCursor?}`. Pass the returned cursor unchanged for the next page. |
| `GET /floorplans/{id}` | One floorplan's metadata and current state. |
| `POST /floorplans` | Multipart upload with field `image`; returns `{plan, duplicate}`. |
| `POST /floorplans/{id}/approve` | Approve a draft; returns `{plan, was}`. |
| `POST /floorplans/{id}/reject` | Send a draft or approved plan back with `{note}`. |
| `POST /floorplans/{id}/retry` | Requeue a failed attempt. |
| `POST /floorplans/{id}/retrace` | Requeue a draft or failed plan using current source files and saved profiles. |
| `GET /floorplans/{id}/processing` | Current-attempt `{planId, attempt, status, progress?, error?, updatedAt}`. |
| `POST /floorplans/{id}/files` | Multipart attachment with fields `file` and `role` (`spaces` or `reference`). |
| `DELETE /floorplans/{id}/files/{sha}` | Detach an attached file by its complete SHA-256. |
| `GET /floorplans/{id}/files/{sha}` | Stream the attached file. |
| `GET /floorplans/{id}/artifacts/{artifact}` | Stream `model`, `trace`, `report`, `source`, `overlay`, `drawing` or `original`. |
| `GET /layer-profiles/{name}` | Read a built-in or client profile and its role metadata. |
| `PUT /layer-profiles/{name}` | Replace a client profile using the contract's JSON body. Built-ins are read-only. |

An artifact that has not been produced is unavailable; reading it does not start
processing. Model `SOURCE.url` references the versioned source-artifact route.
The processing endpoint describes the current attempt rather than an immutable
operation history. Upload and review outcomes retain the existing local
catalogue state machine and file formats.

Canonical errors use `application/problem+json` with `type`, `title`, `status`
and `detail`. Clients should inspect the HTTP status and problem details rather
than parse a human-readable error string.

For example, a tokenless local list request is:

```sh
curl --fail-with-body 'http://127.0.0.1:8000/api/v1/floorplans?limit=200'
```

## Go clients and remote CLI

Public imports are available under `github.com/weaveplatform/spaceweave/pkg/`:

| Package | Purpose |
| --- | --- |
| `sdk` | Convenient typed operations, multipart uploads, streaming downloads, pagination, problem errors and separate credentials. |
| `client/api` | Generated low-level product client and wire models. |
| `client/operator` | Generated low-level operator client and wire models. |

Create the SDK with an **origin**, such as `http://127.0.0.1:8000`, rather than
an `/api/v1` path. `sdk.New` accepts `sdk.WithToken` and
`sdk.WithOperatorToken` separately. It exposes `GetInfo`, `ListFloorplans`, the
paginated `Floorplans` iterator, `GetFloorplan`, `UploadFloorplan`,
`ReviewFloorplan`, attachment operations, `GetProcessing`, profile operations,
streaming `GetArtifact`/`DownloadFile`, and operator `GetRuntime`. Callers close
successful streaming response bodies. Returned `*sdk.Problem` errors support
`errors.As`.

The SDK bounds safe GET retries and respects its configured backoff limits.
**Writes are not automatically retried.** The local store has no durable command
receipts or general idempotency-key contract. Upload deduplication by content is
not a general retry guarantee for review, attachment or profile writes. After a
lost write response, inspect the resource/current attempt before explicitly
submitting another command.

`spaceweavectl` is a remote REST client. It does not read the server's local
catalogue or run a tracer. Its environment is:

| Client environment variable | Meaning |
| --- | --- |
| `SPACEWEAVECTL_ENDPOINT` | Server origin; `--endpoint` may select it explicitly. |
| `SPACEWEAVECTL_TOKEN` | Product credential, if the server requires one. |
| `SPACEWEAVECTL_OPERATOR_TOKEN` | Independent operator credential. |

Examples from the repository root:

```sh
go run ./cmd/spaceweavectl --endpoint http://127.0.0.1:8000 info
go run ./cmd/spaceweavectl floorplans list --all
go run ./cmd/spaceweavectl floorplans get FLOORPLAN_ID
go run ./cmd/spaceweavectl floorplans upload ./office.dwg
go run ./cmd/spaceweavectl floorplans processing FLOORPLAN_ID
go run ./cmd/spaceweavectl floorplans approve FLOORPLAN_ID
go run ./cmd/spaceweavectl floorplans artifact FLOORPLAN_ID model --output ./model.json
go run ./cmd/spaceweavectl layer-profiles get generic
go run ./cmd/spaceweavectl operator runtime
```

Set `SPACEWEAVECTL_ENDPOINT` for examples without `--endpoint`. Commands return
JSON; artifact/file downloads stream bytes to stdout unless `--output` is used.
Uploads can read stdin with `-` and an explicit `--name`. Credentials stay in
environment variables, never command flags. The existing `spaceweave` executable
remains the local server, watcher, converter, tracer and local review tool.

## Contract generation

Edit fragments under [`api/src/api`](../api/src/api) or
[`api/src/operator`](../api/src/operator), then run:

```sh
make generate
make verify-generate
```

The bundle step validates and assembles the two OpenAPI contracts. Generation
produces strict Chi server interfaces under `internal/transport/gen` and public
Go clients under `pkg/client`. Generated files and bundles are checked in;
`make verify-generate` detects stale output, and `make gate` includes the check.
Keep business implementation in owning modules and adapters rather than editing
generated code.

Executable [acceptance features](../test/acceptance/README.md) cover the product,
operator, credential and remote CLI behaviours against the real binaries.
`make acceptance`, `make gate` and CI run them in strict Godog mode.

The Docker image uses the repository's Go toolchain and Node 22. Node runs the
shared JavaScript plan validator; no npm packages or frontend build are needed.

## Compatibility and static viewing

Existing `/api/info`, `/api/plans`, review/attachment paths and `/api/profiles`
remain local compatibility routes. Their legacy JSON responses and profile input
behaviour are not the versioned contract. New automation should use `/api/v1`.
The versioned operator API has no legacy business-route alias.

Generated files remain available for the existing local workflow, under the
product credential policy when the Go server is used. The normal browser uses
REST for the imported-floor list, models, reports, attachments and evidence.
A plain static server such as `tools/serve.py` can still display examples and
watched generated plans. The browser enters that mode only after the info route
returns a bare/HTML `404`; an authenticated API's problem response, `401`, `403`,
server failure or network error never triggers file fallback. Static viewing has
no import, approval or profile writes and is not an alternative authentication
path.
