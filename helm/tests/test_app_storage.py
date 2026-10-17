"""Render-only checks for App rollout strategies and documented storage topologies.

Run with Helm on PATH and PyYAML installed:
    python3 -m unittest discover -s helm/tests -v
"""

import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

import yaml


CHART = Path(os.environ.get("HELM_CHART_DIR", Path(__file__).resolve().parents[1]))


def render(values=None):
    with tempfile.TemporaryDirectory() as directory:
        arguments = []
        for index, layer in enumerate(values if isinstance(values, list) else [values or {}]):
            path = Path(directory) / f"values-{index}.yaml"
            path.write_text(yaml.safe_dump(layer))
            arguments.extend(["-f", str(path)])
        return subprocess.run(
            [
                "helm", "template", "audit", str(CHART),
                "--namespace", "audit",
                "--set", "secrets.existingSecret=fixture-secret",
                *arguments,
            ],
            capture_output=True, text=True,
        )


class AppStorageTests(unittest.TestCase):
    def documents(self, values=None):
        result = render(values)
        self.assertEqual(result.returncode, 0, result.stderr)
        return [doc for doc in yaml.safe_load_all(result.stdout) if doc]

    def app(self, documents):
        return next(doc for doc in documents
                    if doc["kind"] == "Deployment"
                    and doc["metadata"]["name"] == "audit-weknora-app")

    def assert_data_claim(self, app, name):
        volumes = app["spec"]["template"]["spec"]["volumes"]
        volume = next(v for v in volumes if v["name"] == "data-files")
        self.assertEqual(volume["persistentVolumeClaim"]["claimName"], name)

    def readme_values(self, filename):
        blocks = re.findall(r"```yaml\n(.*?)\n```", (CHART / "README.md").read_text(), re.S)
        return yaml.safe_load(next(block for block in blocks
                                   if block.startswith(f"# {filename}\n")))

    def test_default_strategy_is_unchanged(self):
        app = self.app(self.documents())
        self.assertEqual(app["spec"]["strategy"], {
            "type": "RollingUpdate",
            "rollingUpdate": {"maxSurge": 1, "maxUnavailable": 0},
        })

    def test_recreate_omits_rolling_update(self):
        app = self.app(self.documents({"app": {"strategy": {"type": "Recreate"}}}))
        self.assertEqual(app["spec"]["strategy"], {"type": "Recreate"})

    def test_custom_rolling_update_preserves_zero_and_percentage(self):
        app = self.app(self.documents({"app": {"strategy": {
            "type": "RollingUpdate",
            "rollingUpdate": {"maxSurge": 0, "maxUnavailable": "25%"},
        }}}))
        self.assertEqual(app["spec"]["strategy"]["rollingUpdate"], {
            "maxSurge": 0, "maxUnavailable": "25%",
        })

    def test_null_strategy_falls_back_to_defaults(self):
        app = self.app(self.documents({"app": {"strategy": None}}))
        self.assertEqual(app["spec"]["strategy"], {
            "type": "RollingUpdate",
            "rollingUpdate": {"maxSurge": 1, "maxUnavailable": 0},
        })

    def test_invalid_strategy_is_rejected(self):
        result = render({"app": {"strategy": {"type": "Invalid"}}})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("app.strategy.type must be RollingUpdate or Recreate", result.stderr)

    def test_production_example_uses_single_replica_rwo_and_recreate(self):
        documents = self.documents(self.readme_values("values-production.yaml"))
        app = self.app(documents)
        self.assertEqual(app["spec"]["replicas"], 1)
        self.assertEqual(app["spec"]["strategy"], {"type": "Recreate"})
        self.assert_data_claim(app, "audit-weknora-data-files")
        pvc = next(doc for doc in documents
                   if doc["kind"] == "PersistentVolumeClaim"
                   and doc["metadata"]["name"] == "audit-weknora-data-files")
        self.assertEqual(pvc["spec"]["accessModes"], ["ReadWriteOnce"])
        env = app["spec"]["template"]["spec"]["containers"][0]["env"]
        self.assertIn({"name": "STORAGE_TYPE", "value": "local"}, env)

    def data_files_claim(self, documents):
        return next(doc for doc in documents
                    if doc["kind"] == "PersistentVolumeClaim"
                    and doc["metadata"]["name"] == "audit-weknora-data-files")

    def test_data_files_claim_defaults_to_read_write_once(self):
        pvc = self.data_files_claim(self.documents())
        self.assertEqual(pvc["spec"]["accessModes"], ["ReadWriteOnce"])

    def test_data_files_access_modes_are_configurable(self):
        """A shared filesystem can keep the chart's rolling-update default.

        The claim was hard-coded to ReadWriteOnce, so an RWX storage class
        could not be used for the chart-created volume at all: the upgrade
        then failed with Multi-Attach and the only way out was an
        ``existingClaim``. Setting the access modes lets that deployment keep
        ``app.strategy.type: RollingUpdate``.
        """
        documents = self.documents({"dataFiles": {"persistence": {
            "accessModes": ["ReadWriteMany"],
        }}})
        self.assertEqual(self.data_files_claim(documents)["spec"]["accessModes"],
                         ["ReadWriteMany"])
        app = self.app(documents)
        self.assertEqual(app["spec"]["strategy"]["type"], "RollingUpdate")

    def test_shared_storage_example_references_existing_claim(self):
        values = self.readme_values("values-production.yaml")
        override = self.readme_values("values-shared-storage.yaml")
        documents = self.documents([values, override])
        app = self.app(documents)
        self.assertEqual(app["spec"]["replicas"], 3)
        self.assertEqual(app["spec"]["strategy"]["type"], "RollingUpdate")
        self.assert_data_claim(app, "weknora-shared-files")
        self.assertFalse(any(doc["kind"] == "PersistentVolumeClaim"
                             and doc["metadata"]["name"] == "audit-weknora-data-files"
                             for doc in documents))


if __name__ == "__main__":
    unittest.main()
