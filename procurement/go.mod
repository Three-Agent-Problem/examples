module github.com/three-agent-problem/examples/procurement

go 1.26.0

require (
	github.com/a2aproject/a2a-go/v2 v2.7.1-0.20261027131848-2692308d0926
	github.com/google/jsonschema-go v0.4.3
	github.com/three-agent-problem/registry v0.0.0
	github.com/three-agent-problem/lagrange v0.0.0
)

require (
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
)

// The org's modules are not tagged yet. These point at sibling checkouts laid out by
// the workspace's scripts/bootstrap.sh (org/registry, org/lagrange). Drop them once
// registry and runtime have releases.
replace (
	github.com/three-agent-problem/registry => ../../registry
	github.com/three-agent-problem/lagrange => ../../lagrange
)
