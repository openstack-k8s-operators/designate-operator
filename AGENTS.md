# AGENTS.md - designate-operator

## Project overview

designate-operator is a Kubernetes operator that manages
[OpenStack Designate](https://docs.openstack.org/designate/latest/) (the
DNS-as-a-Service: DNS zone management, record sets, multi-tenant DNS, and
BIND9 backend) on OpenShift/Kubernetes. It is part of the
[openstack-k8s-operators](https://github.com/openstack-k8s-operators) project.

Key Designate domain concepts: **DNS zones**, **record sets**, **stub zones**,
**NS records**, **BIND9 backend**, **unbound** (DNS resolver), **MDNS**
(mini DNS for zone transfers), **pools** (backend grouping and configuration).
## Tech stack

| Layer | Technology |
|-------|------------|
| Language | Go (modules, multi-module workspace via `go.work`) |
| Scaffolding | [Kubebuilder v4](https://book.kubebuilder.io/) + [Operator SDK](https://sdk.operatorframework.io/) |
| CRD generation | controller-gen (DeepCopy, CRDs, RBAC, webhooks) |
| Config management | Kustomize |
| Packaging | OLM bundle |
| Testing | Ginkgo/Gomega + envtest (functional), KUTTL (integration) |
| Linting | golangci-lint (`.golangci.yaml`) |
| CI | Zuul (`zuul.d/`), Prow (`.ci-operator.yaml`), GitHub Actions |

## Custom Resources

| Kind | Purpose |
|------|---------|
| `Designate` | Top-level CR. Owns the database, keystone service, transport URL, and spawns sub-CRs for each service component. |
| `DesignateAPI` | Manages the Designate API deployment. |
| `DesignateCentral` | Manages the central service (core business logic). |
| `DesignateWorker` | Manages the worker service (zone operations on backends). |
| `DesignateMdns` | Manages the mini-DNS service (AXFR/IXFR zone transfers). |
| `DesignateProducer` | Manages the producer service (periodic tasks, zone purging). |
| `DesignateBackendbind9` | Manages the BIND9 backend deployment. |
| `DesignateUnbound` | Manages the Unbound DNS resolver deployment. |

The `Designate` CR has defaulting and validating admission webhooks.
Sub-CRs are created and owned by the `Designate` controller -- not intended to
be created directly by users.

## Directory structure

**Maintenance rule:** when directories are added, removed, or renamed, or when
their purpose changes, update this table to match.

| Directory | Contents |
|-----------|----------|
| `api/v1beta1/` | CRD types (`designate_types.go`, `designateapi_types.go`, `designatebackendbind9_types.go`, `designatecentral_types.go`, `designatemdns_types.go`, `designateproducer_types.go`, `designateunbound_types.go`, `designateworker_types.go`), conditions, webhook markers |
| `cmd/` | `main.go` entry point |
| `internal/controller/` | Reconcilers: `designate_controller.go`, `designateapi_controller.go`, `designatebackendbind9_controller.go`, `designatecentral_controller.go`, `designatemdns_controller.go`, `designateproducer_controller.go`, `designateunbound_controller.go`, `designateworker_controller.go` |
| `internal/designate/` | Designate-level resource builders (db-sync, common helpers) |
| `internal/designateapi/` | DesignateAPI resource builders |
| `internal/designatebackendbind9/` | DesignateBackendbind9 resource builders |
| `internal/designatecentral/` | DesignateCentral resource builders |
| `internal/designatemdns/` | DesignateMdns resource builders |
| `internal/designateproducer/` | DesignateProducer resource builders |
| `internal/designateunbound/` | DesignateUnbound resource builders |
| `internal/designateworker/` | DesignateWorker resource builders |
| `internal/webhook/` | Webhook implementation |
| `templates/` | Config files and scripts mounted into pods via `OPERATOR_TEMPLATES` env var |
| `config/crd,rbac,manager,webhook/` | Generated Kubernetes manifests (CRDs, RBAC, deployment, webhooks) |
| `config/samples/` | Example CRs (Kustomize overlays). Includes multipool config map. |
| `test/functional/` | envtest-based Ginkgo/Gomega tests |
| `test/kuttl/` | KUTTL integration tests |
| `hack/` | Helper scripts (CRD schema checker, local webhook runner) |
| `demo/` | Demo resources and examples |

## Build commands

After modifying Go code, always run: `make generate manifests fmt vet`.

## Code style guidelines

- Follow standard openstack-k8s-operators conventions and lib-common patterns.
- Use `lib-common` modules for conditions, endpoints, TLS, storage, and other
  cross-cutting concerns rather than re-implementing them.
- CRD types go in `api/v1beta1/`. Controller logic goes in
  `internal/controller/`. Resource-building helpers go in `internal/designate*`
  packages matching the CR they support.
- Config templates are plain files in `templates/` -- they are mounted at
  runtime via the `OPERATOR_TEMPLATES` environment variable.
- Webhook logic is split between the kubebuilder markers in `api/v1beta1/` and
  the implementation in `internal/webhook/`.

## Testing

- Functional tests use the envtest framework with Ginkgo/Gomega and live in
  `test/functional/`.
- KUTTL integration tests live in `test/kuttl/`.
- Run all functional tests: `make test`.
- When adding a new field or feature, add corresponding test cases in
  `test/functional/` and update fixture data accordingly.

## Key dependencies

- [lib-common](https://github.com/openstack-k8s-operators/lib-common): shared modules for conditions, endpoints, database, TLS, secrets, etc.
- [infra-operator](https://github.com/openstack-k8s-operators/infra-operator): RabbitMQ and topology APIs.
- [mariadb-operator](https://github.com/openstack-k8s-operators/mariadb-operator): database provisioning.
- [keystone-operator](https://github.com/openstack-k8s-operators/keystone-operator): identity service registration.
- [gophercloud](https://github.com/gophercloud/gophercloud): Go OpenStack SDK.
