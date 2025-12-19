# Testing kube-parcel

## Quick Start (When Docker is Available)

### 1. Build Everything

```bash
# Build client CLI
make build-client

# Start Docker daemon (if not running)
sudo systemctl start docker
# or: sudo dockerd &

# Build server image with ko
KO_DOCKER_REPO=ko.local ko build --local ./cmd/server

# Tag for convenience
docker tag ko.local/server-*:latest kube-parcel:latest
```

### 2. Run Integration Test

```bash
# Start server and upload chart in one command
./bin/kube-parcel start --image=kube-parcel:latest examples/sample-chart

# Expected output:
# 🐳 Launching server locally with Docker...
# ✅ Container started: abc12345
# 🌐 Server running at: http://localhost:8080
# 🌐 Open in browser to see live logs!
# 📦 Bundling chart from: examples/sample-chart
# Found 1 image(s): [nginx:1.25]
# Exporting image: nginx:1.25
# ✅ Added image: nginx_1.25.tar (67108864 bytes)
# 📤 Streaming to: http://localhost:8080/parcel/upload
# ✅ Upload accepted
# ✅ Deployment complete! Press Ctrl+C to stop...
```

### 3. Verify in Browser

Open http://localhost:8080 and you should see:

- **State Machine**: IDLE → TRANSFERRING → STARTING → READY (animated)
- **Statistics**:
  - Images: 1
  - Charts: 1
  - K3s Status: Ready ✅
- **Live Logs**: Real-time K3s and Helm output

### 4. Verify with kubectl

```bash
# Get container ID
CONTAINER_ID=$(docker ps --filter "ancestor=kube-parcel:latest" --format "{{.ID}}")

# Copy kubeconfig
docker exec $CONTAINER_ID cat /etc/rancher/k3s/k3s.yaml > /tmp/k3s.yaml

# Fix server URL in kubeconfig
sed -i 's/server: https:\/\/127.0.0.1:6443/server: https:\/\/localhost:6443/' /tmp/k3s.yaml

# Check pods
KUBECONFIG=/tmp/k3s.yaml kubectl get pods

# Expected:
# NAME                         READY   STATUS    RESTARTS   AGE
# nginx-demo-xxxxxxxxx-xxxxx   1/1     Running   0          30s

# Check deployment
KUBECONFIG=/tmp/k3s.yaml kubectl get deployment nginx-demo

# Check service
KUBECONFIG=/tmp/k3s.yaml kubectl get svc nginx-demo
```

---

## Manual Testing Steps

### Test 1: Basic Upload

```bash
# Terminal 1: Start server manually
docker run --rm --privileged \
  -p 8080:8080 -p 9090:9090 \
  --name kube-parcel-test \
  kube-parcel:latest

# Terminal 2: Upload chart
./bin/kube-parcel upload examples/sample-chart

# Terminal 3: Check status
./bin/kube-parcel status
```

### Test 2: State Machine Transitions

1. Start server → Check UI shows **IDLE**
2. Upload chart → Check UI transitions to **TRANSFERRING**
3. Wait 30s → Check UI transitions to **READY**
4. Check logs → Verify K3s startup messages
5. Check logs → Verify Helm install messages

### Test 3: Image Pre-positioning

```bash
# After upload, exec into container
docker exec kube-parcel-test ls -lh /var/lib/rancher/k3s/agent/images/

# Should show:
# -rw-r--r-- 1 root root  64M Dec 18 23:55 nginx_1.25.tar

# Check containerd has imported it
docker exec kube-parcel-test ctr -n k8s.io images ls | grep nginx

# Should show nginx:1.25
```

### Test 4: Helm Installation

```bash
# Check Helm releases
docker exec kube-parcel-test helm list

# Expected:
# NAME       NAMESPACE  REVISION  STATUS    CHART           APP VERSION
# nginx-demo default    1         deployed  nginx-demo-0.1.0 1.25

# Check pod is actually running nginx
docker exec kube-parcel-test kubectl get pods -o wide
docker exec kube-parcel-test kubectl exec <pod-name> -- nginx -v

# Should output: nginx version: nginx/1.25.x
```

---

## Performance Testing

### Test 5: Large Image Streaming

```bash
# Create chart with larger image
cat > examples/large-chart/values.yaml <<EOF
image:
  repository: postgres
  tag: "16"
EOF

# Test upload (postgres:16 is ~150MB)
./bin/kube-parcel start examples/large-chart

# Monitor memory usage
docker stats kube-parcel-test

# Client memory should stay < 100MB (proves io.Pipe is working)
```

### Test 6: Multiple Images

```bash
# Create chart with multiple images
cat > examples/multi-chart/values.yaml <<EOF
web:
  image:
    repository: nginx
    tag: "1.25"
db:
  image:
    repository: redis
    tag: "7.0"
cache:
  image:
    repository: memcached
    tag: "1.6"
EOF

# Client should detect all 3 images
./bin/kube-parcel start examples/multi-chart
```

## Cleanup

```bash
# Stop and remove container
docker stop kube-parcel-test
docker rm kube-parcel-test

# Remove image
docker rmi kube-parcel:latest

# Clean binaries
make clean
```

---

## CI/CD Example

```yaml
# .github/workflows/test.yml
name: Integration Test

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      
      - name: Install ko
        run: go install github.com/google/ko@latest
      
      - name: Build client
        run: make build-client
      
      - name: Build server image
        run: |
          KO_DOCKER_REPO=ko.local ko build --local ./cmd/server
          docker tag $(docker images --format "{{.Repository}}:{{.Tag}}" | grep ko.local | head -1) kube-parcel:latest
      
      - name: Run integration test
        run: |
          ./bin/kube-parcel start --image=kube-parcel:latest examples/sample-chart &
          sleep 60
          
          # Verify pod is running
          KUBECONFIG=/tmp/k3s.yaml kubectl wait --for=condition=Ready pod -l app=nginx-demo --timeout=120s
          
          # Verify helm release
          docker exec $(docker ps -q) helm list | grep nginx-demo
```

---

## Troubleshooting Guide

### "Cannot connect to Docker daemon"
```bash
# Check Docker is running
sudo systemctl status docker

# Start if needed
sudo systemctl start docker

# Check permissions
sudo usermod -aG docker $USER
```

### "Image pull failed in K3s"
```bash
# K3s should NOT pull images - they're pre-positioned
# If you see pull errors, check:

# 1. Was image extracted?
docker exec <container> ls /var/lib/rancher/k3s/agent/images/

# 2. Does containerd see it?
docker exec <container> ctr -n k8s.io images ls

# 3. Check K3s logs
docker logs <container> 2>&1 | grep -i "pull"
```

### "Helm install timeout"
```bash
# Check K3s is ready
docker exec <container> kubectl get nodes

# Check pod events
docker exec <container> kubectl describe pod <pod-name>

# Check Helm debug
docker exec <container> helm list --debug
```

### "Port already in use"
```bash
# Find process using port
sudo lsof -i :8080
sudo lsof -i :9090

# Kill or use different ports
docker run -p 8081:8080 -p 9091:9090 ...
./bin/kube-parcel upload --server http://localhost:8081 ...
```

---

## Success Criteria

✅ **Integration test passes when:**

1. Client spawns container successfully
2. Server starts HTTP server
3. Client uploads tar stream successfully
4. Server extracts images to K3s directory
5. Server extracts charts to /tmp/parcel/charts/
6. K3s starts and becomes ready
7. Helm installs chart successfully
8. Pod reaches Running state
9. Web UI shows all transitions
10. Logs stream in real-time

**Total time:** ~60 seconds from start to Running pod
