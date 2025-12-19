# Microservice Example

A multi-replica nginx deployment demonstrating service discovery testing.

## What This Tests

- ✅ Deployment with multiple replicas
- ✅ Service routing to pods
- ✅ HTTP connectivity via DNS

## Run

```bash
kube-parcel start ./examples/microservice
```

## Chart Structure

```
microservice/
├── Chart.yaml
├── values.yaml
└── templates/
    ├── deployment.yaml    # 2-replica nginx
    ├── service.yaml       # ClusterIP service
    └── test-connection.yaml  # Helm test (curl to service)
```

## What Happens

1. Deploys 2 nginx pods behind a ClusterIP service
2. Waits for pods to be ready
3. Runs `helm test` which curls the service endpoint
4. Validates service discovery works correctly
