# ConfigMap App Example

Demonstrates ConfigMap injection and environment variable validation.

## What This Tests

- ✅ ConfigMap creation
- ✅ Environment injection via `envFrom`
- ✅ Validates specific config values

## Run

```bash
kube-parcel start ./examples/configmap-app
```

## Chart Structure

```
configmap-app/
├── Chart.yaml
├── values.yaml           # Config values
└── templates/
    ├── configmap.yaml    # Creates ConfigMap from values
    ├── deployment.yaml   # App that reads env vars
    └── test-config.yaml  # Helm test validates injection
```

## What Happens

1. Creates a ConfigMap with DATABASE_URL, LOG_LEVEL, FEATURE_FLAGS
2. Deploys a pod that mounts the ConfigMap as environment variables
3. Runs `helm test` which validates:
   - DATABASE_URL is set
   - LOG_LEVEL is set
   - FEATURE_FLAGS contains expected values
