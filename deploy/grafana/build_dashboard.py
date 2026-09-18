#!/usr/bin/env python3
"""Build the PSSST Grafana dashboard (schema v2, tabbed layout).

The first tab answers the only two questions worth asking at a glance: who is
down, and who is in planned maintenance. The other tabs are for looking closer
once that answer is not enough.
"""
import json

DS = "prometheus"
GREEN, YELLOW, RED, BLUE, GREY = "green", "yellow", "red", "blue", "text"

elements = {}
_next_id = [0]


def query(expr, legend="", instant=False, table=False):
    spec = {"expr": expr, "legendFormat": legend, "range": not instant, "instant": instant}
    if table:
        spec["format"] = "table"
    return {
        "kind": "PanelQuery",
        "spec": {
            "refId": "A",
            "hidden": False,
            "query": {
                "kind": "DataQuery",
                "group": "prometheus",
                "version": "v0",
                "datasource": {"name": DS},
                "spec": spec,
            },
        },
    }


def panel(title, viz, queries, options=None, defaults=None, overrides=None, description="", transformations=None):
    _next_id[0] += 1
    name = f"panel-{_next_id[0]}"
    elements[name] = {
        "kind": "Panel",
        "spec": {
            "id": _next_id[0],
            "title": title,
            "description": description,
            "links": [],
            "data": {
                "kind": "QueryGroup",
                "spec": {
                    "queries": queries,
                    "transformations": transformations or [],
                    "queryOptions": {},
                },
            },
            "vizConfig": {
                "kind": "VizConfig",
                "group": viz,
                "version": "13.0.0",
                "spec": {
                    "options": options or {},
                    "fieldConfig": {"defaults": defaults or {}, "overrides": overrides or []},
                },
            },
        },
    }
    return name


def steps(*pairs):
    return {"mode": "absolute", "steps": [{"color": c, "value": v} for c, v in pairs]}


def stat(title, expr, good_is_zero=True, description="", unit="short"):
    """A headline number. Zero is the healthy answer for every count here."""
    thresholds = steps((GREEN, None), (RED, 1)) if good_is_zero else steps((GREY, None))
    return panel(
        title,
        "stat",
        [query(expr, instant=True)],
        options={
            "graphMode": "none",
            "colorMode": "background",
            "textMode": "auto",
            "justifyMode": "center",
            "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
        },
        defaults={"thresholds": thresholds, "unit": unit, "noValue": "0", "mappings": []},
        description=description,
    )


def table(title, expr, description="", value_title="", unit=None, overrides=None):
    """Instant table: one row per series, labels as columns."""
    organize = {
        "kind": "organize",
        "spec": {"excludeByName": {"Time": True, "__name__": True, "job": True, "instance": True},
                 "renameByName": {"Value": value_title or "Valeur"},
                 "indexByName": {}},
    }
    defaults = {"custom": {"align": "left", "cellOptions": {"type": "auto"}}, "mappings": []}
    if unit:
        defaults["unit"] = unit
    return panel(
        title,
        "table",
        [query(expr, instant=True, table=True)],
        options={"showHeader": True, "cellHeight": "sm", "footer": {"show": False}},
        defaults=defaults,
        overrides=overrides or [],
        description=description,
        transformations=[organize],
    )


def timeseries(title, expr, legend, unit="short", description=""):
    return panel(
        title,
        "timeseries",
        [query(expr, legend)],
        options={"legend": {"displayMode": "table", "placement": "right", "calcs": ["lastNotNull", "max"], "showLegend": True},
                 "tooltip": {"mode": "multi", "sort": "desc"}},
        defaults={"unit": unit, "custom": {"lineWidth": 2, "fillOpacity": 8, "showPoints": "never"},
                  "thresholds": steps((GREY, None)), "mappings": []},
        description=description,
    )


# --------------------------------------------------------------- tab 1: triage
UP_DOWN = [{"matcher": {"id": "byName", "options": "Valeur"},
            "properties": [{"id": "mappings", "value": [
                {"type": "value", "options": {"0": {"text": "EN PANNE", "color": RED, "index": 0},
                                              "1": {"text": "OK", "color": GREEN, "index": 1}}}]},
                           {"id": "custom.cellOptions", "value": {"type": "color-text"}}]}]

