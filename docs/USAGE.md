# kube-parcel Usage Guide

**kube-parcel** is a CI-first integration testing tool for Helm charts. It runs a full K3s Kubernetes cluster inside a container—either locally via a Docker-compatible daemon, or as Pods spawned in an existing Kubernetes cluster.

By bundling your charts and images into a self-contained "parcel" and streaming them directly into the ephemeral K3s environment, kube-parcel bypasses container registries entirely, enabling true airgapped testing in CI pipelines.

## Quick Start

```bash
# Build and load the runner image
bazel build //cmd/runner:image //cmd/client
bazel run //cmd/runner:load

# Run a basic test
bazel-bin/cmd/client/client_/client start ./path/to/helm-chart
```

## CLI Commands

### `start` - Launch and Test

The `start` command is the primary way to test Helm charts. It:
1. Launches a K3s container (locally or remotely)
2. Bundles and streams your charts and images
3. Installs charts and runs tests
4. Reports results

```bash
kube-parcel start [flags] <chart-path> [chart-path...]
```

#### Flags

**General Flags:**

| Flag | Description | Default |
|------|-------------|---------|
| `--exec-mode` | Execution mode: `docker` (local) or `k8s` (Kubernetes) | `docker` |
| `--load-images` | Image tars or OCI directories to load into the cluster | - |
| `--runner-image` | Runner image to use | `ghcr.io/tiborv/kube-parcel-runner:v0.0` |
| `--keep-alive` | Keep container running after tests complete | `false` |
| `--no-airgap` | Allow K3s to pull images from external registries | `false` |

**Kubernetes Mode Flags** (only apply when `--exec-mode k8s`):

| Flag | Description | Default |
|------|-------------|---------|
| `--namespace` | Kubernetes namespace | `default` |
| `--cpu` | CPU limit for Pod | - |
| `--memory` | Memory limit for Pod | - |
| `--labels` | Labels for Pod (k=v,k=v) | - |
| `--annotations` | Annotations for Pod (k=v,k=v) | - |
| `--host-pid` | Use host PID namespace for nested container support | `true` |

#### Permissions
 
 When running in Kubernetes mode, the **kube-parcel client** (running inside your CI pod) assumes it has permission to **spawn and execute pods** in the target namespace.
 
 Generally, if your CI job can run `kubectl run` or `kubectl exec`, the **kube-parcel client** has sufficient permissions. Specifically, it needs `create`, `get`, `list`, `watch`, and `delete` on `pods`, and permission to `create` `pods/exec` and `pods/portforward`.
 
 The **runner pod** itself is fully isolated and does **not** require any access to the host cluster's API server.

**Example: Client Pod with RBAC**

```yaml
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-parcel-client
  namespace: testing
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kube-parcel-client
  namespace: testing
rules:
  - apiGroups: [""]
    resources: ["pods", "pods/log", "pods/exec", "pods/portforward"]
    verbs: ["create", "get", "list", "watch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kube-parcel-client
  namespace: testing
subjects:
  - kind: ServiceAccount
    name: kube-parcel-client
    namespace: testing
roleRef:
  kind: Role
  name: kube-parcel-client
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: v1
kind: Pod
metadata:
  name: kube-parcel-test
  namespace: testing
spec:
  serviceAccountName: kube-parcel-client
  containers:
    - name: client
      image: ghcr.io/tiborv/kube-parcel-cli:latest
      command: ["kube-parcel", "start", "--exec-mode", "k8s", "./my-chart"]
      volumeMounts:
        - name: charts
          mountPath: /charts
  volumes:
    - name: charts
      configMap:
        name: my-chart-files
  restartPolicy: Never
```

#### Image Sources

The `--load-images` flag supports multiple source types:

```bash
# Docker image from local daemon
--load-images "myapp:v1=docker://myapp:v1"

# OCI layout directory (from rules_oci)
--load-images "myapp:v1=oci:///path/to/oci_layout"

# Tar archive
--load-images "myapp:v1=tar:///path/to/image.tar"
```

#### Examples

**Simple local test:**
```bash
kube-parcel start ./examples/nginx-chart
```

**Multiple charts:**
```bash
kube-parcel start ./charts/frontend ./charts/backend ./charts/database
```

**With custom images (Bazel OCI):**
```bash
bazel build //myapp:image
kube-parcel start \
  --load-images "myapp:latest=oci://$(pwd)/bazel-bin/myapp/image" \
  ./deploy/helm-chart
```

**Remote Kubernetes deployment:**
```bash
kube-parcel start \
  --exec-mode k8s \
  --namespace testing \
  --cpu 2000m \
  --memory 4Gi \
  ./charts/myapp
```

### `upload` - Stream to Existing Runner

Stream charts and images to an already-running kube-parcel instance:

```bash
kube-parcel upload [flags] <chart-path> [chart-path...]
```

#### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--url` | Runner URL | `http://localhost:38080` |
| `--load-images` | Image mappings (same as `start`) | - |

#### Example

```bash
kube-parcel upload --url http://runner:8080 ./charts/myapp
```

### `status` - Check Runner Status

Query the current state of a runner:

```bash
kube-parcel status [--url <runner-url>]
```

## Helm Chart Requirements

### Test Hooks

kube-parcel automatically runs `helm test` after installation. Add test pods to your chart:

```yaml
# templates/test-pod.yaml
apiVersion: v1
kind: Pod
metadata:
  name: {{ .Release.Name }}-test
  annotations:
    "helm.sh/hook": test
spec:
  restartPolicy: Never
  containers:
    - name: test
      image: busybox
      command: ["/bin/sh", "-c", "echo 'Test passed!' && exit 0"]
```

### Image Pull Policy

For airgap mode, use `imagePullPolicy: Never` in your values:

```yaml
# values.yaml
image:
  repository: docker.io/library/myapp
  tag: latest
  pullPolicy: Never
```

> **Important:** Use fully qualified image names (`docker.io/library/...`) to ensure Kubernetes can find locally imported images.

## Web UI

Access the dashboard at `http://localhost:38080` (default port).

### Features

- **Steps Timeline**: Visual progress through testing phases
- **Helm Charts Table**: Status of each chart installation and test
- **Resource Visualization**: Pod status with emoji indicators and exit codes
- **Real-time Logs**: WebSocket streaming of K3s, Helm, and test output
- **Images List**: All images loaded into containerd

### Pod Status Indicators

| Emoji | Status |
|-------|--------|
| 🟢 | Running |
| ✅ | Succeeded |
| ❌ | Failed |
| 🟡 | Pending |
| 🔄 | Creating |
| ⚪ | Unknown |

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | All tests passed |
| 1 | Test failures |
| 2 | Infrastructure/setup failure |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `DOCKER_API_VERSION` | Docker API version (use `1.44` for compatibility) |
| `KUBE_PARCEL_AIRGAP` | Set to `false` to disable airgap network isolation |

## Troubleshooting

### ErrImageNeverPull

If pods fail with `ErrImageNeverPull`:
- Ensure `imagePullPolicy: Never` in chart values
- Use fully qualified image names: `docker.io/library/myimage:tag`
- Verify image was bundled with `--load-images` flag

### DNS Issues in Airgap Mode

The airgap network isolation blocks external DNS. For internal service discovery, DNS should work normally. If not:
- Check CoreDNS pods are running
- Verify service names resolve correctly
