# Public Go packages

Packages in this directory may be imported by other repositories. Business
implementation remains in `internal/`.

| Import suffix | Purpose |
| --- | --- |
| `pkg/sdk` | Typed product and operator operations, independent credentials, pagination, multipart uploads, streaming downloads and problem errors. |
| `pkg/client/api` | Generated product client and wire types for `/api/v1`. |
| `pkg/client/operator` | Generated installation client and wire types for `/operator/v1`. |

The full module prefix is `github.com/weaveplatform/spaceweave`. Prefer the SDK
for ordinary integrations. Construct it with a server origin:

```go
client, err := sdk.New("http://127.0.0.1:8000",
    sdk.WithToken(productToken),
    sdk.WithOperatorToken(operatorToken),
)
```

Handle `err` before calling the client. Product and operator credentials are
independent; omit the product token for the default local browser setup.
Callers close successful artifact and attachment response bodies. The SDK bounds
safe GET retries and never retries writes automatically. `*sdk.Problem` supports
`errors.As` for structured HTTP failure handling.

Generated clients expose the wire contract directly; they do not add the SDK's
credential scoping or convenience behaviour. Regenerate them with `make generate`
and check freshness with `make verify-generate`; do not edit `.gen.go` files.
See [REST APIs](../docs/rest-apis.md) for endpoints, credentials, CLI examples and
current local-storage limitations.
