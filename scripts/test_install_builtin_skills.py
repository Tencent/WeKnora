#!/usr/bin/env python3
"""Regression checks for the image's resource/environment consistency gate."""
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('installer', Path(__file__).resolve().parents[1] / 'docker/install-builtin-skills.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class ManifestVerificationTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.skill = self.root / 'pdf'
        self.skill.mkdir()
        (self.skill / 'requirements.lock').write_text('pypdf==6.0.0\n')
        (self.skill / 'SKILL.md').write_text('reviewed instructions\n')
        (self.skill / '.requirements-digest').write_text(hashlib.sha256((self.skill / 'requirements.lock').read_bytes()).hexdigest())
        self.manifest = {'schema_version': 1, 'profile': 'office-core', 'version': 'test',
                         'skills': [{'name': 'pdf', 'digest': installer.resource_digest(self.skill), 'verified': True}]}
        patcher = patch.object(installer, 'ROOT', self.root)
        patcher.start()
        self.addCleanup(patcher.stop)

    def test_modified_instructions_fail_before_smoke(self):
        (self.skill / 'SKILL.md').write_text('changed instructions')
        with patch.object(installer, 'run') as run:
            with self.assertRaisesRegex(ValueError, 'resource digest mismatch'):
                installer.verify(self.manifest)
            run.assert_not_called()

    def test_changed_lock_cannot_reuse_an_old_environment(self):
        (self.skill / 'requirements.lock').write_text('pypdf==5.0.0\n')
        self.manifest['skills'][0]['digest'] = installer.resource_digest(self.skill)
        with self.assertRaisesRegex(ValueError, 'installed dependency lock mismatch'):
            installer.verify(self.manifest)

    def test_changed_node_lock_cannot_reuse_environment(self):
        (self.skill / 'package.json').write_text('{}')
        (self.skill / 'package-lock.json').write_text('{}')
        (self.skill / '.package-lock-digest').write_text(installer.node_digest(self.skill))
        (self.skill / 'package-lock.json').write_text('{"changed":true}')
        self.manifest['skills'][0]['digest'] = installer.resource_digest(self.skill)
        with self.assertRaisesRegex(ValueError, 'Node dependency lock mismatch'):
            installer.verify(self.manifest)

    def test_missing_or_extra_package_and_duplicate_declaration(self):
        self.manifest['skills'].append(dict(self.manifest['skills'][0]))
        with self.assertRaisesRegex(ValueError, 'manifest skill names'):
            installer.verify(self.manifest)
        self.manifest['skills'].pop()
        (self.root / 'browser').mkdir()
        with self.assertRaisesRegex(ValueError, 'manifest skill names'):
            installer.verify(self.manifest)
        self.manifest['skills'].append({'name': 'browser'})
        with self.assertRaisesRegex(ValueError, 'non-core skills'):
            installer.verify(self.manifest)

    def test_retired_profiles_are_rejected(self):
        for profile in ('office', 'office-browser', 'office-core-browser'):
            with self.subTest(profile=profile):
                self.manifest['profile'] = profile
                with self.assertRaisesRegex(ValueError, 'unsupported builtin manifest'):
                    installer.verify(self.manifest)

    def test_runtime_files_do_not_change_resource_digest(self):
        expected = installer.resource_digest(self.skill)
        (self.skill / '.venv').mkdir()
        (self.skill / '.venv' / 'installed.py').write_text('runtime')
        (self.skill / '.bundle-digest').write_text('recorded')
        (self.skill / 'node_modules').mkdir()
        (self.skill / 'node_modules/runtime.js').write_text('runtime')
        (self.skill / '.package-lock-digest').write_text('installed')
        self.assertEqual(expected, installer.resource_digest(self.skill))


if __name__ == '__main__':
    unittest.main()
