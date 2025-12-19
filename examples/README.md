# kube-parcel Examples

Example Helm charts demonstrating how to use kube-parcel for integration testing.

## Quick Start

```bash
# Build and load the runner image
bazel run //cmd/runner:load

# Run an example
kube-parcel start ./examples/microservice
```

## Examples

### `microservice/`
A multi-replica nginx deployment with service discovery testing.

**What it tests:**
- Deployment with 2 replicas
- Service routing to pods
- HTTP connectivity between pods

```bash
kube-parcel start ./examples/microservice
```

---

### `configmap-app/`
Demonstrates ConfigMap injection and environment variable validation.

**What it tests:**
- ConfigMap creation
- Environment variable injection via `envFrom`
- Validates specific config values are present

```bash
kube-parcel start ./examples/configmap-app
```

---

### `client-pod-rbac.yaml`
RBAC configuration for running kube-parcel in Kubernetes mode.

**What it provides:**
- ServiceAccount with appropriate permissions
- Role and RoleBinding for pod management
- Example client pod configuration

```bash
kubectl apply -f examples/client-pod-rbac.yaml
```

## Running with Images

If your chart uses custom images, bundle them:

```bash
kube-parcel start --load-images ./myimage.tar ./examples/microservice
```

## K8s Mode

Run tests inside a Kubernetes cluster instead of Docker:

```bash
kube-parcel start --exec-mode k8s ./examples/microservice
```
