"""Render the chart and check Service names together with their consumers.

Run: python3 -m unittest discover -s helm/tests -v (requires Helm and PyYAML).
HELM and CHART_DIR may point at another Helm binary or a baseline chart.
"""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import yaml


CHART = Path(os.environ.get("CHART_DIR", Path(__file__).resolve().parents[1]))
HELM = os.environ.get("HELM", "helm")
DEFAULT_NAMES = {
    "app": "app",
    "frontend": "frontend",
    "postgresql": "postgres",
    "redis": "redis",
    "docreader": "docreader",
    "neo4j": "neo4j",
}


def render(values=None, release="audit", notes=False, chart=CHART):
    if notes:
        # Render the real NOTES template through a temporary resource. Unlike
        # `helm install --dry-run`, this needs no API server on Helm 3 either.
        with tempfile.TemporaryDirectory() as directory:
            scratch = Path(directory) / "chart"
            shutil.copytree(chart, scratch, ignore=shutil.ignore_patterns("__pycache__"))
            (scratch / "templates" / "test-notes.yaml").write_text(
                'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: service-name-test-notes\n'
                'data:\n  notes: {{ include (print $.Template.BasePath "/NOTES.txt") . | toJson }}\n',
                encoding="utf-8",
            )
            docs = render(values, release, chart=scratch)
            return next(d["data"]["notes"] for d in docs
                        if d["metadata"]["name"] == "service-name-test-notes")
    config = {"secrets": {"existingSecret": "fixture-secret"}}
    config.update(values or {})
    args = [HELM, "template", release, str(chart),
            "--namespace", "shared", "-f", "-"]
    result = subprocess.run(args, input=yaml.safe_dump(config), text=True,
                            capture_output=True, check=True)
    return [doc for doc in yaml.safe_load_all(result.stdout) if doc]


def names_for(prefix):
    return {component: f"{prefix}-{name}" for component, name in DEFAULT_NAMES.items()}


def custom_values(names):
    return {component: {"service": {"name": name}} for component, name in names.items()}


def container_env(docs, component):
    deployment = next(doc for doc in docs if doc["kind"] == "Deployment"
                      and doc["metadata"]["labels"]["app.kubernetes.io/component"] == component)
    return {env["name"]: env.get("value")
            for env in deployment["spec"]["template"]["spec"]["containers"][0]["env"]}


class ServiceNamesTest(unittest.TestCase):
    def assert_references(self, docs, names, release="audit", frontend_host=None):
        services = {doc["metadata"]["name"]: doc for doc in docs if doc["kind"] == "Service"}
        self.assertEqual(set(names.values()), set(services))
        for service in services.values():
            selector = service["spec"]["selector"]
            self.assertEqual(release, selector["app.kubernetes.io/instance"])
            matches = [doc for doc in docs if doc["kind"] == "Deployment" and
                       all(doc["spec"]["template"]["metadata"]["labels"].get(k) == v
                           for k, v in selector.items())]
            self.assertEqual(1, len(matches), "Service must select its release's workload")
        app = container_env(docs, "app")
        self.assertEqual(names["postgresql"], app["DB_HOST"])
        self.assertEqual(f'{names["redis"]}:6379', app["REDIS_ADDR"])
        self.assertEqual(f'{names["docreader"]}:50051', app["DOCREADER_ADDR"])
        self.assertEqual(f'bolt://{names["neo4j"]}:7687', app["NEO4J_URI"])
        self.assertEqual(frontend_host or names["app"], container_env(docs, "frontend")["APP_HOST"])
        ingress = next(doc for doc in docs if doc["kind"] == "Ingress")
        backends = {path["path"]: path["backend"]["service"]["name"]
                    for path in ingress["spec"]["rules"][0]["http"]["paths"]}
        self.assertEqual({"/api": names["app"], "/": names["frontend"]}, backends)

    def test_default_names_and_references(self):
        docs = render({"neo4j": {"enabled": True}, "ingress": {"enabled": True}})
        self.assert_references(docs, DEFAULT_NAMES)
        self.assertIn("kubectl port-forward svc/frontend -n shared", render(notes=True))

    def test_custom_names_and_references(self):
        names = names_for("weknora")
        values = custom_values(names)
        values["neo4j"]["enabled"] = True
        values["ingress"] = {"enabled": True}
        self.assert_references(render(values), names)
        values["ingress"]["enabled"] = False
        notes = render(values, notes=True)
        self.assertIn("kubectl port-forward svc/weknora-frontend -n shared", notes)
        self.assertIn("kubectl port-forward svc/weknora-neo4j -n shared", notes)

    def test_partial_override_and_explicit_frontend_host(self):
        names = dict(DEFAULT_NAMES, app="weknora-app")
        values = {"app": {"service": {"name": names["app"]}}, "neo4j": {"enabled": True},
                  "ingress": {"enabled": True},
                  "frontend": {"appHost": "external-app.example", "appPort": 9090}}
        docs = render(values)
        self.assert_references(docs, names, frontend_host="external-app.example")
        self.assertEqual("9090", container_env(docs, "frontend")["APP_PORT"])

    def test_empty_overrides_preserve_defaults(self):
        values = custom_values(dict.fromkeys(DEFAULT_NAMES, ""))
        values["neo4j"]["enabled"] = True
        values["ingress"] = {"enabled": True}
        self.assert_references(render(values), DEFAULT_NAMES)

    def test_disabled_components_do_not_create_services(self):
        names = names_for("weknora")
        values = custom_values(names)
        for component in ("postgresql", "redis", "docreader", "neo4j"):
            values[component]["enabled"] = False
        docs = render(values)
        self.assertEqual({names["app"], names["frontend"]},
                         {d["metadata"]["name"] for d in docs if d["kind"] == "Service"})
        app = container_env(docs, "app")
        self.assertEqual(names["postgresql"], app["DB_HOST"])
        self.assertEqual(f'{names["redis"]}:6379', app["REDIS_ADDR"])
        self.assertEqual(f'{names["docreader"]}:50051', app["DOCREADER_ADDR"])
        self.assertNotIn("NEO4J_URI", app)
        values["app"]["enabled"] = False
        values["frontend"]["enabled"] = False
        self.assertFalse(any(d["kind"] == "Service" for d in render(values)))

    def test_two_releases_have_disjoint_resource_identities(self):
        resources = []
        for release in ("alpha", "beta"):
            names = names_for(release)
            values = custom_values(names)
            values["neo4j"]["enabled"] = True
            values["ingress"] = {"enabled": True, "host": f"{release}.example.test"}
            docs = render(values, release=release)
            self.assert_references(docs, names, release=release)
            identities = {(d["apiVersion"], d["kind"], d["metadata"].get("namespace"),
                           d["metadata"]["name"]) for d in docs}
            self.assertEqual(len(docs), len(identities), "duplicate identities within a release")
            resources.append(identities)
        self.assertFalse(resources[0] & resources[1], "chart-owned resources collide across releases")


if __name__ == "__main__":
    unittest.main()
