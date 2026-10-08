"""Opt-in macOS Simulator acceptance; fixture mutations stay on loopback.

Production access is disabled by default. An explicit owner-supplied HTTPS URL\nenables read-only health/readiness checks.
Only the evidence directory is publishable; runtime DB, pairing code and
xctestrun configuration remain private, ignored, disposable runner files.
"""
import contextlib
import argparse
import datetime as dt
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import platform
import plistlib
import re
import secrets
import socket
import sqlite3
import subprocess
import tempfile
import threading
import time
import urllib.request
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]


def command(args, *, cwd=ROOT, env=None, log=None, timeout=900):
    if log:
        print('Running: ' + args[0] + ' (log: ' + log.name + ')', flush=True)
        with log.open('w', encoding='utf-8') as output:
            result = subprocess.run(args, cwd=cwd, env=env, stdout=output,
                                    stderr=subprocess.STDOUT, timeout=timeout)
        if result.returncode:
            print('\n'.join(log.read_text(errors='replace').splitlines()[-70:]), flush=True)
            raise subprocess.CalledProcessError(result.returncode, args)
        return ''
    return subprocess.check_output(args, cwd=cwd, env=env, text=True, stderr=subprocess.STDOUT,
                                   timeout=timeout).strip()


def frames():
    at = dt.datetime.now(dt.timezone.utc) - dt.timedelta(seconds=10)
    result = []
    for index, (serial, cancelled) in enumerate([(1, False), (3, False), (2, False), (3, True)]):
        body = {'_schema': {'type': 'eew-information', 'version': '1.0.0'},
                'eventId': '20261005000000', 'serialNo': str(serial), 'status': '通常',
                'infoType': '取消' if cancelled else '発表',
                'reportDateTime': (at + dt.timedelta(seconds=index)).isoformat(),
                'title': '隔離環境の合成テスト電文',
                'body': {'isCanceled': cancelled, 'isLastInfo': cancelled}}
        result.append({'type': 'data', 'id': f'synthetic-ios-acceptance-{index}',
                       'classification': 'eew.forecast', 'head': {'type': 'VXSE45', 'test': False},
                       'format': 'json', 'encoding': 'utf-8', 'compression': None,
                       'body': json.dumps(body, ensure_ascii=False)})
    # A second event keeps an assumed forecast and warning active for numeric
    # decoding/persistence checks. It never queues a real notification.
    for index, code in enumerate(('VXSE45', 'VXSE43'), start=4):
        classification = 'eew.forecast' if code == 'VXSE45' else 'eew.warning'
        body = {'_schema': {'type': 'eew-information', 'version': '1.0.0'},
                'eventId': '20261005000001', 'serialNo': '1', 'status': '通常', 'infoType': '発表',
                'reportDateTime': (at + dt.timedelta(seconds=index)).isoformat(),
                'title': '隔離環境の仮定震源テスト',
                'body': {'isWarning': True, 'earthquake': {'condition': '仮定震源要素',
                    'originTime': at.isoformat(), 'magnitude': {'value': '1.0'},
                    'hypocenter': {'name': '合成震源', 'depth': {'value': '10'},
                        'coordinate': {'latitude': {'value': '31.8'}, 'longitude': {'value': '131.7'}}}},
                    'intensity': {'forecastMaxInt': {'from': '5-', 'to': 'over'}},
                    'prefectures': [{'name': '合成対象地域'}]}}
        result.append({'type': 'data', 'id': f'synthetic-ios-acceptance-{index}',
                       'classification': classification, 'head': {'type': code, 'test': False},
                       'format': 'json', 'encoding': 'utf-8', 'compression': None,
                       'body': json.dumps(body, ensure_ascii=False)})

    extras = [
        ('VXSE53', 'earthquake-information', '20261005000002', '震源・震度情報',
         {'earthquake': {'originTime': at.isoformat(), 'hypocenter': {'name': '合成震源・通常情報'},
                         'magnitude': {'value': '5.0'}}, 'intensity': {'maxInt': '4',
             'prefectures': [{'name': '合成県', 'maxInt': '4', 'regions': [
                 {'name': '合成地方', 'maxInt': '4', 'cities': [
                     {'name': '合成市', 'maxInt': '4', 'stations': [
                         {'name': f'合成観測地点{i:02d}', 'int': '4' if i == 1 else '3'}
                         for i in range(1, 21)]}]}]}]}}),
        ('VTSE41', 'tsunami-information', '20261005000002 20261005000003', '合成津波警報',
         {'tsunami': {'forecasts': [{'code': '999', 'name': '合成予報区',
             'kind': {'code': '53', 'name': '大津波警報', 'lastKind': {'code': '00', 'name': 'なし'}},
             'firstHeight': {'condition': '津波到達中と推測'},
             'maxHeight': {'height': {'type': '津波の高さ', 'unit': 'm', 'value': None, 'condition': '巨大'}}}]}}),
        ('VYSE50', 'earthquake-nankai', None, '合成南海トラフ情報',
         {'earthquakeInfo': {'kind': {'code': 'synthetic', 'name': '南海トラフ地震臨時情報（調査中）'},
              'text': 'これは合成資料です。実際の発表ではありません。', 'appendix': '合成補足'}}),
    ]
    for index, (code, schema, identity, title, content) in enumerate(extras, start=6):
        body = {'_schema': {'type': schema, 'version': '1.0.0'}, 'eventId': identity,
                'serialNo': '9' if code == 'VXSE53' else None, 'status': '通常', 'infoType': '発表',
                'reportDateTime': (at + dt.timedelta(seconds=index)).isoformat(),
                'title': title, 'body': content}
        result.append({'type': 'data', 'id': f'synthetic-ios-acceptance-{index}',
                       'classification': 'telegram.earthquake', 'head': {'type': code, 'test': False},
                       'format': 'json', 'encoding': 'utf-8', 'compression': None,
                       'body': json.dumps(body, ensure_ascii=False)})
    result.append({'type': 'data', 'id': 'synthetic-ios-acceptance-9',
                   'classification': 'telegram.earthquake',
                   'head': {'type': 'WEPA60', 'test': False, 'author': 'synthetic', 'time': at.isoformat()},
                   'format': 'a/n', 'encoding': 'utf-8', 'compression': None,
                   'body': 'SYNTHETIC INTERNATIONAL TSUNAMI TEXT. NOT AN ACTUAL ALERT.'})
    return result


