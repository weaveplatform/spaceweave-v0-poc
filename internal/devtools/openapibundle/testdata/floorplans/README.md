# Floorplans contract fixture

A small Floorplans OpenAPI surface for bundler tests. The collection and resource
fragments share a Floorplan schema and a common problem response. This verifies
cross-fragment references, deterministic ordering and preservation of comments.
The expected result is `../golden/floorplans.openapi.yaml`.

This Markdown file also verifies that the bundler ignores non-YAML files.
The fixture is independent of the supported product contract in `api/src/api/`.
