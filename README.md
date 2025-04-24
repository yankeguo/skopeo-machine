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

## Credits

GUO YANKE, MIT License