def configure_tests(value, environment):
    count = 0
    if isinstance(value, dict):
        if 'TestBundlePath' in value:
            value.setdefault('EnvironmentVariables', {}).update(environment)
            count += 1
        for child in value.values():
            count += configure_tests(child, environment)
    elif isinstance(value, list):
        for child in value:
            count += configure_tests(child, environment)
    return count



@contextlib.contextmanager
def notification_control(simulator, evidence):
    # Loopback-only, fixed actions and paths: the UI runner cannot execute arbitrary commands.
    states = ('foreground', 'background', 'terminated')
    injections = []
    captures = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            if self.path in ['/push/' + state for state in states]:
                state = self.path.rsplit('/', 1)[1]
                payload = {'aps': {'alert': {'title': 'W8 ' + state,
                                           'body': 'シミュレーターの表示確認。地震情報ではありません。'},
                                   'sound': 'default'}, 'kind': 'system_test'}
                path = evidence / ('simulated-' + state + '.json')
                path.write_text(json.dumps(payload, ensure_ascii=False), encoding='utf-8')
                command(['xcrun', 'simctl', 'push', simulator, 'jp.kb-dev.quickrelay', str(path)], timeout=30)
                injections.append(state)
            elif self.path in ['/capture/' + state for state in ('launched', 'earthquake-list', 'tsunami-list', 'advisory-list', 'intensity-popup', 'intensity-dismissed', 'tsunami', 'advisory', 'ordinary', 'notification-settings', 'notification-test') + states]:
                state = self.path.rsplit('/', 1)[1]
                try:
                    size = int(self.headers.get('Content-Length', '0'))
                except ValueError:
                    self.send_error(400)
                    return
                if not 8 <= size <= 16 * 1024 * 1024:
                    self.send_error(400)
                    return
                self.connection.settimeout(10)
                data = self.rfile.read(size)
                if len(data) != size or not data.startswith(b'\x89PNG\r\n\x1a\n'):
                    self.send_error(400)
                    return
                (evidence / ('simulator-' + state + '.png')).write_bytes(data)
                captures.append(state)
            else:
                self.send_error(404)
                return
            self.send_response(204)
            self.end_headers()

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f'http://127.0.0.1:{server.server_port}', injections, captures
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


def source_evidence(mode, baseline=None):
    trees = {name: command(['git', 'rev-parse', 'HEAD:' + path])
             for name, path in (('server', 'server'), ('ios', 'ios'))}
    matches = None
    if baseline is not None:
        if set(baseline) != {'server', 'ios'} or not all(
                isinstance(value, str) and re.fullmatch(r'[0-9a-f]{40}', value)
                for value in baseline.values()):
            raise SystemExit('Deployment baseline must contain server and ios Git tree IDs.')
        matches = {name: trees[name] == baseline[name] for name in trees}
    if mode == 'deployed' and (matches is None or not all(matches.values())):
        raise SystemExit('Deployed mode requires an explicit baseline matching both source trees.')
    return {'source_mode': mode, 'source_tree_ids': trees, 'source_tree_paths': {'server': 'server', 'ios': 'ios'},
            'matches_recorded_deployed_sources': matches}


