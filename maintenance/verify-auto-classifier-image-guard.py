#!/usr/bin/env python3
"""Actual local binary -> TLS fake upstream; synthetic payloads, no model use."""
from pathlib import Path
import hashlib
import http.server
import json
import os
import queue
import secrets
import ssl
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request

BINARY = Path('/artifact') / sys.argv[1]
EXPECTED = sys.argv[2]
GUARDED = sys.argv[3] == 'guarded'
WORK = Path('/work')
assert hashlib.sha256(BINARY.read_bytes()).hexdigest() == EXPECTED
MONITOR = 'You are a security monitor for autonomous AI coding agents.'
S1 = '\nStage 1 does NOT apply user intent or ALLOW exceptions — stage 2 will handle those. Your ENTIRE response MUST begin with <block>.'
S2 = '\nReview the classification process and follow it carefully. Use <thinking> before responding with <block>.'
IMAGE = 'A' * 1400000
IMAGE_MARKDOWN = '\n\n![generated image](data:image/png;base64,' + IMAGE + ')'
captured = queue.Queue()
scenario = {}


class Upstream(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        if self.path != '/backend-api/codex/responses':
            self.send_error(404)
            return
        request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        hosted = sum(t.get('type') == 'image_generation' for t in request.get('tools', []) if isinstance(t, dict))
        choice = request.get('tool_choice')
        if isinstance(choice, dict):
            choice = choice.get('type')
        case = scenario
        instructions = [p.get('text', '') for item in request.get('input', []) if item.get('role') == 'developer' for p in item.get('content', []) if isinstance(p, dict)]
        users = [p.get('text', '') for item in request.get('input', []) if item.get('role') == 'user' for p in item.get('content', []) if isinstance(p, dict) and p.get('type') == 'input_text']
        wire = {'hosted_image_tool_count': hosted, 'tool_choice': choice,
                'bridge_guidance_present': '<cliproxy-codex-image-generation>' in request.get('instructions', ''),
                'system_preserved': case['system'] in instructions,
                'user_preserved': case['user'] in users,
                'upstream_stream': request.get('stream')}
        captured.put(wire)
        output = []
        if hosted or case.get('unsolicited_image'):
            output.append({'id': 'ig1', 'type': 'image_generation_call', 'status': 'completed',
                           'output_format': 'png', 'result': IMAGE})
        if case.get('thinking'):
            output.append({'id': 'reason1', 'type': 'reasoning', 'summary': [{'type': 'summary_text', 'text': 'Synthetic independent reasoning.'}]})
        item = {'id': 'm1', 'type': 'message', 'role': 'assistant', 'status': 'completed',
                'content': [{'type': 'output_text', 'text': case['verdict']}]}
        output.append(item)
        response = {'id': 'r1', 'model': 'gpt-6.1-terra', 'status': 'completed', 'output': output,
                    'usage': {'input_tokens': 11, 'output_tokens': 44, 'total_tokens': 55}}
        events = [{'type': 'response.created', 'response': {'id': 'r1', 'model': 'gpt-6.1-terra'}}]
        for index, obj in enumerate(output):
            added = dict(obj)
            if obj['type'] == 'message':
                added['content'] = []
            events.extend([{'type': 'response.output_item.added', 'output_index': index, 'item': added},
                           {'type': 'response.output_item.done', 'output_index': index, 'item': obj}])
        events.append({'type': 'response.completed', 'response': response})
        raw = ''.join('data: ' + json.dumps(e) + '\n\n' for e in events).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Content-Length', str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


def classifier(name, keyword='image generation', suffix=S1, **options):
    return dict(name=name, system=MONITOR, user='<transcript>Quoted capability: ' + keyword + '. Proposed command: date -u -Is.</transcript>' + suffix,
                verdict='<block>no</block>', classifier=True, **options)


cases = [
    classifier('stage1_system_string'),
    classifier('stage1_system_blocks', blocks=True),
    classifier('stage1_attribution_system', blocks=True, attribution=True),
    classifier('stage2_attribution_system', suffix=S2, blocks=True, attribution=True),
    classifier('stage2_system_string', suffix=S2),
    classifier('stage2_system_blocks', suffix=S2, blocks=True),
    classifier('quoted_chinese', keyword='生图'),
    classifier('quoted_local_tool', keyword='image_gen.imagegen'),
    classifier('no_image_keyword', keyword='ordinary shell access'),
    classifier('native_thinking', thinking=True),
    classifier('unsolicited_image_not_clipped', unsolicited_image=True),
    classifier('classifier_empty_tools', tools=[]),
    dict(name='normal_generate_nonstream', system='You are a helpful assistant.', user='Generate an image of a cat.', verdict='Synthetic image.', classifier=False),
    dict(name='normal_generate_stream', system='You are a helpful assistant.', user='Generate an image of a cat.', verdict='Synthetic image.', classifier=False, stream=True),
    dict(name='local_image_tool', system='You are a helpful assistant.', user='Generate an image of a cat.', verdict='Use the local tool.', classifier=False,
         tools=[{'name': 'image_gen.imagegen', 'description': 'Local test image tool.', 'input_schema': {'type': 'object', 'properties': {}}}], want_local=True),
    dict(name='quoted_monitor_not_identity', system='You are a helpful assistant.', user=MONITOR+'<transcript>image generation</transcript>'+S1, verdict='Synthetic image.', classifier=False),
    dict(name='unknown_stage_unchanged', system=MONITOR, user='<transcript>image generation</transcript>Unknown future stage.', verdict='Synthetic image.', classifier=False),
    dict(name='stage_inside_transcript_unchanged', system=MONITOR, user='<transcript>image generation'+S1+'</transcript>Explain the code.', verdict='Synthetic image.', classifier=False),
]
cases.append(dict(classifier('block_verdict_preserved'), verdict='<block>yes</block>'))
cases.append(dict(classifier('extra_classifier_text_not_clipped'), verdict='<block>no</block>\nEXTRA_CLASSIFIER_TEXT'))

server_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
server_context.minimum_version = server_context.maximum_version = ssl.TLSVersion.TLSv1_2
server_context.load_cert_chain('/certs/loopback.crt', '/certs/loopback.key')
upstream = http.server.ThreadingHTTPServer(('127.0.0.1', 18992), Upstream)
upstream.socket = server_context.wrap_socket(upstream.socket, server_side=True)
upstream.daemon_threads = True
threading.Thread(target=upstream.serve_forever, daemon=True).start()
client_key = secrets.token_urlsafe(32)
config = '''host: "127.0.0.1"
port: 18991
auth-dir: "/work/auths"
allow-unauthenticated: false
tls:
  enable: true
  cert: "/certs/loopback.crt"
  key: "/certs/loopback.key"
api-keys:
  - "CLIENT_KEY_PLACEHOLDER"
request-retry: 0
disable-cooling: true
postgres:
  dsn: "postgres://localprobe:synthetic-only@pg:5432/localprobe?sslmode=verify-full&sslrootcert=/certs/ca.crt"
redis:
  enable: false
account-status-refresh:
  enabled: false
codex-api-key:
  - api-key: "fake-loopback-only"
    base-url: "https://127.0.0.1:18992/backend-api/codex"
    models:
      - name: "gpt-6.1-terra"
        alias: "gpt-6.1-terra"
'''.replace('CLIENT_KEY_PLACEHOLDER', client_key)
(WORK/'config.yaml').write_text(config)
env = dict(os.environ, CLIRELAY_ADMIN_PASSWORD='SyntheticLocal-Only-8x!5', AUTH_PATH=str(WORK/'auths'),
           SSL_CERT_FILE='/certs/ca.crt', HTTPS_PROXY='', HTTP_PROXY='', ALL_PROXY='', https_proxy='', http_proxy='', all_proxy='')
client_context = ssl.create_default_context(cafile='/certs/ca.crt')
client_context.minimum_version = client_context.maximum_version = ssl.TLSVersion.TLSv1_2
http = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=client_context))
results = []
process = None
try:
    with (WORK/'synthetic-server.log').open('wb') as log:
        process = subprocess.Popen([str(BINARY), '-config', str(WORK/'config.yaml')], cwd=WORK, env=env, stdout=log, stderr=subprocess.STDOUT)
        deadline = time.monotonic()+45
        while True:
            if process.poll() is not None:
                raise RuntimeError('Isolated binary exited before ready')
            try:
                with http.open('https://127.0.0.1:18991/readyz', timeout=1) as response:
                    if response.status == 204:
                        break
            except (OSError, urllib.error.URLError):
                pass
            if time.monotonic() >= deadline:
                raise RuntimeError('Isolated ready deadline exceeded')
            time.sleep(.1)
        try:
            http.open('https://127.0.0.1:18991/v1/models', timeout=2)
        except urllib.error.HTTPError as error:
            assert error.code == 401
        else:
            raise AssertionError('Unauthenticated API request was not rejected')
        for case in cases:
            scenario = case
            system = [{'type': 'text', 'text': case['system']}] if case.get('blocks') else case['system']
            if case.get('attribution'):
                system.insert(0, {'type': 'text', 'text': 'x-anthropic-billing-header: synthetic-test-only'})
            body = {'model': 'gpt-6.1-terra', 'max_tokens': 2112, 'stream': case.get('stream', False), 'system': system,
                    'messages': [{'role': 'user', 'content': case['user']}]}
            if case['classifier']:
                body['stop_sequences'] = ['</block>']
            if 'tools' in case:
                body['tools'] = case['tools']
            request = urllib.request.Request('https://127.0.0.1:18991/v1/messages?beta=true', data=json.dumps(body).encode(),
                headers={'Content-Type': 'application/json', 'Authorization': 'Bearer '+client_key, 'anthropic-version': '2023-06-01'})
            with http.open(request, timeout=20) as response:
                raw = response.read()
                status = response.status
            wire = captured.get(timeout=1)
            want_hosted = 0 if case.get('want_local') or (GUARDED and case['classifier']) else 1
            assert wire['hosted_image_tool_count'] == want_hosted, case['name']+' hosted tool mismatch'
            assert wire['bridge_guidance_present'] == bool(want_hosted), case['name']+' guidance mismatch'
            assert wire['upstream_stream'] is True and wire['system_preserved'] and wire['user_preserved'], case['name']+' original input changed'
            if want_hosted and case['name'] != 'no_image_keyword':
                assert wire['tool_choice'] == 'image_generation', case['name']+' expected old image choice'
            else:
                assert wire['tool_choice'] != 'image_generation', case['name']+' unexpected forced choice'
            if case.get('stream'):
                events = [json.loads(line[6:]) for line in raw.decode().splitlines() if line.startswith('data: {')]
                text = ''.join(e.get('delta', {}).get('text', '') for e in events if e.get('type') == 'content_block_delta')
                assert any(e.get('type') == 'message_stop' for e in events), 'missing stream termination'
                assert case['verdict'] in text and IMAGE in text, 'normal streaming image lost'
            else:
                value = json.loads(raw)
                text = ''.join(b.get('text', '') for b in value.get('content', []) if b.get('type') == 'text')
                expected = case['verdict'] + (IMAGE_MARKDOWN if want_hosted or case.get('unsolicited_image') else '')
                assert text == expected, case['name']+' response text changed, length='+str(len(text))
                assert value['usage']['output_tokens'] == 44 and value['stop_reason'] == 'end_turn', case['name']+' usage or termination changed'
                if case.get('thinking'):
                    assert any(b.get('type') == 'thinking' for b in value['content']), 'native thinking lost'
            results.append({'case': case['name'], 'passed': True, 'http_status': status, 'text_characters': len(text),
                            'inline_image_present': 'data:image/' in text,
                            'pure_allow_format': text.strip() in {'<block>no', '<block>no</block>'}, 'wire': wire})
finally:
    if process is not None and process.poll() is None:
        process.terminate()
        try: process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill(); process.wait(timeout=5)
    upstream.shutdown(); upstream.server_close()
    result = {'binary_sha256': EXPECTED, 'guard_expected': GUARDED, 'production_used': False, 'real_credentials_used': False,
              'service_stopped': process is None or process.poll() is not None, 'results': results}
    (WORK/'result.json').write_text(json.dumps(result, indent=2))
print(json.dumps({'binary_sha256': EXPECTED, 'passed_cases': len(results), 'guard_expected': GUARDED}))
