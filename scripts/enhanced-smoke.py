#!/usr/bin/env python3
"""Run the compiled app against an isolated local alert source and persistent SQLite file."""
import argparse
import http.server
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', default='.build/alertmanager-ui')
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    class Source(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            data = [{'fingerprint': 'smoke', 'labels': {'alertname': 'SmokeWarning', 'namespace': 'smoke', 'severity': 'warning'}, 'annotations': {'summary': 'Synthetic persistent smoke alert'}, 'status': {'state': 'active'}}]
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps(data).encode())
        def log_message(self, *_):
            pass
    source = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Source)
    threading.Thread(target=source.serve_forever, daemon=True).start()
    proc = None
    try:
        with tempfile.TemporaryDirectory(prefix='alerts-enhanced-smoke-') as folder:
            config = Path(folder) / 'config.yaml'
            config.write_text(json.dumps({'listenAddr': '127.0.0.1:0', 'alertmanager': {'url': f'http://127.0.0.1:{source.server_port}'}, 'enhanced': {'enabled': True, 'databasePath': str(Path(folder) / 'workflow.db')}}))
            def start():
                with socket.socket() as sock:
                    sock.bind(('127.0.0.1', 0))
                    port = sock.getsockname()[1]
                env = {k:v for k,v in os.environ.items() if k not in ['ALERTMANAGER_URL','PROMETHEUS_URL','DEMO','READ_ONLY','ENHANCED','DATABASE_PATH','BASE_PATH']}
                env.update(CONFIG_FILE=str(config), LISTEN_ADDR=f'127.0.0.1:{port}')
                p = subprocess.Popen([binary], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                return p, f'http://127.0.0.1:{port}'
            def call(path, method='GET', data=None):
                req = urllib.request.Request(base+path, data=None if data is None else json.dumps(data).encode(), method=method, headers={'Content-Type':'application/json'})
                with urllib.request.urlopen(req, timeout=5) as response:
                    return json.load(response)
            def wait_incidents():
                for _ in range(100):
                    if proc.poll() is not None:
                        raise RuntimeError('app exited during startup')
                    try:
                        result=call('/api/incidents?limit=200&offset=0')
                        if result['incidents']:
                            return result
                    except (OSError, urllib.error.URLError):
                        pass
                    time.sleep(.05)
                raise RuntimeError('incident collection timed out')
            proc, base = start()
            first = wait_incidents()['incidents'][0]
            updated = call('/api/incidents/'+first['id'], 'PATCH', {'version':first['version'], 'owner':'smoke-operator', 'acknowledge':True, 'reviewAt':'2099-01-01T00:00:00Z'})
            assert updated['owner']=='smoke-operator' and updated['acknowledgedAt']
            try:
                call('/api/incidents/'+first['id'], 'PATCH', {'version':first['version'], 'owner':'stale'})
                raise AssertionError('stale version unexpectedly accepted')
            except urllib.error.HTTPError as exc:
                assert exc.code==409
            proc.terminate(); proc.wait(timeout=10)
            proc, base = start()
            restored = wait_incidents()['incidents'][0]
            assert restored['id']==first['id'] and restored['owner']=='smoke-operator'
            assert restored['reviewAt']=='2099-01-01T00:00:00Z' and restored['acknowledgedAt']
            detail=call('/api/incidents/'+first['id'])
            assert any(e['kind']=='acknowledged' for e in detail['timeline'])
            print('PASS enhanced app: collector, versioned ownership/review, optimistic conflict, restart persistence, audit history')
    finally:
        if proc and proc.poll() is None:
            proc.terminate(); proc.wait(timeout=10)
        source.shutdown(); source.server_close()

if __name__=='__main__':
    main()
