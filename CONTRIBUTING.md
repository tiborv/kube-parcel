# Contributing correctly to kube-parcel

Thank you for your interest in contributing to kube-parcel! We welcome contributions from the community to make this tool better for everyone.

## Getting Started

1.  **Fork the Repository**: Start by forking the `kube-parcel` repository to your GitHub account.
2.  **Clone Locally**: Clone your fork to your local machine.
3.  **Install Prerequisites**:
    *   **Bazel**: We use Bazel for building and testing. `bazelisk` is recommended.
    *   **Docker**: Required for building images and running local integration tests.
    *   **Go**: Standard Go toolchain (although Bazel manages Go versions for builds).

## Development Workflow

### Building
To build all targets:
```bash
bazel build //...
```

### Testing
We have both unit tests and integration tests.

**Run Unit Tests:**
```bash
bazel test //...
```

**Run Integration Tests (K8s Mode):**
This requires Docker to be running.
```bash
bazel run //tests/integration:k8s_mode_test
```

### Making Changes
1.  Create a new branch for your feature or bugfix.
2.  Make your changes.
3.  Ensure all tests pass.
4.  Run `bazel run //:gazelle` if you added new Go imports or files, to update Bazel build files.

## Submitting a Pull Request

1.  Push your branch to your fork.
2.  Open a Pull Request against the `master` branch of `kube-parcel`.
3.  Provide a clear description of your changes and the problem they solve.
4.  Wait for review! We try to review PRs promptly.

## Code of Conduct

Please be respectful and considerate in all interactions. We focus on technical excellence and constructive collaboration.
