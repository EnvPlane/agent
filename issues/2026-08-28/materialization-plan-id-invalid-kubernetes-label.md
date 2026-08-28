# Materialization plan ID is not a valid Kubernetes label value

The Secret materializer copied the canonical plan ID directly into the
`envplane.io/secret-plan` label. Canonical plan IDs contain `/` separators, so
the Kubernetes API rejected otherwise valid Secret server-side apply requests
with HTTP 422.

Use deterministic, Kubernetes-safe identifier hashes for the plan and item
labels. Keep the authoritative plan and item digests in annotations and add a
regression test with the production plan ID shape.
