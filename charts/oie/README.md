# Oiê Helm chart

See [Run on Kubernetes with Helm](https://github.com/araihu/oie/blob/helm-v0.2.36/docs/installation.md#run-on-kubernetes-with-helm)
for installation and access via `kubectl port-forward`.

Chart releases use their own version. Chart `0.2.36` defaults to the Oiê image
`v0.0.1-rc.58`. Install it from GHCR or download the archive and checksum
from [Helm chart 0.2.36](https://github.com/araihu/oie/releases/tag/helm-v0.2.36).

```sh
helm upgrade --install oie oci://ghcr.io/araihu/charts/oie --version 0.2.36 \
  --namespace oie --create-namespace --wait --timeout 10m
```

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
| `diagnostics.existingSecret` | Empty | Use an existing Secret for Diagnostics behind an external authentication proxy. |
| `diagnostics.secretKey` | `key` | Name of the Secret entry containing the diagnostics access key. |
| `resources` | `{}` | Set CPU and memory requests or limits for the pod. |

The pod runs as UID and GID `65532`. The storage driver or volume
permissions must allow that identity to write to `/data`. An existing claim
uses its own storage class, access mode and capacity.

## Remote access

Set up a TLS-terminating Ingress or Gateway and register the OIDC callback
`https://oie.example.com/auth/callback` with your provider. Create the
release namespace first, then create a Secret from the file containing
your OIDC client secret.

```sh
kubectl -n oie create secret generic oie-oidc --from-file=client-secret=./oidc-client-secret
```

Add the public origin and OIDC settings to your values file.

```yaml
hosted:
  enabled: true
  publicURL: https://oie.example.com
  trustedTLSProxies: 10.42.0.19/32 # Replace with the gateway's actual private source CIDRs.
  oidc:
    issuer: https://id.example.com
    subject: "OWNER_SUBJECT"
    clientID: oie
    clientSecretName: oie-oidc
    clientSecretKey: client-secret
```

Helm passes the OIDC and public settings as container environment variables
and mounts the referenced Secret entry as a read-only file in the Oiê container. Use
`clientSecretKey` if your Secret has a different key name. Restart the
StatefulSet after rotating the client secret.

The proxy must preserve the public host, overwrite `X-Forwarded-Proto` with
`https` for HTTPS traffic, and stream server-sent events without buffering.
Restrict access to the backend HTTP Service to the trusted proxy network.
The chart binds the internal HTTP probes on pod port `8081`. Kubernetes
calls `/readyz` for startup and readiness and `/livez` for liveness. The Service
and public routes use the application port. Keep the probe port private.
See the [internal probe OpenAPI contract](../../api/probe.openapi.yaml).

The init container sets the owner on the first start. To change an existing
owner, use `oie auth recover` and update the Helm values to match. For key
management and recovery, run the [host commands](../../docs/hosted-auth.md) in the running
pod as the service user. For example:

```sh
kubectl -n oie exec statefulset/oie -c oie -- /oie auth key-list --data-dir /data
```

For an existing external authentication proxy, set
`externalAuthentication: true` and protect all browser and API routes at
the proxy. Restrict pod and Service access in shared clusters. For images
that predate the internal probe listener, use chart `0.2.5`.

### Ingress

Set `ingress.enabled` and `ingress.host` to route the host to Oiê. Set
`ingress.className` to your controller's IngressClass, `ingress.tlsSecretName`
to the TLS secret in the release namespace if the controller terminates TLS,
and `ingress.annotations` to any controller-specific settings, including
authentication. The Ingress routes the entire host at `/`.

### Gateway API

Use a cluster with the Gateway API CRDs serving `gateway.networking.k8s.io/v1`,
a controller, and an existing Gateway. Configure the HTTPS listener on the Gateway and the hosted settings above. Save these values as `gateway-values.yaml`
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

Starting with chart `0.2.2`, set `httpRoute.timeouts.request` to `"0s"` in
`gateway-values.yaml` if your controller's default request timeout cuts off
Oiê's live chat events (SSE).

```yaml
httpRoute:
  timeouts:
    request: "0s"
```

The timeout applies to all paths on the route. With `httpRoute.timeouts`
omitted or empty, the controller uses its default timeout behavior.
You can also set `httpRoute.timeouts.backendRequest` to control the timeout
of an individual request from the Gateway to Oiê. Use Gateway API duration
strings such as `"30s"`, Gateway API CRDs from v1.2.0 or later, and a
controller that supports the fields you set.
See [HTTPRoute timeouts](https://gateway-api.sigs.k8s.io/reference/api-types/httproute/#timeouts-optional).

## Diagnostics

With `hosted.enabled`, the authenticated owner can open **Settings →
Diagnostics**. API clients can use an Oiê API key to read the same snapshot.
Behind an external authenticated proxy, set
`diagnostics.existingSecret` to a Secret with the legacy access key. The
entry defaults to `key` and can be changed with `diagnostics.secretKey`.

Operators can also use `kubectl logs -n oie statefulset/oie`. The pod retains
bounded log files under `/data/diagnostics/` using the configured persistence.

## Publishing a chart version

Publish the application image first. Set `appVersion` in `Chart.yaml` to
the published image tag and increment `version` for the chart release. Push
a tag of the form `helm-v<version>` matching `version`. The Helm release
workflow checks that the default image exists for Linux amd64 and arm64,
then pushes the OCI chart to `oci://ghcr.io/araihu/charts/oie` and publishes
the same archive and its checksum as GitHub release assets.
