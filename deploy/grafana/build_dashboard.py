#!/usr/bin/env python3
"""Build the PSSST Grafana dashboard (schema v2, tabbed layout).

    python3 build_dashboard.py > pssst-v2.json
    python3 build_dashboard.py --grafana-com > pssst-grafana-com.json

The second form is the same dashboard in English and in the classic JSON model
that grafana.com imports: each tab's rows become rows of panels, the first
tab's open and the others collapsed, and the datasource is asked for at import.

The first tab answers the only two questions worth asking at a glance: who is
down, and who is in planned maintenance. The other tabs are for looking closer
once that answer is not enough.

Layout note: Grafana 13.0 renders nothing for a GridLayout nested inside a tab.
Each tab therefore uses RowsLayout, and each row an AutoGridLayout, so position
is an ordered list plus a column count rather than x/y coordinates.
"""
import json
import sys

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

# For a table of psp:observed_unavailable == 1, where 1 means down. UP_DOWN
# reads 1 as up, and once labelled every provider in this table "OK".
DOWN = [{"matcher": {"id": "byName", "options": "État"},
         "properties": [{"id": "mappings", "value": [
             {"type": "value", "options": {"1": {"text": "EN PANNE", "color": RED, "index": 0}}}]},
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
              description="Vide signifie qu'aucune panne n'est observée.", overrides=DOWN),
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


# ------------------------------------------------------ grafana.com, English
# Every human string of the dashboard, French to English. A string missing
# here fails the build rather than reaching the listing untranslated.
EN = {
    "PSSST · Statut des prestataires de paiement": "PSSST · Payment provider status",
    "Signal déclaré et signal observé, gardés séparés. Le premier onglet répond à qui est en panne et qui est en maintenance.":
        "Declared and observed status of your payment providers, kept apart. The first rows answer who is down and who is in planned maintenance.",
    "PSP en panne observée": "Observed down",
    "Au moins une sonde en échec, sur une collecte fraîche et valide.": "At least one probe failing, on a fresh and valid collection.",
    "Incidents déclarés": "Declared incidents",
    "Le fournisseur annonce lui-même un incident ou un composant non opérationnel.": "The provider itself announces an incident or a component that is not operational.",
    "Incidents confirmés": "Confirmed incidents",
    "Panne observée ET incident déclaré. Le signal le plus fiable.": "Observed failure AND declared incident. The most reliable signal.",
    "Pannes non annoncées": "Unannounced failures",
    "Panne observée alors que la source officielle, fraîche, ne déclare rien.": "Observed failure while the official source, fresh, declares nothing.",
    "Maintenances en cours": "Maintenance in progress",
    "Fenêtres de maintenance actuellement ouvertes.": "Maintenance windows open right now.",
    "Sources périmées": "Stale sources",
    "Source déclarée configurée mais sans instantané frais.": "Declared source configured but without a fresh snapshot.",
    "Vide signifie qu'aucune panne n'est observée.": "Empty means no failure is observed.",
    "État": "State",
    "EN PANNE": "DOWN",
    "OK": "OK",
    "Incidents déclarés en cours": "Declared incidents in progress",
    "Comptés par sévérité normalisée.": "Counted by normalized severity.",
    "Incidents": "Incidents",
    "Prochaine fenêtre annoncée": "Next announced window",
    "Date de début publiée par le fournisseur.": "Start date published by the provider.",
    "Début": "Start",
    "Fenêtres annoncées par fournisseur": "Announced windows per provider",
    "Total annoncé, y compris les fenêtres sans date.": "Total announced, including windows without a date.",
    "Fenêtres": "Windows",
    "État par composant": "State per component",
    "overall est le rollup de la page: un composant non opérationnel le force à zéro.": "overall is the page's rollup: any component not operational forces it to zero.",
    "Fraîcheur des sources": "Source freshness",
    "Temps écoulé depuis le dernier instantané complet et valide.": "Time since the last complete and valid snapshot.",
    "Âge": "Age",
    "Incidents déclarés par sévérité": "Declared incidents by severity",
    "Sévérités normalisées: none, minor, major, critical, unknown.": "Normalized severities: none, minor, major, critical, unknown.",
    "Source déclarée joignable": "Declared source reachable",
    "1 quand le dernier appel a produit un instantané complet.": "1 when the last call produced a complete snapshot.",
    "Dernier résultat": "Last result",
    "Résultat rapporté par Blackbox, indépendant de ce que déclare le fournisseur.": "Result reported by Blackbox, independent of what the provider declares.",
    "Expiration des certificats": "Certificate expiry",
    "Seules les sondes TLS alimentent cette table.": "Only TLS probes feed this table.",
    "Jours restants": "Days left",
    "Latence des sondes": "Probe latency",
    "Durée mesurée par Blackbox pour la dernière sonde collectée.": "Duration measured by Blackbox for the last collected probe.",
    "Dernier code HTTP": "Last HTTP status",
    "Zéro signifie aucune réponse HTTP. Les sondes TLS n'en produisent pas.": "Zero means no HTTP response. TLS probes produce none.",
    "Code": "Code",
    "Collectes Blackbox indisponibles": "Blackbox collections unavailable",
    "PSSST ne parvient pas à obtenir une réponse Blackbox valide.": "PSSST cannot get a valid answer from Blackbox.",
    "Sondes jamais collectées": "Probes never collected",
    "Aucune collecte valide depuis le démarrage.": "No valid collection since startup.",
    "Sources jamais collectées": "Sources never collected",
    "Source configurée n'ayant jamais produit d'instantané.": "Configured source that has never produced a snapshot.",
    "Entités surveillées": "Monitored providers",
    "PSP, acquéreurs et banques confondus.": "Payment providers, acquirers and banks together.",
    "Échecs de collecte Blackbox": "Blackbox collection failures",
    "Mesure notre capacité à collecter, pas la santé du fournisseur.": "Measures our ability to collect, not the provider's health.",
    "Échecs des sources déclarées": "Declared source failures",
    "Échecs de récupération de la page de statut officielle.": "Failures to fetch the official status page.",
    "Sondes en échec observé": "Probes observed failing",
    "Collecte réussie mais résultat en échec: le fournisseur, pas nous.": "Collection succeeded but the result failed: the provider, not us.",
    "Âge de la dernière collecte valide": "Age of the last valid collection",
    "À comparer au seuil psp_probe_stale_after_seconds.": "Compare with the psp_probe_stale_after_seconds threshold.",
    "Vue d'ensemble": "Overview",
    "Coup d'œil": "At a glance",
    "Qui est en panne": "Who is down",
    "Maintenances planifiées": "Planned maintenance",
    "Déclaré": "Declared",
    "État déclaré": "Declared state",
    "Tendances": "Trends",
    "Observé": "Observed",
    "Résultat des sondes": "Probe results",
    "Détail": "Detail",
    "Santé de la collecte": "Collection health",
    "Notre capacité à mesurer": "Our ability to measure",
    "Taux d'échec": "Failure rates",
    "Sondes et fraîcheur": "Probes and freshness",
}


def en(text):
    if text == "":
        return text
    if text not in EN:
        sys.exit(f"build_dashboard: no English for {text!r}")
    return EN[text]


def translate(node):
    """Translate, in place, every human string: titles, descriptions, value
    mapping texts, renamed columns and the column names overrides match."""
    if isinstance(node, dict):
        for key, value in node.items():
            if key in ("title", "description", "text") and isinstance(value, str):
                node[key] = en(value)
            elif key == "renameByName":
                node[key] = {k: en(v) for k, v in value.items()}
            elif key == "matcher" and value.get("id") == "byName":
                value["options"] = en(value["options"])
            else:
                translate(value)
    elif isinstance(node, list):
        for item in node:
            translate(item)


DS_INPUT = {"type": "prometheus", "uid": "${DS_PROMETHEUS}"}


def classic_panel(spec, panel_id, x, y, w, h):
    viz = spec["vizConfig"]
    targets = []
    for q in spec["data"]["spec"]["queries"]:
        target = {"refId": q["spec"]["refId"], "datasource": DS_INPUT}
        target.update(q["spec"]["query"]["spec"])
        targets.append(target)
    return {
        "id": panel_id,
        "type": viz["group"],
        "title": spec["title"],
        "description": spec["description"],
        "datasource": DS_INPUT,
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "targets": targets,
        "options": viz["spec"]["options"],
        "fieldConfig": viz["spec"]["fieldConfig"],
        "transformations": [{"id": t["spec"]["id"], "options": t["spec"]["options"]}
                            for t in spec["data"]["spec"]["transformations"]],
    }


def classic(v2):
    """The v2 dashboard in English and the classic JSON model, with a
    datasource input."""
    # A JSON round trip, not deepcopy: panels share override objects, and a
    # shared object would be translated twice.
    v2 = json.loads(json.dumps(v2))
    translate(v2["spec"])
    panels, y, panel_id = [], 0, 100
    for index, tab_ in enumerate(v2["spec"]["layout"]["spec"]["tabs"]):
        for row in tab_["spec"]["layout"]["spec"]["rows"]:
            grid = row["spec"]["layout"]["spec"]
            columns, height = grid["maxColumnCount"], grid["rowHeight"]
            w = 24 // columns
            h = 4 if height <= 1 else 2 * height
            collapsed = index > 0
            panel_id += 1
            header = {"id": panel_id, "type": "row", "collapsed": collapsed,
                      "title": f'{tab_["spec"]["title"]} · {row["spec"]["title"]}',
                      "gridPos": {"x": 0, "y": y, "w": 24, "h": 1}, "panels": []}
            panels.append(header)
            y += 1
            children = []
            for i, item in enumerate(grid["items"]):
                panel_id += 1
                element = v2["spec"]["elements"][item["spec"]["element"]["name"]]
                children.append(classic_panel(element["spec"], panel_id,
                                              (i % columns) * w, y + (i // columns) * h, w, h))
            y += h * -(-len(grid["items"]) // columns)
            if collapsed:
                header["panels"] = children
            else:
                panels.extend(children)
    spec = v2["spec"]
    out = {
        "__inputs": [{"name": "DS_PROMETHEUS", "label": "Prometheus", "type": "datasource",
                      "pluginId": "prometheus", "pluginName": "Prometheus",
                      "description": "The Prometheus that scrapes PSSST and loads its rules."}],
        "__requires": [
            {"type": "grafana", "id": "grafana", "name": "Grafana", "version": "10.0.0"},
            {"type": "datasource", "id": "prometheus", "name": "Prometheus", "version": "1.0.0"},
            {"type": "panel", "id": "stat", "name": "Stat", "version": ""},
            {"type": "panel", "id": "table", "name": "Table", "version": ""},
            {"type": "panel", "id": "timeseries", "name": "Time series", "version": ""},
        ],
        "uid": "pssst-status",
        "title": spec["title"],
        "description": spec["description"],
        "tags": ["pssst", "payments", "status-page", "blackbox"],
        "editable": True,
        "graphTooltip": 0,
        "time": {"from": spec["timeSettings"]["from"], "to": spec["timeSettings"]["to"]},
        "timepicker": {"refresh_intervals": spec["timeSettings"]["autoRefreshIntervals"]},
        "refresh": spec["timeSettings"]["autoRefresh"],
        "timezone": spec["timeSettings"]["timezone"],
        "schemaVersion": 39,
        "templating": {"list": []},
        "annotations": {"list": []},
        "links": [{"type": "link", "title": "PSSST on GitHub", "url": "https://github.com/adeleglise/pssst",
                   "targetBlank": True, "icon": "external link", "tags": [], "asDropdown": False,
                   "includeVars": False, "keepTime": False, "tooltip": ""}],
        "panels": panels,
    }
    return out


if sys.argv[1:] == ["--grafana-com"]:
    print(json.dumps(classic(dashboard), ensure_ascii=False, indent=2))
elif sys.argv[1:]:
    sys.exit("usage: build_dashboard.py [--grafana-com]")
else:
    print(json.dumps(dashboard, ensure_ascii=False, indent=2))