def live_endpoint(value):
    if not value:
        return ''
    parsed = urllib.parse.urlsplit(value)
    if (parsed.scheme != 'https' or not parsed.hostname or parsed.username or parsed.password
            or parsed.path != '/api/v1' or parsed.query or parsed.fragment):
        raise SystemExit('Explicit live URL must be an HTTPS /api/v1 endpoint without credentials.')
    return value.rstrip('/')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-mode', choices=('deployed', 'candidate'), default='candidate')
    parser.add_argument('--deployment-baseline', type=Path)
    parser.add_argument('--live-url', default='')
    args = parser.parse_args()
    if platform.system() != 'Darwin':
        raise SystemExit('Run this acceptance on macOS with Xcode (or the GitHub workflow).')
    baseline = json.loads(args.deployment_baseline.read_text(encoding='utf-8')) if args.deployment_baseline else None
    sources = source_evidence(args.source_mode, baseline)
    live = live_endpoint(args.live_url)
    work = ROOT / '.local/ios-acceptance'
    work.mkdir(parents=True, exist_ok=False)
    evidence = work / 'evidence'
    evidence.mkdir()
    sdk = tuple(map(int, command(['xcrun', '--sdk', 'iphonesimulator', '--show-sdk-version']).split('.')[:2]))
    devices = json.loads(command(['xcrun', 'simctl', 'list', 'devices', 'available', '--json']))['devices']
    candidates = []
    for runtime, items in devices.items():
        match = re.search(r'\.iOS-(\d+)-(\d+)', runtime)
        if match and (17, 0) <= tuple(map(int, match.groups())) <= sdk:
            for item in items:
                if item.get('isAvailable') and item['name'].startswith('iPhone'):
                    candidates.append((tuple(map(int, match.groups())), item['udid'], item))
    if not candidates:
        raise SystemExit('No compatible iPhone Simulator')
    runtime, simulator, device = max(candidates)
    if device['state'] != 'Booted':
        command(['xcrun', 'simctl', 'boot', simulator])
    command(['xcrun', 'simctl', 'bootstatus', simulator, '-b'])
    print('Simulator ready: ' + device['name'], flush=True)
    destination = f'platform=iOS Simulator,id={simulator},arch={platform.machine()}'
    derived = work / 'DerivedData'
    command(['xcodebuild', '-project', 'QuakeRelay.xcodeproj', '-scheme', 'QuakeRelay',
             '-destination', destination, '-derivedDataPath', str(derived),
             'CODE_SIGNING_ALLOWED=YES', 'CODE_SIGN_IDENTITY=-', 'CODE_SIGN_STYLE=Manual',
             'DEVELOPMENT_TEAM=', 'build-for-testing'], cwd=ROOT / 'ios',
            log=evidence / 'build.log', timeout=1200)
    binary = work / 'quakerelay'
    build_env = dict(os.environ, CGO_ENABLED='0')
    command(['go', 'build', '-trimpath', '-o', str(binary), './cmd/quakerelay'],
            cwd=ROOT / 'server', env=build_env, log=evidence / 'go-build.log')
    summary = {'tested_commit': command(['git', 'rev-parse', 'HEAD']),
               **sources,
               'xcode': command(['xcodebuild', '-version']),
               'go': command(['go', 'version']), 'architecture': platform.machine(),
               'simulator': device['name'], 'ios_runtime': '.'.join(map(str, runtime)),
               'physical_device': False, 'apns_acceptance': 'not_tested',
               'production_mutations': False}
    with tempfile.TemporaryDirectory(prefix='fixture-', dir=work) as temp:
        scratch = Path(temp)
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        env = dict(os.environ, RELAY_MODE='offline', LISTEN_ADDR=f'127.0.0.1:{port}',
                   DATABASE_PATH=str(scratch / 'fixture.db'), PAIRING_SECRET=secrets.token_hex(32))
        command([str(binary), 'check'], env=env)
        source = scratch / 'frames.jsonl'
        source.write_text('\n'.join(json.dumps(f, ensure_ascii=False) for f in frames()), encoding='utf-8')
        command([str(binary), 'replay', str(source)], env=env)
        code = re.search(r'\b[0-9]{8}\b', command([str(binary), 'pair'], env=env)).group()
        # This ephemeral local-only credential must not appear in CI output.
        with (scratch / 'relay.log').open('wb') as output:
            relay = subprocess.Popen([str(binary), 'serve'], env=env, stdout=output, stderr=subprocess.STDOUT)
        try:
            for _ in range(100):
                if relay.poll() is not None:
                    raise RuntimeError('Fixture relay exited')
                try:
                    with urllib.request.urlopen(f'http://127.0.0.1:{port}/health', timeout=2) as response:
                        assert json.load(response)['ok']
                    break
                except OSError:
                    time.sleep(0.1)
            else:
                raise RuntimeError('Fixture relay did not become healthy')
            files = list((derived / 'Build/Products').glob('*.xctestrun'))
            assert len(files) == 1, 'Expected one generated XCTest run configuration'
            config = plistlib.loads(files[0].read_bytes())
            assert configure_tests(config, {
                'QUAKERELAY_ACCEPTANCE_BASE_URL': f'http://localhost:{port}/api/v1',
                'QUAKERELAY_ACCEPTANCE_PAIRING_CODE': code,
                'QUAKERELAY_ACCEPTANCE_LIVE_URL': live,
            }) > 0
            test_run = files[0].with_name('acceptance.xctestrun')
            test_run.write_bytes(plistlib.dumps(config))
            try:
                command(['xcodebuild', 'test-without-building', '-xctestrun', str(test_run),
                         '-destination', destination, '-parallel-testing-enabled', 'NO',
                         '-only-testing:QuakeRelayTests/ServerAcceptanceTests',
                         '-resultBundlePath', str(work / 'Acceptance.xcresult')],
                        log=evidence / 'acceptance.log', timeout=600)
            finally:
                test_run.unlink()
            with contextlib.closing(sqlite3.connect(scratch / 'fixture.db')) as db:
                assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
                reports = db.execute('SELECT COUNT(*) FROM reports').fetchone()[0]
                deliveries = db.execute('SELECT COUNT(*) FROM deliveries').fetchone()[0]
                revoked = db.execute('SELECT COUNT(*) FROM devices WHERE revoked=1').fetchone()[0]
                assert reports == 10 and deliveries == 0 and revoked == 1
                summary['fixture'] = {'reports': reports, 'deliveries': deliveries, 'revoked_devices': revoked,
                                      'integrity_check': 'ok'}
            summary['swift_go_acceptance'] = 'passed'
            summary['simulator_to_public_vps'] = 'passed_read_only' if live else 'not_tested'
            ui_code = re.search(r'\b[0-9]{8}\b', command([str(binary), 'pair'], env=env)).group()
            # UI tests grant permission, assert visible notifications, and tap each banner.
            command(['xcrun', 'simctl', 'ui', simulator, 'appearance', 'dark'], timeout=30)
            with notification_control(simulator, evidence) as (control, injections, captures):
                config = plistlib.loads(files[0].read_bytes())
                assert configure_tests(config, {'QUAKERELAY_ACCEPTANCE_CONTROL_URL': control,
                    'QUAKERELAY_UI_BASE_URL': f'http://localhost:{port}/api/v1',
                    'QUAKERELAY_UI_PAIRING_CODE': ui_code}) > 0
                test_run = files[0].with_name('notification-acceptance.xctestrun')
                test_run.write_bytes(plistlib.dumps(config))
                try:
                    command(['xcodebuild', 'test-without-building', '-xctestrun', str(test_run),
                             '-destination', destination, '-parallel-testing-enabled', 'NO',
                             '-only-testing:QuakeRelayUITests',
                             '-resultBundlePath', str(work / 'Notifications.xcresult')],
                            log=evidence / 'notifications.log', timeout=600)
                finally:
                    test_run.unlink()
                assert injections == ['foreground', 'background', 'terminated']
                assert captures == ['launched', 'foreground', 'background', 'terminated',
                    'earthquake-list', 'ordinary', 'intensity-popup', 'intensity-dismissed',
                    'tsunami-list', 'tsunami', 'advisory-list', 'advisory',
                    'notification-settings', 'notification-test']
        finally:
            if relay.poll() is None:
                relay.terminate()
                try:
                    relay.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    relay.kill()
                    relay.wait(timeout=5)
    summary['simulated_notification_display_and_tap'] = 'passed_foreground_background_terminated'
    summary['notification_transport'] = 'local_simctl_injection_not_apns'
    summary['completed_at_utc'] = dt.datetime.now(dt.timezone.utc).isoformat()
    (evidence / 'summary.json').write_text(json.dumps(summary, indent=2, ensure_ascii=False), encoding='utf-8')
    print(json.dumps(summary, ensure_ascii=False), flush=True)


if __name__ == '__main__':
    main()
