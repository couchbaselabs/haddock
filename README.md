# Couchbase Operator Dashboard (COD)

This repository contains a dashboard application for monitoring and managing Couchbase clusters on Kubernetes.

## Compiling the Dashboard Binary

To compile the dashboard binary, choose the appropriate command for your target platform:

### For x86_64/amd64 platforms:
```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -a -installsuffix cgo -o dashboard cmd/cod/main.go
```

### For ARM64 platforms:
```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -a -installsuffix cgo -o dashboard cmd/cod/main.go
```

## Building the Docker Image

After compiling the binary, build the Docker image for your target platform:

### For x86_64/amd64 platforms:
```bash
docker build --platform=linux/amd64 -t cod:latest .
```

### For ARM64 platforms:
```bash
docker build --platform=linux/arm64 -t cod:latest .
```

### For multi-platform builds (optional):
```bash
docker buildx build --platform=linux/amd64,linux/arm64 -t cod:latest .
```

## Setting Up Kubernetes with Couchbase

### 1. Download the Couchbase Autonomous Operator (CAO) tool

Download the CAO tool from:
https://www.couchbase.com/content/c/downloads-kubernetes?x=gdjudm

### 2. Generate the Operator YAML

In the CAO tool directory, run:

```bash
bin/cao generate operator > operator.yaml
```

You can also generate the admission controller configuration:

```bash
bin/cao generate admission > admission.yaml
```

### 3. Modify the Operator YAML

Modify the `operator.yaml` file to include the COD sidecar. Use the provided operator.yaml as a reference.
See the operator.yaml file in the example folder of this repository for additional guidance on the modifications.

The key modifications include:

1. Add pod/log access to the operator role:
```yaml
apiGroups:
  - ""
resources:
  - pods
  - pods/status
  - services
  - persistentvolumeclaims
  - pods/log  # Add this line
verbs:
  - get
  - list
  - watch
  - create
  - update
  - delete
  - patch
```

2. Add 'watch' permission for events:
```yaml
apiGroups:
  - ""
resources:
  - events
verbs:
  - list
  - create
  - update
  - watch  # Add this line
```

3. Add the COD sidecar container to the operator deployment:
```yaml
- name: cod-sidecar
  image: cod:latest
  imagePullPolicy: IfNotPresent
  env:
  - name: WATCH_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace
  - name: POD_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
  ports:
  - containerPort: 3000
    name: cod
  resources: {}
```

4. Add the COD port to the service:
```yaml
- name: cod
  port: 3000
  protocol: TCP
```

### 4. Apply the Configurations

Apply the Custom Resource Definitions (CRDs) first:

```bash
kubectl apply -f <cao-directory>/crd.yaml
```

Then apply the modified operator and admission configurations:

```bash
kubectl apply -f operator.yaml
kubectl apply -f admission.yaml
```

Finally, deploy your Couchbase cluster:

```bash
kubectl apply -f <your-couchbase-cluster-config>.yaml
```

## Couchbase Cluster Configuration

### Required Settings for Dashboard UI Access

To access the Couchbase UI through the dashboard's reverse proxy, ensure your Couchbase cluster configuration includes the following setting:

```yaml
apiVersion: couchbase.com/v2
kind: CouchbaseCluster
metadata:
  name: your-cluster-name
spec:
  networking:
    exposeAdminConsole: true
  # ... rest of your cluster configuration
```

**Important:** The `exposeAdminConsole: true` setting is **required** for the dashboard's reverse proxy functionality to work properly. Without this setting, you won't be able to access the Couchbase Web Console through the "Open Couchbase UI" button in the dashboard.

### 5. Access the Dashboard

Once everything is up and running, forward the dashboard port:

```bash
kubectl port-forward svc/couchbase-operator 3000
```

Access the dashboard at: http://localhost:3000

