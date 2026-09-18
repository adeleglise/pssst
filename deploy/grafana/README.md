# Grafana dashboard

`pssst-v2.json` is generated, never hand-edited. Regenerate it after any change:

```sh
python3 build_dashboard.py > pssst-v2.json
```

Publish it with the dashboard API (schema v2, tabbed layout needs Grafana 12+
with `dashboardNewLayouts` enabled):

```sh
curl -X POST -H 'Content-Type: application/json' --data-binary @pssst-v2.json \
  http://GRAFANA/apis/dashboard.grafana.app/v2beta1/namespaces/default/dashboards
```

The first tab answers the two questions worth asking at a glance: who is down,
and who is in planned maintenance. The other three are for looking closer:
declared status, observed probes, and collection health, which is where the
cumulative counters show whether a gap is the provider failing or us failing to
measure it.
