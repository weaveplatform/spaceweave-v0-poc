# Acceptance tests

`make acceptance` builds coverage-instrumented `spaceweave` and `spaceweavectl`
binaries and runs both the Go acceptance tests and executable Gherkin features.
It is included in `make gate` and the CI acceptance job. Node is required for the
real plan validator. No external model API or database is needed.

Following InfraWeave's feature layout, scenarios are grouped under `features/`:

| Area | Executable behaviour |
| --- | --- |
| `api/` | Product imports, duplicate detection, request validation and problem details. |
| `auth/` | Independent surface credentials, legacy protection and file-path aliases. |
| `cli/` | Real upload, processing, model download, approval and operator counts through the remote CLI. |
| `operator/` | Runtime diagnostics, disabled access and CLI authorization failures. |

`features_test.go` binds the steps with Godog in strict mode: undefined or pending
steps fail the test. Every scenario starts an isolated server and catalogue and
stops its process before the next scenario. The process shutdown writes coverage
for the ordinary coverage gate. Scenario outlines execute every example row.

Run only the features, with their step output:

```sh
GOWORK=off go test -count=1 -run TestAcceptanceFeatures -v ./test/acceptance
```

The existing Go acceptance tests continue to cover local watch/trace/review,
CAD and raster processing, profiles and drawing packages. The feature suite
covers the new REST and remote CLI behaviour; it does not replace those checks.
The process harness currently needs interrupt signals, so server tests run on
Linux and macOS; the CI acceptance job runs on Linux.