overview = [
    (stat("PSP en panne observée", "count(psp:observed_unavailable == 1)",
          description="Au moins une sonde en échec, collecte fraîche et valide."), 0, 0, 4, 4),
    (stat("Incidents déclarés", "count(psp:declared_unavailable == 1)",
          description="Le fournisseur annonce lui-même un incident ou un composant non opérationnel."), 4, 0, 4, 4),
    (stat("Incidents confirmés", "count(psp:confirmed_incident == 1)",
          description="Panne observée ET incident déclaré. Le signal le plus fiable."), 8, 0, 4, 4),
    (stat("Pannes non annoncées", "count(psp:unannounced_failure == 1)",
          description="Panne observée alors que la source officielle, fraîche, ne déclare rien."), 12, 0, 4, 4),
    (stat("Maintenances en cours", "count(psp_maintenance_active > 0)", good_is_zero=False,
          description="Fenêtres de maintenance actuellement ouvertes."), 16, 0, 4, 4),
    (stat("Sources périmées", "count(psp:status_source_stale == 1)",
          description="Source déclarée configurée mais sans instantané frais."), 20, 0, 4, 4),

    (table("PSP en panne observée", "psp:observed_unavailable == 1", value_title="État",
           description="Vide = aucune panne observée.", overrides=UP_DOWN), 0, 4, 12, 7),
    (table("Incidents déclarés en cours", "psp_active_incidents > 0", value_title="Incidents",
           description="Par sévérité normalisée."), 12, 4, 12, 7),

    (table("Maintenances planifiées", "psp_maintenance_next_start_timestamp_seconds * 1000 > time() * 1000",
           value_title="Début", unit="dateTimeAsIso",
           description="Prochaine fenêtre annoncée par le fournisseur."), 0, 11, 12, 8),
    (table("Nombre de maintenances annoncées", "psp_maintenance_scheduled > 0", value_title="Fenêtres",
           description="Total annoncé par fournisseur, y compris sans date."), 12, 11, 12, 8),
]

# ------------------------------------------------------------- tab 2: declared
declared = [
    (table("État déclaré par composant", "psp_declared_operational", value_title="État",
           description="overall est le rollup de la page. Un composant non opérationnel le force à zéro.",
           overrides=UP_DOWN), 0, 0, 12, 10),
    (table("Fraîcheur des sources déclarées",
           "time() - psp_status_source_last_success_timestamp_seconds", value_title="Âge", unit="s",
           description="Temps écoulé depuis le dernier instantané complet et valide."), 12, 0, 12, 10),
    (timeseries("Incidents déclarés par sévérité", "sum by (severity) (psp_active_incidents)",
                "{{severity}}", description="Sévérités normalisées: none, minor, major, critical, unknown."),
     0, 10, 12, 8),
    (timeseries("Source déclarée joignable", "psp_status_source_up", "{{psp}}",
                description="1 quand le dernier appel a produit un instantané complet."), 12, 10, 12, 8),
]

# ------------------------------------------------------------- tab 3: observed
observed = [
    (table("Résultat des sondes", "psp_probe_success", value_title="État",
           description="Résultat rapporté par Blackbox, indépendant de ce que déclare le fournisseur.",
           overrides=UP_DOWN), 0, 0, 12, 10),
    (table("Expiration des certificats",
           "(psp_probe_ssl_earliest_cert_expiry_timestamp_seconds - time()) / 86400",
           value_title="Jours restants", unit="d",
           description="Seules les sondes TLS alimentent cette table."), 12, 0, 12, 10),
    (timeseries("Latence des sondes", "psp_probe_duration_seconds", "{{psp}}/{{endpoint}}", unit="s",
                description="Durée mesurée par Blackbox pour la dernière sonde collectée."), 0, 10, 12, 8),
    (table("Dernier code HTTP", "psp_probe_http_status_code", value_title="Code",
           description="Zéro signifie aucune réponse HTTP. Les sondes TLS n'en produisent pas."),
     12, 10, 12, 8),
]

