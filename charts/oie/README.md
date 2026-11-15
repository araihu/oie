# Oiê Helm chart

See [Run on Kubernetes with Helm](../../docs/installation.md#run-on-kubernetes-with-helm)
for installation and access via `kubectl port-forward`.

| Setting | Default | Use |
| --- | --- | --- |
| `image.repository` | `ghcr.io/araihu/oie` | Override the image repository. |
| `image.tag` | Chart `appVersion` | Select a published image tag. |
| `image.digest` | Empty | Pin a digest such as `sha256:…`. Takes precedence over the tag. |
| `image.pullPolicy` | `IfNotPresent` | Control when Kubernetes pulls the image. |
| `imagePullSecrets` | `[]` | Reference registry credentials in the release namespace, for example `[{name: ghcr}]`. |
| `persistence.enabled` | `true` | Set to `false` for a disposable instance whose data disappears when the pod is replaced. |
| `persistence.existingClaim` | Empty | Mount an existing claim in the release namespace when persistence is enabled. |
| `persistence.storageClass` | Cluster default | Set a class name or use `""` for a volume without a storage class. |
| `persistence.accessMode` | `ReadWriteOnce` | Use `ReadWriteOncePod` if your cluster and CSI driver support it. |
| `persistence.size` | `10Gi` | Set the capacity requested for a new claim. Expansion depends on the StorageClass. |
| `resources` | `{}` | Set CPU and memory requests or limits for the pod. |

The pod runs as UID and GID `65532`. The storage driver or volume
permissions must allow that identity to write to `/data`. An existing claim
uses its own storage class, access mode and capacity.

## Remote access

Oiê has no built-in login. Before enabling Ingress or HTTPRoute, configure
your controller or an upstream proxy to require authentication for all paths
and serve HTTPS.
Keep the public host in proxied requests and allow server-sent events to stream
without buffering. Workloads that can reach the Service directly can access
the instance, so restrict that path in shared clusters.

### Ingress

Set `ingress.enabled` and `ingress.host` to route the host to Oiê. Set
`ingress.className` to your controller's IngressClass, `ingress.tlsSecretName`
to the TLS secret in the release namespace if the controller terminates TLS,
and `ingress.annotations` to any controller-specific settings, including
authentication. The Ingress routes the entire host at `/`.

### Gateway API

Use a cluster with the Gateway API CRDs serving `gateway.networking.k8s.io/v1`,
a controller, and an existing Gateway. Configure HTTPS and authentication at
the Gateway or upstream proxy. Save these values as `gateway-values.yaml`
and adjust the Gateway reference, listener and hostname for your cluster.

```yaml
httpRoute:
  enabled: true
  parentRefs:
    - name: shared-gateway
      namespace: gateway-system
      sectionName: https
  hostnames:
    - oie.example.com
```

Run the installation command with `--values gateway-values.yaml`. The
`HTTPRoute` forwards all paths for the listed hostnames to the Oiê Service.
If the Gateway is in the release namespace, omit `namespace`. For a Gateway
in a different namespace, its listener must allow routes from the Oiê
namespace through `allowedRoutes`. Use `httpRoute.annotations` for
controller-specific annotations.
