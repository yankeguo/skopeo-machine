# skopeo-machine

An API server that launches skopeo jobs in Kubernetes

## Usage

```bash
/skopeo-machine -conf config.json
```

```plain
POST /any/path

{
    "action":"copy",
    "source":"some/image:111",
    "target":"registry.mycompany.com/some-image:111"
}
```

## Configuration

```json
{
  "job": {
    "namespace": "skopeo",
    "image": "quay.io/skopeo/stable:latest",
    "imagePullPolicy": "IfNotPresent",
    "imagePullSecrets": [
      {
        "name": "myregistrykey"
      }
    ]
  },
  "copy": {
    "authfileSrc": "myregistrykeysecret-1",
    "authfileDst": "myregistrykeysecret-2"
  }
}
```

## In-Cluster Setup

```yaml
# Role for list, create, delete batchv1.Job
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: skopeo-machine
  namespace: autoops
rules:
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["list", "create", "delete"]
---
# ServiceAccount for skopeo-machine
apiVersion: v1
kind: ServiceAccount
metadata:
  name: skopeo-machine
  namespace: autoops
automountServiceAccountToken: true
---
# RoleBinding for skopeo-machine
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: skopeo-machine
  namespace: autoops
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: skopeo-machine
subjects:
  - kind: ServiceAccount
    name: skopeo-machine
    namespace: autoops
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: skopeo-machine
  namespace: autoops
data:
  config.json: |
    {
        "auth": {
            "username": "hello",
            "password": "world"
        },
        "job": {
            "namespace": "skopeo",
            "image": "quay.io/skopeo/stable:latest",
            "imagePullPolicy": "IfNotPresent",
            "imagePullSecrets": [
                {
                    "name": "myregistrykey"
                }
            ]
        },
        "copy": {
            "multiArch": "system",
            "authfileSrc": "myregistrykeysecret-1",
            "authfileDst": "myregistrykeysecret-2"
        }
    }
---
apiVersion: v1
kind: Service
metadata:
  name: skopeo-machine
  namespace: autoops
spec:
  selector:
    app: skopeo-machine
  ports:
    - protocol: TCP
      port: 8080
      targetPort: 8080
      name: http
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: skopeo-machine
  namespace: autoops
spec:
  serviceName: skopeo-machine
  replicas: 1
  selector:
    matchLabels:
      app: skopeo-machine
  template:
    metadata:
      labels:
        app: skopeo-machine
    spec:
      serviceAccountName: skopeo-machine
      containers:
        - name: skopeo-machine
          image: yankeguo/skopeo-machine
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8080
          volumeMounts:
            - name: vol-config
              mountPath: /data/config.json
              subPath: config.json
      volumes:
        - name: vol-config
          configMap:
            name: skopeo-machine
```

## Credits

GUO YANKE, MIT License
