# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
import http.server
import json
import ssl
import subprocess
import tempfile
import threading
import unittest
from pathlib import Path
from test_fencing import f, UUID, ROOT


class RedfishTLSTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        cls.cert = cls.root / 'cert.pem'
        key = cls.root / 'key.pem'
        conf = cls.root / 'openssl.cnf'
        conf.write_text('[req]\ndistinguished_name=dn\nx509_extensions=ext\nprompt=no\n'
                        '[dn]\nCN=localhost\n[ext]\nsubjectAltName=DNS:localhost\n'
                        'basicConstraints=critical,CA:TRUE\n')
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
                        '-days', '1', '-config', str(conf), '-out', str(cls.cert),
                        '-keyout', str(key)], check=True, capture_output=True)
        cls.password = cls.root / 'password'
        cls.password.write_text('test-password')
        cls.password.chmod(0o600)
        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                self.server.gets += 1
                if self.server.mode == 'unauthorized':
                    self.send_error(401)
                    return
                if self.server.mode == 'redirect':
                    self.send_response(302)
                    self.send_header('Location', 'https://localhost:1/stolen')
                    self.end_headers()
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(json.dumps({
                    'UUID': UUID, 'SerialNumber': 'SERIAL', 'PowerState': self.server.power,
                    'Actions': {'#ComputerSystem.Reset': {
                        'target': '/redfish/v1/Systems/1/Actions/ComputerSystem.Reset',
                        'ResetType@Redfish.AllowableValues': ['ForceOff']}}}).encode())

            def do_POST(self):
                self.server.posts += 1
                data = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                if data != {'ResetType': 'ForceOff'}:
                    self.send_error(400)
                    return
                self.server.power = 'Off'
                self.send_response(202)
                self.end_headers()
        cls.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cls.cert, key)
        cls.server.socket = context.wrap_socket(cls.server.socket, server_side=True)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()
        cls.temp.cleanup()

    def setUp(self):
        self.server.mode = 'normal'
        self.server.power = 'On'
        self.server.gets = self.server.posts = 0
        self.target = {'provider': 'redfish', 'url': 'https://localhost:' + str(self.server.server_port),
                       'username': 'fence', 'password_file': str(self.password),
                       'ca_file': str(self.cert), 'system_uri': '/redfish/v1/Systems/1',
                       'uuid': UUID, 'serial': 'SERIAL'}

    def test_verified_tls_and_actual_http_round_trip(self):
        f.fence(self.target, 'b', f.Runner())
        self.assertEqual(self.server.posts, 1)
        self.assertGreaterEqual(self.server.gets, 2)

    def test_cli_from_protected_config(self):
        config = self.root / 'fencing.json'
        config.write_text(json.dumps({'local_node': 'a', 'targets': {
            'b': {**self.target, 'restart_disabled': True}}}))
        config.chmod(0o600)
        result = subprocess.run([str(ROOT / 'scripts/godhcp-fence-peer'), '--config', str(config), 'b'],
                                capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, 'FENCED b\n')
        self.assertEqual(self.server.posts, 1)
        # Reject an accidental field typo before performing any power operation.
        config.write_text(json.dumps({'local_node': 'a', 'targets': {
            'b': {**self.target, 'restart_disabled': True, 'insecure': True}}}))
        result = subprocess.run([str(ROOT / 'scripts/godhcp-fence-peer'), '--config', str(config), 'b'],
                                capture_output=True, text=True, timeout=20)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.server.posts, 1)

    def test_wrong_hostname_rejected_before_poweroff(self):
        self.target['url'] = self.target['url'].replace('localhost', '127.0.0.1')
        with self.assertRaises(Exception):
            f.fence(self.target, 'b', f.Runner())
        self.assertEqual(self.server.posts, 0)

    def test_untrusted_ca_rejected(self):
        self.target['ca_file'] = None  # use system trust, which excludes test certificate
        with self.assertRaises(Exception):
            f.fence(self.target, 'b', f.Runner())
        self.assertEqual(self.server.posts, 0)

    def test_redirect_and_auth_error_fail_closed(self):
        for mode in ('redirect', 'unauthorized'):
            self.server.mode = mode
            with self.subTest(mode=mode), self.assertRaises(Exception):
                f.fence(self.target, 'b', f.Runner())
        self.assertEqual(self.server.posts, 0)
