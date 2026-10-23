# PSSST Helm chart

![PSSST](https://raw.githubusercontent.com/adeleglise/pssst/main/brand/png/banner.png)

Deploys [PSSST](https://github.com/adeleglise/pssst), a Prometheus exporter that
reports what payment providers declare on their status pages and what their
endpoints actually do, side by side and never merged, together with the
Blackbox Exporter it probes through.

```sh
helm install pssst oci://ghcr.io/adeleglise/charts/pssst \
  --namespace monitoring --create-namespace \
  --set-file inventory=inventory.yml
```

Without `inventory`, the chart runs the default inventory of 24 European
providers. In yours, set `blackbox.base_url: http://pssst-blackbox:9115`.

The chart renders the same objects, under the same fixed names, as the
kustomize base in `deploy/`; a test fails when the two drift. Install it once
per namespace.

| Value | Default | Meaning |
| --- | --- | --- |
| `image.repository` | `ghcr.io/adeleglise/pssst` | |
| `image.tag` | the chart's `appVersion` | Chart X.Y.Z deploys image X.Y.Z. |
| `inventory` | empty: the default inventory | Your inventory, as YAML text. Use `--set-file`. |
| `logLevel` | `info` | |
| `extraEnv` | empty | Extra container environment, rendered after `PSSST_LOG_LEVEL`. |
| `envFrom` | empty | Sources of environment, for example a Secret holding the tokens the inventory reads as `${TOKEN}`. |
| `resources` | 50m / 64Mi requested, 500m / 256Mi limit | |
| `blackbox.image.*` | `quay.io/prometheus/blackbox-exporter:v0.28.0` | |
| `blackbox.resources` | 50m / 64Mi requested, 500m / 192Mi limit | |
| `serviceMonitor.enabled` | `false` | Turn on with the Prometheus Operator. |
| `serviceMonitor.additionalLabels` | empty | Labels your Prometheus selects on (kube-prometheus-stack: `release: <name>`). |
| `prometheusRule.enabled` | `false` | Installs the correlation rules as a PrometheusRule, copied from the chart's own release. |
| `prometheusRule.namespace` | the release namespace | |
| `prometheusRule.additionalLabels` | empty | Labels your Prometheus selects rules on. |
| `networkPolicy.enabled` | `true` | Ingress to Blackbox only from the `pssst` pods. Needs a CNI that enforces NetworkPolicy. |
| `datadog.metrics` | seven metrics, about 300 series | Datadog bills custom metrics per series; see the README before adding any. |

The correlation (`psp:unannounced_failure`, `psp:confirmed_incident`) is a set
of Prometheus rules, not part of the exporter: install them with
`--set prometheusRule.enabled=true`, or load
[deploy/prometheus/pssst.yml](https://github.com/adeleglise/pssst/blob/main/deploy/prometheus/pssst.yml)
from the tag matching your chart.

To keep a token out of the inventory, reference it as `${TOKEN}` in a header
and give the pod the variable from a Secret:

```yaml
envFrom:
  - secretRef:
      name: pssst-tokens
```
