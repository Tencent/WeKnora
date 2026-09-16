"""Offline tests for explicit parser configuration and native preflight boundaries."""
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('launcher',Path(__file__).with_name('run-parser-benchmark.py'))
launcher=importlib.util.module_from_spec(spec);spec.loader.exec_module(launcher)

class LauncherTests(unittest.TestCase):
    def test_default_configuration_never_discovers_credentials(self):
        with patch.dict(os.environ,{},clear=True), patch.object(launcher.subprocess,'run',side_effect=AssertionError('no discovery')):
            value=launcher.read_credentials(native=True)
        self.assertEqual(set(value),{'parser_config'})
        self.assertEqual(value['parser_config']['mineru_endpoint'],'http://127.0.0.1:18081')

    def test_explicit_stream_preserves_routing_and_avoids_file(self):
        payload={'parser_config':{'mineru_endpoint':'http://isolated.example:8000'},'cloud':{'app_id':'synthetic'}}
        with patch.dict(os.environ,{'PARSER_BENCHMARK_CREDENTIALS_FILE':'missing-private-file'}):
            value=launcher.read_credentials(stream=io.BytesIO(json.dumps(payload).encode()))
        self.assertEqual(value['cloud'],payload['cloud'])
        self.assertEqual(value['parser_config']['mineru_endpoint'],'http://isolated.example:8000')

    def test_rejects_invalid_and_oversized_private_input(self):
        for data in [b'[]',b'{"parser_config":[]}',b'x'*(1024*1024+1)]:
            with self.subTest(size=len(data)),self.assertRaises(ValueError):
                launcher.read_credentials(stream=io.BytesIO(data))

    def test_native_preflight_does_not_read_credentials_or_start_docker(self):
        calls=[]
        class Process:
            returncode=0
            def __init__(self,command,**kwargs):calls.append(command)
            def communicate(self,payload):self.payload=payload;calls.append(payload)
        with tempfile.TemporaryDirectory() as directory:
            args=['run-parser-benchmark.py','--native','--engine','builtin','--manifest',str(Path(directory)/'input.json'),
                  '--output',str(Path(directory)/'out'),'--binary',str(Path(directory)/'parser')]
            with patch.object(launcher.sys,'argv',args),patch.object(launcher,'read_credentials',side_effect=AssertionError('no credential read')):
                with patch.object(launcher.subprocess,'Popen',Process):self.assertEqual(launcher.main(),0)
        self.assertNotIn('docker',calls[0]);self.assertEqual(calls[1],b'')

if __name__=='__main__':unittest.main()
