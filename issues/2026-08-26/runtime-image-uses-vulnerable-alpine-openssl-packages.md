# Runtime image retains vulnerable Alpine OpenSSL packages

## Status

Resolved in the runtime image build.

## Problem

The published agent image retained `libcrypto3` and `libssl3` at `3.5.7-r0`
from the pinned Alpine base image. Trivy reported CVE-2026-14456 as HIGH for
both packages; Alpine provides the fixed `3.5.8-r0` packages.

## Impact

The publish workflow failed its HIGH/CRITICAL Trivy gate.

## Resolution

Upgrade packages from the configured Alpine repositories before installing the
runtime dependencies, so the image receives security fixes released after the
pinned base image was built.

## Evidence

Failed workflow: [Publish agent image run 32963018936](https://github.com/envplane/agent/actions/runs/32963018936).
