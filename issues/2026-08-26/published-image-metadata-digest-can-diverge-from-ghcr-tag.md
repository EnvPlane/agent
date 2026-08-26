# Published image metadata digest can diverge from the GHCR tag

## Problem

The publish workflow forwards `docker/build-push-action`'s digest to deploy.
For a multi-platform image with provenance and SBOM attestations, the final GHCR
tag can resolve to a different index digest. Deploy then correctly rejects the
runtime pin because the supplied digest is not the digest served by GHCR.

Observed for agent commit `38a0bf50376d80346eac944b0cd967800366bd70`:

- dispatched digest: `sha256:be2a5c44e5a1b64673f21d16d6277afa97829dc6eaf4cadf5f730e8c0775217e`
- final tag digest: `sha256:d3dc474cd90d6cb2a214b8dbb80dd522e80492ef8af0e2cadb4461587583ff96`

## Resolution

Resolve and validate the final immutable tag digest from GHCR after the image
scan, and use that value for artifacts and downstream dispatch.

