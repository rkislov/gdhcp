# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
import importlib.machinery
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
loader = importlib.machinery.SourceFileLoader('fencing', str(ROOT / 'scripts/godhcp-fence-peer'))
spec = importlib.util.spec_from_loader(loader.name, loader)
f = importlib.util.module_from_spec(spec)
loader.exec_module(f)
UUID = '11111111-1111-1111-1111-111111111111'


class FakeRunner(f.Runner):
    def __init__(self, results):
        super().__init__(1)
        self.results = list(results)
        self.calls = []

    def run(self, args):
        self.calls.append(args)
        if not self.results:
            raise AssertionError('unexpected provider call: ' + str(args))
        item = self.results.pop(0)
        if isinstance(item, Exception):
            raise item
        return item


class FencingTests(unittest.TestCase):
    def test_config_rejects_self_unknown_injection_and_restart(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'config.json'
            path.write_text(json.dumps({'local_node': 'a', 'targets': {
                'a': {'restart_disabled': True}, 'b': {'restart_disabled': False}}}))
            for target in ('a', 'c', 'b', 'b;reboot', 'b\n', '-oProxyCommand=x'):
                with self.subTest(target=target), self.assertRaises(f.FenceError):
                    f.load_target(str(path), target)
            path.chmod(0o666)
            with self.assertRaises(f.FenceError):
                f.load_target(str(path), 'b')

    def test_duplicate_json_rejected(self):
        with self.assertRaises(f.FenceError):
            json.loads('{"a":1,"a":2}', object_pairs_hook=f.unique_object)

    def test_proxmox_poweroff_and_fresh_verification(self):
        runner = FakeRunner(['[]', 'smbios1: uuid=' + UUID, 'status: running', '', 'status: stopped'])
        f.fence({'provider': 'proxmox', 'vmid': 201, 'uuid': UUID}, 'b', runner)
        self.assertIn(['/usr/sbin/qm', 'stop', '201', '--timeout', '10'], runner.calls)
        self.assertFalse(runner.results)

    def test_proxmox_already_off_does_not_stop(self):
        runner = FakeRunner(['[]', 'smbios1: uuid=' + UUID, 'status: stopped', 'status: stopped'])
        f.fence({'provider': 'proxmox', 'vmid': 201, 'uuid': UUID}, 'b', runner)
        self.assertFalse(any('stop' in c for c in runner.calls))

    def test_proxmox_rejects_ha_lock_autostart_and_wrong_uuid(self):
        for results in ([json.dumps([{'sid': 'vm:201'}])],
                        ['[]', 'lock: migrate\nsmbios1: uuid=' + UUID],
                        ['[]', 'onboot: 1\nsmbios1: uuid=' + UUID],
                        ['[]', 'smbios1: uuid=wrong']):
            runner = FakeRunner(results)
            with self.subTest(results=results), self.assertRaises(f.FenceError):
                f.fence({'provider': 'proxmox', 'vmid': 201, 'uuid': UUID}, 'b', runner)
            self.assertFalse(any('stop' in c for c in runner.calls))

    def test_kvm_destroy_and_already_off(self):
        for initial in ('running', 'shut off'):
            outputs = ['<domain><uuid>' + UUID + '</uuid></domain>', 'Autostart: disable', initial]
            if initial == 'running':
                outputs.append('')
            outputs.append('shut off')
            runner = FakeRunner(outputs)
            f.fence({'provider': 'kvm', 'uuid': UUID}, 'b', runner)
            self.assertEqual(any('destroy' in c for c in runner.calls), initial == 'running')
            self.assertFalse(any('undefine' in c for c in runner.calls))

    def test_kvm_autostart_blocks_destroy(self):
        runner = FakeRunner(['<domain><uuid>' + UUID + '</uuid></domain>', 'Autostart: enable'])
        with self.assertRaises(f.FenceError):
            f.fence({'provider': 'kvm', 'uuid': UUID}, 'b', runner)

    @patch.object(f, 'secret', return_value='DO-NOT-LOG')
    def test_ipmi_guid_and_status(self, _):
        target = {'provider': 'ipmi', 'host': 'bmc', 'username': 'user', 'guid': UUID,
                  'password_file': '/private/password'}
        for initial in ('on', 'off'):
            outputs = ['System GUID : ' + UUID, 'Chassis Power is ' + initial]
            if initial == 'on':
                outputs.append('Chassis Power Control: Down/Off')
            outputs.append('Chassis Power is off')
            runner = FakeRunner(outputs)
            f.fence(target, 'b', runner)
            self.assertNotIn('DO-NOT-LOG', repr(runner.calls))
            self.assertEqual(sum(c[-1] == 'off' for c in runner.calls), initial == 'on')
        with self.assertRaises(f.FenceError):
            f.fence(target, 'b', FakeRunner(['System GUID : wrong']))

    @patch.object(f, 'protected', return_value='')
    def test_ssh_requires_exact_ack_and_enforces_host_key(self, _):
        target = {'provider': 'ssh', 'host': 'gateway.example', 'user': 'fence',
                  'key_file': '/key', 'known_hosts': '/known'}
        runner = FakeRunner(['FENCED b'])
        f.fence(target, 'b', runner)
        self.assertIn('StrictHostKeyChecking=yes', runner.calls[0])
        self.assertEqual(runner.calls[0][-1], 'b')
        for output in ('', 'OK', 'FENCED a', 'FENCED b\nextra'):
            with self.subTest(output=output), self.assertRaises(f.FenceError):
                f.fence(target, 'b', FakeRunner([output]))

    def test_runner_failed_command_and_timeout(self):
        with self.assertRaises(f.FenceError):
            f.Runner().run(['/bin/sh', '-c', 'echo SECRET >&2; exit 1'])
        with self.assertRaises(f.FenceError):
            f.Runner(0.05).run(['/bin/sleep', '2'])
        runner = f.Runner(0.01)
        with self.assertRaises(f.FenceError):
            runner.wait_off(lambda: 'running', 'off')

    def test_cli_fails_without_leaking_invalid_config(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'config.json'
            path.write_text('TOP-SECRET-invalid-json')
            p = subprocess.run([str(ROOT / 'scripts/godhcp-fence-peer'), '--config', str(path), 'b'],
                               capture_output=True, text=True)
            self.assertNotEqual(p.returncode, 0)
            self.assertNotIn('TOP-SECRET', p.stdout + p.stderr)
            self.assertNotIn('FENCED', p.stdout)


class RedfishTests(unittest.TestCase):
    def adapter(self, responses):
        obj = object.__new__(f.Redfish)
        obj.t = {'system_uri': '/redfish/v1/Systems/1', 'uuid': UUID, 'serial': 'SERIAL'}
        obj.runner = f.Runner(0.5)
        self.calls = []
        def request(path, body=None):
            self.calls.append((path, body))
            value = responses.pop(0)
            if isinstance(value, Exception):
                raise value
            return value
        obj.request = request
        return obj

    def system(self, state='On'):
        return {'UUID': UUID, 'SerialNumber': 'SERIAL', 'PowerState': state,
                'Actions': {'#ComputerSystem.Reset': {
                    'target': '/redfish/v1/Systems/1/Actions/ComputerSystem.Reset',
                    'ResetType@Redfish.AllowableValues': ['ForceOff']}}}

    def test_forceoff_ack_is_not_completion(self):
        obj = self.adapter([self.system(), {}, self.system('PoweringOff'), self.system('Off')])
        obj.fence()
        self.assertEqual(self.calls[1][1], {'ResetType': 'ForceOff'})
        self.assertEqual(len(self.calls), 4)

    def test_already_off_has_no_post(self):
        self.adapter([self.system('Off'), self.system('Off')]).fence()
        self.assertTrue(all(body is None for _, body in self.calls))

    def test_wrong_identity_blocks_power_action(self):
        for field in ('UUID', 'SerialNumber'):
            data = self.system()
            data[field] = 'wrong'
            with self.subTest(field=field), self.assertRaises(f.FenceError):
                self.adapter([data]).fence()
            self.assertEqual(len(self.calls), 1)

    def test_forceoff_not_supported(self):
        data = self.system()
        data['Actions']['#ComputerSystem.Reset']['ResetType@Redfish.AllowableValues'] = ['ForceRestart']
        with self.assertRaises(f.FenceError):
            self.adapter([data]).fence()

    def test_actioninfo_discovery(self):
        data = self.system()
        action = data['Actions']['#ComputerSystem.Reset']
        del action['ResetType@Redfish.AllowableValues']
        action['@Redfish.ActionInfo'] = '/redfish/v1/ResetActionInfo'
        info = {'Parameters': [{'Name': 'ResetType', 'AllowableValues': ['ForceOff']}]}
        self.adapter([data, info, {}, self.system('Off')]).fence()
        self.assertEqual(self.calls[1][0], '/redfish/v1/ResetActionInfo')

    def test_error_after_ack_is_failure(self):
        with self.assertRaises(f.FenceError):
            self.adapter([self.system(), {}, f.FenceError('unreachable')]).fence()

    def test_cross_origin_action_and_redirect_rejected(self):
        obj = object.__new__(f.Redfish)
        for path in ('https://evil/redfish/v1/Reset', '//evil/redfish/v1/Reset',
                     '/redfish/v1/../evil', '/redfish/v1/Reset?token=x'):
            with self.subTest(path=path), self.assertRaises(f.FenceError):
                obj.request(path, {})
        with self.assertRaises(f.FenceError):
            f.NoRedirect().redirect_request(None, None, None, None, None, None)


if __name__ == '__main__':
    unittest.main()
