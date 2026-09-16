"""Offline fault-injection checks for the long CPU benchmark controller."""
import argparse
import copy
import importlib.util
import json
import subprocess
import tempfile
import unittest
from contextlib import ExitStack
from pathlib import Path
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location('cpu_batch', Path(__file__).with_name('run-parser-benchmark-cpu-batch.py'))
batch = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(batch)


class CpuBatchTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.samples = []
        for sample_id in ('a', 'b'):
            content = b'%PDF-1.7\n' + sample_id.encode()
            (self.root / (sample_id + '.pdf')).write_bytes(content)
            self.samples.append({'id': sample_id, 'sha256': batch.digest(content), 'pdf_path': sample_id + '.pdf'})
        self.manifest = self.root / 'full.json'
        self.manifest.write_bytes(json.dumps({'samples': self.samples, 'split': 'full'}, indent=3).encode() + b'\n')
        self.identity = {'source_commit': 'fixed-source', 'binary_sha256': 'binary-hash',
                         'config': {'paddleocr_vl_endpoint': 'http://weknora-parser-paddle:8080'}}
        self.steps = []
        self.statuses = {}
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.stack.enter_context(patch.object(batch, 'build_identity', return_value={}))
        self.stack.enter_context(patch.object(batch, 'verify_models'))
        self.stack.enter_context(patch.object(batch, 'current_identity', side_effect=lambda *args: copy.deepcopy(self.identity)))
        self.stack.enter_context(patch.object(batch, 'wait_health', side_effect=lambda *args: self.steps.append('health')))
        self.stack.enter_context(patch.object(batch, 'docker', side_effect=self.docker))
        self.stack.enter_context(patch.object(batch, 'process_alive', return_value=False))
        self.popen = self.stack.enter_context(patch.object(batch.subprocess, 'Popen', side_effect=self.launch))
        self.stack.enter_context(patch('builtins.print'))

    def controller(self, execute=True, postprocess=None):
        return batch.Batch(self.root, argparse.Namespace(manifest='full.json', output='runs', batch_dir='batch',
                           build_identity='build.json', execute=execute, health_timeout=1,
                           postprocess_json=str(postprocess) if postprocess else None))

    def docker(self, *arguments, **kwargs):
        self.steps.append('docker-' + arguments[0])
        return ''

    def write_result(self, page, status='success', error=''):
        sample = page['samples'][0]
        output = self.root / 'runs' / batch.ENGINE
        output.mkdir(parents=True, exist_ok=True)
        markdown = b'recognized text' if status == 'success' else b''
        (output / (sample['id'] + '.md')).write_bytes(markdown)
        record = {'engine': batch.ENGINE, 'sample_id': sample['id'], 'input_sha256': sample['sha256'],
                  'manifest_sha256': batch.digest(batch.encoded(page)), 'source_commit': 'fixed-source',
                  'config': self.identity['config'], 'markdown_sha256': batch.digest(markdown),
                  'status': status, 'error': error, 'duration_ms': 1000001 if error else 100}
        # Deliberately exercise the baseline binary's schema without binary_sha256.
        (output / (sample['id'] + '.json')).write_bytes(batch.encoded(record))

    def launch(self, command, **kwargs):
        self.assertEqual(command[command.index('--engine') + 1], 'paddleocr_vl')
        self.assertEqual(command[command.index('--timeout') + 1], '30m')
        self.assertIn('--execute', command)
        self.assertEqual(kwargs['stdout'], subprocess.DEVNULL)
        page = batch.read_json(Path(command[command.index('--manifest') + 1]))
        sample_id = page['samples'][0]['id']
        self.steps.append('launch-' + sample_id)
        status, error = self.statuses.get(sample_id, ('success', ''))
        owner = self

        class Process:
            pid = 987654

            def wait(self, timeout):
                owner.steps.append('client-exit-' + sample_id)
                owner.write_result(page, status, error)
                return 0

        return Process()

    def test_prepare_preserves_parent_bytes_and_never_submits(self):
        original = self.manifest.read_bytes()
        self.assertEqual(self.controller(execute=False).run(), 0)
        self.assertEqual((self.root / 'batch/parent-manifest.json').read_bytes(), original)
        singles = list((self.root / 'batch/manifests').glob('*.json'))
        self.assertEqual(len(singles), 2)
        for single in singles:
            content = batch.read_json(single)
            self.assertEqual(content['cpu_batch']['parent_manifest_sha256'], batch.digest(original))
            self.assertEqual(len(content['samples']), 1)
            self.assertIn(content['samples'][0], self.samples)
        self.popen.assert_not_called()
        self.assertEqual(self.steps, [])
        self.assertFalse((self.root / 'batch/execution-complete.json').exists())

    def test_changed_parent_is_rejected_without_touching_frozen_files(self):
        self.controller(execute=False).run()
        frozen = (self.root / 'batch/parent-manifest.json').read_bytes()
        self.manifest.write_bytes(self.manifest.read_bytes() + b' ')
        with self.assertRaisesRegex(batch.BatchError, 'frozen_file_identity_conflict'):
            self.controller(execute=False).run()
        self.assertEqual((self.root / 'batch/parent-manifest.json').read_bytes(), frozen)

    def test_timeout_waits_for_client_exit_then_recovers_before_next_page(self):
        self.statuses['a'] = ('error', 'HTTP request: context deadline exceeded (Client.Timeout exceeded while awaiting headers)')
        self.assertEqual(self.controller().run(), 0)
        first_exit = self.steps.index('client-exit-a')
        stop = self.steps.index('docker-stop')
        next_launch = self.steps.index('launch-b')
        self.assertLess(first_exit, stop)
        self.assertLess(stop, next_launch)
        self.assertIn('health', self.steps[stop:next_launch])
        completion = batch.read_json(self.root / 'batch/execution-complete.json')
        self.assertEqual(completion['executed_pages'], 2)
        self.assertEqual(completion['result_counts'], {'error': 1, 'success': 1})
        self.assertFalse(completion['all_pages_successful'])
        first_raw = (self.root / 'runs/paddleocr_vl/a.json').read_bytes()
        self.assertEqual(self.controller().run(), 0)
        self.assertEqual(self.steps.count('launch-a'), 1)
        self.assertEqual(self.steps.count('launch-b'), 1)
        self.assertEqual(self.steps.count('docker-stop'), 1)
        self.assertEqual((self.root / 'runs/paddleocr_vl/a.json').read_bytes(), first_raw)

    def test_readiness_failure_never_becomes_a_quality_result(self):
        with patch.object(batch, 'wait_health', side_effect=batch.BatchError('paddle_health_timeout_no_page_submitted')):
            with self.assertRaises(batch.BatchError):
                self.controller().run()
        self.popen.assert_not_called()
        state = batch.read_json(self.root / 'batch/batch-state.json')
        self.assertEqual(state['remaining_sample_ids'], ['a', 'b'])
        self.assertFalse((self.root / 'runs/paddleocr_vl/a.json').exists())
        self.assertFalse((self.root / 'batch/batch-complete.json').exists())

    def test_markdown_tamper_is_rejected_without_overwrite_or_resubmit(self):
        self.controller().run()
        markdown = self.root / 'runs/paddleocr_vl/a.md'
        markdown.write_bytes(b'changed evidence')
        with self.assertRaisesRegex(batch.BatchError, 'raw_record_identity_conflict'):
            self.controller().run()
        self.assertEqual(markdown.read_bytes(), b'changed evidence')
        self.assertEqual(self.popen.call_count, 2)

    def test_binary_or_configuration_change_rejects_resume(self):
        self.controller().run()
        self.identity['binary_sha256'] = 'replacement-binary'
        with self.assertRaisesRegex(batch.BatchError, 'batch_runtime_identity_conflict'):
            self.controller().run()
        self.assertEqual(self.popen.call_count, 2)

    def test_foreign_legacy_record_without_submission_receipt_is_rejected(self):
        self.controller(execute=False).run()
        single = sorted((self.root / 'batch/manifests').glob('*.json'))[0]
        self.write_result(batch.read_json(single))
        with self.assertRaisesRegex(batch.BatchError, 'unowned_existing_evidence_preserved'):
            self.controller().run()
        self.popen.assert_not_called()

    def test_live_client_halts_and_missing_result_never_gets_resubmitted(self):
        class Hanging:
            pid = 987654

            def wait(self, timeout):
                raise subprocess.TimeoutExpired('redacted', timeout)

        with patch.object(batch.subprocess, 'Popen', return_value=Hanging()):
            with self.assertRaisesRegex(batch.BatchError, 'client_still_running'):
                self.controller().run()
        self.assertNotIn('docker-stop', self.steps)
        with patch.object(batch, 'process_alive', return_value=True):
            with self.assertRaisesRegex(batch.BatchError, 'previous_launcher_still_running'):
                self.controller().run()
        with self.assertRaisesRegex(batch.BatchError, 'batch_incomplete_indeterminate'):
            self.controller().run()
        self.assertEqual(self.popen.call_count, 1)  # Only the untouched second PDF.
        self.assertNotIn('launch-a', self.steps)
        self.assertIn('launch-b', self.steps)
        self.assertFalse((self.root / 'batch/execution-complete.json').exists())
        self.assertFalse((self.root / 'runs/paddleocr_vl/a.json').exists())

    def test_postprocess_is_structured_and_failure_does_not_claim_batch_complete(self):
        commands = self.root / 'postprocess.json'
        commands.write_text(json.dumps([['python', 'score.py', '--public']]))
        with patch.object(batch.subprocess, 'run', return_value=argparse.Namespace(returncode=9)) as run:
            with self.assertRaisesRegex(batch.BatchError, 'postprocess_failed'):
                self.controller(postprocess=commands).run()
        self.assertEqual(run.call_args.args[0], ['python', 'score.py', '--public'])
        self.assertIs(run.call_args.kwargs['shell'], False)
        self.assertTrue((self.root / 'batch/execution-complete.json').exists())
        self.assertFalse((self.root / 'batch/batch-complete.json').exists())
        self.assertEqual(self.popen.call_count, 2)
        with patch.object(batch.subprocess, 'run', return_value=argparse.Namespace(returncode=0)):
            self.assertEqual(self.controller(postprocess=commands).run(), 0)
        self.assertEqual(self.popen.call_count, 2)
        self.assertTrue((self.root / 'batch/batch-complete.json').exists())

    def test_crash_before_pid_saved_never_assumes_no_client_exists(self):
        class Hanging:
            pid = 987654

            def wait(self, timeout):
                raise subprocess.TimeoutExpired('redacted', timeout)

        with patch.object(batch.subprocess, 'Popen', return_value=Hanging()):
            with self.assertRaises(batch.BatchError):
                self.controller().run()
        state_path = self.root / 'batch/batch-state.json'
        state = batch.read_json(state_path)
        del state['pages']['a']['launcher_pid']
        batch.atomic_json(state_path, state)
        with self.assertRaisesRegex(batch.BatchError, 'submission_pid_unknown'):
            self.controller().run()
        self.assertNotIn('docker-stop', self.steps)
        self.popen.assert_not_called()

    def test_shell_string_is_rejected(self):
        commands = self.root / 'postprocess.json'
        commands.write_text(json.dumps(['python score.py && echo done']))
        with self.assertRaisesRegex(batch.BatchError, 'argument_arrays'):
            batch.load_commands(commands)


if __name__ == '__main__':
    unittest.main()
