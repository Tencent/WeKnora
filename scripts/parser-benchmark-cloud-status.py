"""Read WeKnora Cloud quota metadata without submitting a parsing task."""
import base64
import hashlib
import importlib.util
import json
from pathlib import Path
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

loader = importlib.util.spec_from_file_location('parser_launcher', Path(__file__).with_name('run-parser-benchmark.py'))
launcher = importlib.util.module_from_spec(loader)
loader.loader.exec_module(launcher)


def main():
    c = launcher.read_credentials()
    cloud = c['cloud']
    value = cloud['app_secret']
    if value.startswith('enc:v1:'):
        b = base64.urlsafe_b64decode(value[7:] + '=' * (-len(value[7:]) % 4))
        value = AESGCM(c['aes_key'].encode()).decrypt(b[:12], b[12:], None).decode()
    params = {'x-appid': cloud['app_id'], 'x-api-key': value, 'x-request-id': str(uuid.uuid4()), 'x-timestamp': str(int(time.time())), 'x-nonce': secrets.token_hex(8), 'body': hashlib.md5(b'{}').hexdigest()}
    raw = '&'.join(urllib.parse.quote(k, safe='-_.~') + '=' + urllib.parse.quote(params[k], safe='-_.~') for k in sorted(params))
    headers = {k: v for k, v in params.items() if k != 'body'}
    headers['x-signature'] = hashlib.md5(raw.encode()).hexdigest()
    request = urllib.request.Request('https://weknora.weixin.qq.com/api/v1/quotas', data=b'{}', headers=headers, method='GET')
    result = {'checked_at_unix': int(time.time()), 'provider': 'weknoracloud', 'parsing_submissions': 0}
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            body = json.load(response)
            result['http_status'] = response.status
            result['quotas'] = {k: {f: v.get(f) for f in ('used', 'limit')} for k, v in body.get('quotas', {}).items() if isinstance(v, dict)}
            result['quota_visible'] = bool(result['quotas'])
    except urllib.error.HTTPError as exc:
        result['http_status'] = exc.code
    except Exception as exc:
        result['error_type'] = type(exc).__name__
    print(json.dumps(result))


if __name__ == '__main__':
    main()
