# Gateway API

Configure GKE's managed [Gateway API](https://cloud.google.com/kubernetes-engine/docs/concepts/gateway-api) controller via the `gatewayAPIChannel` field on `GCPManagedControlPlane`'s `clusterNetwork`.

```yaml
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: GCPManagedControlPlane
metadata:
  name: mygcpmanagedcontrolplane
spec:
  clusterNetwork:
    gatewayAPIChannel: standard
```

`gatewayAPIChannel` accepts:

- **`standard`** — enables the GKE-managed Gateway API controller on the standard release channel.
- **`disabled`** — disables the Gateway API controller.

Omitting `gatewayAPIChannel` leaves GKE's default behavior in place. The field is mutable and can be changed on an existing cluster.

`gatewayAPIChannel` cannot be set when `enableAutopilot` is `true`, since Autopilot clusters manage Gateway API themselves.
