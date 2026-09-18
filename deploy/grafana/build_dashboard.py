#!/usr/bin/env python3
"""Build the PSSST Grafana dashboard (schema v2, tabbed layout).

The first tab answers the only two questions worth asking at a glance: who is
down, and who is in planned maintenance. The other tabs are for looking closer
once that answer is not enough.

Layout note: Grafana 13.0 renders nothing for a GridLayout nested inside a tab.
Each tab therefore uses RowsLayout, and each row an AutoGridLayout, so position
is an ordered list plus a column count rather than x/y coordinates.
"""
import json

DS = "prometheus"
GREEN, RED, GREY = "green", "red", "text"

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
                "spec": {"queries": queries, "transformations": transformations or [], "queryOptions": {}},
            },
            "vizConfig": {
                "kind": "VizConfig",
                "group": viz,
                "version": "",
                "spec": {"options": options or {}, "fieldConfig": {"defaults": defaults or {}, "overrides": overrides or []}},
            },
        },
    }
    return name


def steps(*pairs):
    return {"mode": "absolute", "steps": [{"color": c, "value": v} for c, v in pairs]}


def stat(title, expr, good_is_zero=True, description="", unit="short"):
    """A headline number. Zero is the healthy answer for most counts here."""
    thresholds = steps((GREEN, None), (RED, 1)) if good_is_zero else steps((GREY, None))
    return panel(
        title, "stat", [query(expr, instant=True)],
        options={"graphMode": "none", "colorMode": "background", "textMode": "auto",
                 "justifyMode": "center",
                 "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False}},
        defaults={"thresholds": thresholds, "unit": unit, "noValue": "0", "mappings": []},
        description=description,
    )


def table(title, expr, description="", value_title="", unit=None, overrides=None):
    """Instant table: one row per series, labels as columns."""
    # The options live under spec.options, not directly under spec: put them in
    # the wrong place and Grafana keeps the transformation but ignores it, so
    # every technical column stays on screen.
    organize = {
        "kind": "organize",
        "spec": {"id": "organize", "options": {
            "excludeByName": {"Time": True, "__name__": True, "job": True, "instance": True},
            "includeByName": {}, "indexByName": {},
            "renameByName": {"Value": value_title or "Valeur"}}},
    }
    defaults = {"custom": {"align": "left", "cellOptions": {"type": "auto"}}, "mappings": []}
    if unit:
        defaults["unit"] = unit
    return panel(
        title, "table", [query(expr, instant=True, table=True)],
        options={"showHeader": True, "cellHeight": "sm", "footer": {"show": False}},
        defaults=defaults, overrides=overrides or [], description=description,
        transformations=[organize],
    )


def timeseries(title, expr, legend, unit="short", description=""):
    return panel(
        title, "timeseries", [query(expr, legend)],
        options={"legend": {"displayMode": "table", "placement": "right",
                            "calcs": ["lastNotNull", "max"], "showLegend": True},
                 "tooltip": {"mode": "multi", "sort": "desc"}},
        defaults={"unit": unit, "custom": {"lineWidth": 2, "fillOpacity": 8, "showPoints": "never"},
                  "thresholds": steps((GREY, None)), "mappings": []},
        description=description,
    )


def autogrid(names, columns, row_height):
    return {"kind": "AutoGridLayout", "spec": {
        "maxColumnCount": columns, "rowHeight": row_height,
        "columnWidthMode": "standard", "fillScreen": False,
        "items": [{"kind": "AutoGridLayoutItem", "spec": {"element": {"kind": "ElementReference", "name": n}}}
                  for n in names]}}


def rows(*specs):
    """One row per group, each with its own column count, so headline numbers
    stay compact while tables get room."""
    return {"kind": "RowsLayout", "spec": {"rows": [
        {"kind": "RowsLayoutRow", "spec": {"title": title, "collapse": False,
                                           "layout": autogrid(names, columns, height)}}
        for title, names, columns, height in specs]}}


def tab(title, layout):
    return {"kind": "TabsLayoutTab", "spec": {"title": title, "layout": layout}}


# Shared override: turn a 0/1 gauge into words a human reads without decoding.
UP_DOWN = [{"matcher": {"id": "byName", "options": "État"},
            "properties": [{"id": "mappings", "value": [
                {"type": "value", "options": {"0": {"text": "EN PANNE", "color": RED, "index": 0},
                                              "1": {"text": "OK", "color": GREEN, "index": 1}}}]},
                           {"id": "custom.cellOptions", "value": {"type": "color-text"}}]}]

# ------------------------------------------------------------- tab 1: triage
triage = rows(
    ("Coup d'œil", [
        stat("PSP en panne observée", "count(psp:observed_unavailable == 1)",
             description="Au moins une sonde en échec, sur une collecte fraîche et valide."),
        stat("Incidents déclarés", "count(psp:declared_unavailable == 1)",
             description="Le fournisseur annonce lui-même un incident ou un composant non opérationnel."),
        stat("Incidents confirmés", "count(psp:confirmed_incident == 1)",
             description="Panne observée ET incident déclaré. Le signal le plus fiable."),
        stat("Pannes non annoncées", "count(psp:unannounced_failure == 1)",
             description="Panne observée alors que la source officielle, fraîche, ne déclare rien."),
        stat("Maintenances en cours", "count(psp_maintenance_active > 0)", good_is_zero=False,
             description="Fenêtres de maintenance actuellement ouvertes."),
        stat("Sources périmées", "count(psp:status_source_stale == 1)",
             description="Source déclarée configurée mais sans instantané frais."),
    ], 6, 1),
    ("Qui est en panne", [
        table("PSP en panne observée", "psp:observed_unavailable == 1", value_title="État",
              description="Vide signifie qu'aucune panne n'est observée.", overrides=UP_DOWN),
        table("Incidents déclarés en cours", "psp_active_incidents > 0", value_title="Incidents",
              description="Comptés par sévérité normalisée."),
    ], 2, 5),
    ("Maintenances planifiées", [
        table("Prochaine fenêtre annoncée",
              "psp_maintenance_next_start_timestamp_seconds * 1000 > time() * 1000",
              value_title="Début", unit="dateTimeAsIso",
              description="Date de début publiée par le fournisseur."),
        table("Fenêtres annoncées par fournisseur", "psp_maintenance_scheduled > 0",
              value_title="Fenêtres",
              description="Total annoncé, y compris les fenêtres sans date."),
    ], 2, 5),
)

# ----------------------------------------------------------- tab 2: declared
declared = rows(
    ("État déclaré", [
        table("État par composant", "psp_declared_operational", value_title="État",
              description="overall est le rollup de la page: un composant non opérationnel le force à zéro.",
              overrides=UP_DOWN),
        table("Fraîcheur des sources", "time() - psp_status_source_last_success_timestamp_seconds",
              value_title="Âge", unit="s",
              description="Temps écoulé depuis le dernier instantané complet et valide."),
    ], 2, 6),
    ("Tendances", [
        timeseries("Incidents déclarés par sévérité", "sum by (severity) (psp_active_incidents)",
                   "{{severity}}", description="Sévérités normalisées: none, minor, major, critical, unknown."),
        timeseries("Source déclarée joignable", "psp_status_source_up", "{{psp}}",
                   description="1 quand le dernier appel a produit un instantané complet."),
    ], 2, 5),
)

# ----------------------------------------------------------- tab 3: observed
observed = rows(
    ("Résultat des sondes", [
        table("Dernier résultat", "psp_probe_success", value_title="État",
              description="Résultat rapporté par Blackbox, indépendant de ce que déclare le fournisseur.",
              overrides=UP_DOWN),
        table("Expiration des certificats",
              "(psp_probe_ssl_earliest_cert_expiry_timestamp_seconds - time()) / 86400",
              value_title="Jours restants", unit="d",
              description="Seules les sondes TLS alimentent cette table."),
    ], 2, 6),
    ("Détail", [
        timeseries("Latence des sondes", "psp_probe_duration_seconds", "{{psp}}/{{endpoint}}", unit="s",
                   description="Durée mesurée par Blackbox pour la dernière sonde collectée."),
        table("Dernier code HTTP", "psp_probe_http_status_code", value_title="Code",
              description="Zéro signifie aucune réponse HTTP. Les sondes TLS n'en produisent pas."),
    ], 2, 5),
)

# -------------------------------------------------- tab 4: collection health
collection = rows(
    ("Notre capacité à mesurer", [
        stat("Collectes Blackbox indisponibles", "count(psp:probe_collection_unavailable == 1)",
             description="PSSST ne parvient pas à obtenir une réponse Blackbox valide."),
        stat("Sondes jamais collectées", "count(psp_probe_last_collection_success_timestamp_seconds == 0)",
             description="Aucune collecte valide depuis le démarrage."),
        stat("Sources jamais collectées",
             "count(psp_status_source_last_success_timestamp_seconds == 0 and psp_status_source_configured == 1)",
             description="Source configurée n'ayant jamais produit d'instantané."),
        stat("Entités surveillées", "count(psp_info)", good_is_zero=False,
             description="PSP, acquéreurs et banques confondus."),
    ], 4, 1),
    ("Taux d'échec", [
        timeseries("Échecs de collecte Blackbox",
                   "sum by (psp) (rate(psp_probe_collection_total{outcome=\"failure\"}[5m]))",
                   "{{psp}}", unit="reqps",
                   description="Mesure notre capacité à collecter, pas la santé du fournisseur."),
        timeseries("Échecs des sources déclarées",
                   "sum by (psp) (rate(psp_status_poll_total{outcome=\"failure\"}[5m]))",
                   "{{psp}}", unit="reqps",
                   description="Échecs de récupération de la page de statut officielle."),
    ], 2, 5),
    ("Sondes et fraîcheur", [
        timeseries("Sondes en échec observé",
                   "sum by (psp) (rate(psp_probe_result_total{outcome=\"failure\"}[5m]))",
                   "{{psp}}", unit="reqps",
                   description="Collecte réussie mais résultat en échec: le fournisseur, pas nous."),
        table("Âge de la dernière collecte valide",
              "time() - psp_probe_last_collection_success_timestamp_seconds",
              value_title="Âge", unit="s",
              description="À comparer au seuil psp_probe_stale_after_seconds."),
    ], 2, 5),
)

dashboard = {
    "apiVersion": "dashboard.grafana.app/v2beta1",
    "kind": "Dashboard",
    "metadata": {"name": "pssst-status", "namespace": "default"},
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
                         "autoRefreshIntervals": ["30s", "1m", "5m"],
                         "hideTimepicker": False, "timezone": "browser"},
        "elements": elements,
        "layout": {"kind": "TabsLayout", "spec": {"tabs": [
            tab("Vue d'ensemble", triage),
            tab("Déclaré", declared),
            tab("Observé", observed),
            tab("Santé de la collecte", collection),
        ]}},
    },
}

print(json.dumps(dashboard, ensure_ascii=False, indent=2))
