#!/usr/bin/env bash
# Integration test for k8s-mode-test
# Runs kube-parcel with --exec-mode k8s from inside K3s to spawn nested Runner Pods

set -e

# --- begin runfiles.bash initialization ---
if [[ ! -d "${RUNFILES_DIR:-/dev/null}" && ! -f "${RUNFILES_MANIFEST_FILE:-/dev/null}" ]]; then
  if [[ -f "$0.runfiles_manifest" ]]; then
    export RUNFILES_MANIFEST_FILE="$0.runfiles_manifest"
  elif [[ -f "$0.runfiles/MANIFEST" ]]; then
    export RUNFILES_MANIFEST_FILE="$0.runfiles/MANIFEST"
  elif [[ -f "$0.runfiles/bazel_tools/tools/bash/runfiles/runfiles.bash" ]]; then
    export RUNFILES_DIR="$0.runfiles"
  fi
fi
if [[ -f "${RUNFILES_DIR:-/dev/null}/bazel_tools/tools/bash/runfiles/runfiles.bash" ]]; then
  source "${RUNFILES_DIR}/bazel_tools/tools/bash/runfiles/runfiles.bash"
elif [[ -f "${RUNFILES_MANIFEST_FILE:-/dev/null}" ]]; then
  source "$(grep -m1 "^bazel_tools/tools/bash/runfiles/runfiles.bash " \
            "$RUNFILES_MANIFEST_FILE" | cut -d ' ' -f 2-)"
else
  echo >&2 "ERROR: cannot find @bazel_tools//tools/bash/runfiles:runfiles.bash"
  exit 1
fi
# --- end runfiles.bash initialization ---

# Locate runfiles
CLIENT="$(rlocation _main/cmd/client/client_/client)"
TEST_RUNNER_IMAGE="$(rlocation _main/tests/integration/test_runner_image)"
TEST_RUNNER_LOADER="$(rlocation _main/tests/integration/test_runner_load)"
if [ -z "$TEST_RUNNER_LOADER" ]; then
    TEST_RUNNER_LOADER="$(rlocation _main/tests/integration/test_runner_load.sh)"
fi

# Debug paths
echo "Debug: rlocation results:"
echo "CLIENT: $CLIENT"
echo "TEST_RUNNER_IMAGE: $TEST_RUNNER_IMAGE"
echo "TEST_RUNNER_LOADER: $TEST_RUNNER_LOADER"

if [ -z "$TEST_RUNNER_LOADER" ]; then
  echo "Loader not found via rlocation. Searching in runfiles..."
  find . -name "test_runner_load*"
  
  # Try fallback - prioritize .sh and ensure it's not a directory
  # Check for .sh first
  TEST_RUNNER_LOADER=$(find . -name "test_runner_load.sh" | head -n 1)
  
  if [ -z "$TEST_RUNNER_LOADER" ]; then
     # Try non-sh, but verify it is a file/symlink-to-file, not a directory
     CANDIDATE=$(find . -name "test_runner_load" | head -n 1)
     if [ -n "$CANDIDATE" ]; then
         if [ -d "$CANDIDATE" ]; then
             echo "Debug: Found directory $CANDIDATE, looking inside..."
             # If directory, look for script inside
             INNER=$(find "$CANDIDATE" -name "test_runner_load" -o -name "test_runner_load.sh" | head -n 1)
             if [ -n "$INNER" ]; then
                 TEST_RUNNER_LOADER="$INNER"
             fi
         else
             TEST_RUNNER_LOADER="$CANDIDATE"
         fi
     fi
  fi
  
  # If output is valid path, ensure absolute or relative correctness
  if [ -n "$TEST_RUNNER_LOADER" ]; then
    # Resolve to absolute path or keep relative
    TEST_RUNNER_LOADER="$(pwd)/$TEST_RUNNER_LOADER"
  fi
  echo "Fallback loader: $TEST_RUNNER_LOADER"
fi

REGISTRY_IMAGE="$(rlocation _main/tests/integration/registry_image)"

# Check registry image format
# In tests/integration/BUILD.bazel: oci_image(name="registry_image")
# So it is a directory. Remove /image.tar suffix.
# REGISTRY_IMAGE="$(rlocation _main/tests/integration/registry_image)" # Already set above

# Chart directory is in runfiles at tests/integration/k8s-mode-test
CHART_YAML="$(rlocation _main/tests/integration/k8s-mode-test/Chart.yaml)"
CHART_DIR="$(dirname "$CHART_YAML")"

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║         kube-parcel K8s Mode Integration Test                ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "Client: $CLIENT"
echo "Runner Image Dir: $TEST_RUNNER_IMAGE"
echo "Registry Image Dir: $REGISTRY_IMAGE"
echo "Chart Dir: $CHART_DIR"
echo ""

# Load test runner image into Docker
# Load test runner image into Docker
echo "Loading test-runner:latest into Docker..."
if [ -n "$TEST_RUNNER_LOADER" ]; then
  if [ -d "$TEST_RUNNER_LOADER" ]; then
     # It's a directory (oci_load output), find the loader script inside
     if [ -f "$TEST_RUNNER_LOADER/load.sh" ]; then
       LOADER_SCRIPT="$TEST_RUNNER_LOADER/load.sh"
     elif [ -f "$TEST_RUNNER_LOADER/test_runner_load" ]; then
       LOADER_SCRIPT="$TEST_RUNNER_LOADER/test_runner_load"
     elif [ -f "$TEST_RUNNER_LOADER/test_runner_load.sh" ]; then
         LOADER_SCRIPT="$TEST_RUNNER_LOADER/test_runner_load.sh"
     fi
     
     if [ -n "$LOADER_SCRIPT" ]; then
       "$LOADER_SCRIPT"
     else
       echo "❌ Failed to find loader script inside directory $TEST_RUNNER_LOADER"
       echo "Contents (ls -la):"
       ls -la "$TEST_RUNNER_LOADER"
       exit 1
     fi
  else
     "$TEST_RUNNER_LOADER"
  fi
else
  echo "❌ Failed to find loader script!"
  exit 1
fi

# Run the test
export DOCKER_API_VERSION=1.44

exec "$CLIENT" start \
  --runner-image "test-runner:latest" \
  --load-images "test-runner:latest=oci://$TEST_RUNNER_IMAGE" \
  --load-images "registry:3=oci://$REGISTRY_IMAGE" \
  "$CHART_DIR"