# ----------------------------------------------------- tab 4: collection health
collection = [
    (stat("Collectes Blackbox indisponibles", "count(psp:probe_collection_unavailable == 1)",
          description="PSSST ne parvient pas à obtenir une réponse Blackbox valide."), 0, 0, 6, 4),
    (stat("Sondes jamais collectées", "count(psp_probe_last_collection_success_timestamp_seconds == 0)",
          description="Aucune collecte valide depuis le démarrage."), 6, 0, 6, 4),
    (stat("Sources jamais collectées", "count(psp_status_source_last_success_timestamp_seconds == 0 and psp_status_source_configured == 1)",
          description="Source configurée n'ayant jamais produit d'instantané."), 12, 0, 6, 4),
    (stat("Version exportée", "psp_exporter_build_info", good_is_zero=False,
          description="Identité de build de l'exporteur."), 18, 0, 6, 4),

    (timeseries("Taux d'échec de collecte Blackbox",
                "sum by (psp) (rate(psp_probe_collection_total{outcome=\"failure\"}[5m]))",
                "{{psp}}", unit="reqps",
                description="Compteur cumulatif: mesure notre capacité à collecter, pas la santé du fournisseur."),
     0, 4, 12, 8),
    (timeseries("Taux d'échec des sources déclarées",
                "sum by (psp) (rate(psp_status_poll_total{outcome=\"failure\"}[5m]))",
                "{{psp}}", unit="reqps",
                description="Échecs de récupération de la page de statut officielle."), 12, 4, 12, 8),

    (timeseries("Sondes en échec observé",
                "sum by (psp) (rate(psp_probe_result_total{outcome=\"failure\"}[5m]))",
                "{{psp}}", unit="reqps",
                description="Sondes collectées avec succès mais dont le résultat est un échec: le fournisseur, pas nous."),
     0, 12, 12, 8),
    (table("Âge de la dernière collecte valide",
           "time() - psp_probe_last_collection_success_timestamp_seconds", value_title="Âge", unit="s",
           description="À comparer au seuil psp_probe_stale_after_seconds."), 12, 12, 12, 8),
]


def grid(items):
    return {"kind": "GridLayout", "spec": {"items": [
        {"kind": "GridLayoutItem", "spec": {"x": x, "y": y, "width": w, "height": h,
                                            "element": {"kind": "ElementReference", "name": name}}}
        for name, x, y, w, h in items]}}


dashboard = {
    "apiVersion": "dashboard.grafana.app/v2beta1",
    "kind": "Dashboard",
    "metadata": {"name": "pssst", "namespace": "default"},
    "spec": {
        "title": "PSSST · Statut des prestataires de paiement",
        "description": "Signal déclaré et signal observé, gardés séparés. Le premier onglet répond à qui est en panne et qui est en maintenance.",
        "editable": True,
        "cursorSync": "Off",
        "liveNow": False,
        "preload": False,
        "tags": ["pssst", "paiement"],
        "links": [],
        "annotations": [],
        "variables": [],
        "timeSettings": {"from": "now-6h", "to": "now", "autoRefresh": "1m",
                         "autoRefreshIntervals": ["30s", "1m", "5m"], "hideTimepicker": False,
                         "timezone": "browser"},
        "elements": elements,
        "layout": {"kind": "TabsLayout", "spec": {"tabs": [
            {"kind": "TabsLayoutTab", "spec": {"title": "Vue d'ensemble", "layout": grid(overview)}},
            {"kind": "TabsLayoutTab", "spec": {"title": "Déclaré", "layout": grid(declared)}},
            {"kind": "TabsLayoutTab", "spec": {"title": "Observé", "layout": grid(observed)}},
            {"kind": "TabsLayoutTab", "spec": {"title": "Santé de la collecte", "layout": grid(collection)}},
        ]}},
    },
}

print(json.dumps(dashboard, ensure_ascii=False, indent=2))
