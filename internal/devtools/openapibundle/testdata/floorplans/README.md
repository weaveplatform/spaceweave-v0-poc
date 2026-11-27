# Floorplans contract fixture

A small Floorplans OpenAPI surface for bundler tests. `paths-floorplans.yaml`
holds collection and resource routes, and `paths-layer-profiles.yaml` holds a
separate API area. Floorplan routes reference common problem responses in
`components-common.yaml`. This verifies cross-fragment references, deterministic
ordering and preservation of comments. The expected result is
`../golden/floorplans.openapi.yaml`.

This Markdown file also verifies that the bundler ignores non-YAML files.
The fixture is independent of the supported product contract in `api/src/api/`.
