# Report target storage providers without changing contracts

Status: implemented; Agent tests passed.

Codex prompt: keep storageClasses names backward-compatible and report the
actual provisioner/default annotation as storageClass.<name>.provisioner=<id>
and storageClass.<name>.default=true capability flags. Never infer Minikube from
the name standard. Preserve pagination/error handling and exclude the separate
envplane-local-path-storage system namespace from base application discovery.
No new RBAC is needed: the existing Agent already lists StorageClasses.
