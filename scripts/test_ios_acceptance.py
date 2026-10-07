"""Exercise the loopback screenshot receiver without Xcode or a live service."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
import urllib.error
import urllib.request
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    'ios_acceptance', Path(__file__).with_name('verify-ios-acceptance.py'))
acceptance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(acceptance)


class ScreenshotReceiverTests(unittest.TestCase):
    def test_saves_xctest_png_without_spawning_capture_process(self):
        data = b'\x89PNG\r\n\x1a\nfixture'
        with tempfile.TemporaryDirectory() as directory, patch.object(acceptance, 'command') as command:
            evidence = Path(directory)
            with acceptance.notification_control('unused', evidence) as (base, injections, captures):
                for state in ('launched', 'foreground', 'background', 'terminated'):
                    request = urllib.request.Request(base + '/capture/' + state, data=data,
                                                     headers={'Content-Type': 'image/png'})
                    with urllib.request.urlopen(request, timeout=5) as response:
                        self.assertEqual(response.status, 204)
                    self.assertEqual((evidence / ('simulator-' + state + '.png')).read_bytes(), data)
                self.assertEqual(captures, ['launched', 'foreground', 'background', 'terminated'])
                self.assertEqual(injections, [])
                command.assert_not_called()

    def test_rejects_unexpected_paths_invalid_png_and_oversized_payload(self):
        with tempfile.TemporaryDirectory() as directory:
            evidence = Path(directory)
            with acceptance.notification_control('unused', evidence) as (base, _, captures):
                cases = [('/capture/../outside', b'\x89PNG\r\n\x1a\n', {}, 404),
                         ('/capture/foreground', b'not-a-png', {}, 400),
                         ('/capture/foreground', b'', {}, 400),
                         ('/capture/foreground', b'', {'Content-Length': str(16 * 1024 * 1024 + 1)}, 400)]
                for path, data, headers, status in cases:
                    with self.subTest(path=path, data=data, headers=headers):
                        request = urllib.request.Request(base + path, data=data, headers=headers)
                        with self.assertRaises(urllib.error.HTTPError) as failure:
                            urllib.request.urlopen(request, timeout=5)
                        self.assertEqual(failure.exception.code, status)
                self.assertEqual(captures, [])
                self.assertEqual(list(evidence.iterdir()), [])


class SourceEvidenceTests(unittest.TestCase):
    def test_candidate_without_baseline_never_claims_a_deployment_match(self):
        with patch.object(acceptance, 'command', side_effect=['a' * 40, 'b' * 40]):
            result = acceptance.source_evidence('candidate')
        self.assertIsNone(result['matches_recorded_deployed_sources'])
        self.assertEqual(result['source_tree_ids'], {'server': 'a' * 40, 'ios': 'b' * 40})

    def test_deployed_requires_explicit_matching_baseline(self):
        for baseline in (None, {'server': 'a' * 40, 'ios': 'c' * 40},
                         {'server': 'invalid', 'ios': 'b' * 40}):
            with self.subTest(baseline=baseline):
                with patch.object(acceptance, 'command', side_effect=['a' * 40, 'b' * 40]):
                    with self.assertRaises(SystemExit):
                        acceptance.source_evidence('deployed', baseline)

    def test_deployed_records_both_exact_matches(self):
        baseline = {'server': 'a' * 40, 'ios': 'b' * 40}
        with patch.object(acceptance, 'command', side_effect=['a' * 40, 'b' * 40]):
            result = acceptance.source_evidence('deployed', baseline)
        self.assertEqual(result['matches_recorded_deployed_sources'], {'server': True, 'ios': True})


class LiveEndpointTests(unittest.TestCase):
    def test_live_access_is_opt_in_and_https_only(self):
        self.assertEqual(acceptance.live_endpoint(''), '')
        self.assertEqual(acceptance.live_endpoint('https://relay.example.com/api/v1'),
                         'https://relay.example.com/api/v1')
        for value in ('http://relay.example.com/api/v1', 'https://relay.example.com/',
                      'https://user:password@relay.example.com/api/v1',
                      'https://relay.example.com/api/v1?token=example', 'https:///api/v1'):
            with self.subTest(value=value), self.assertRaises(SystemExit):
                acceptance.live_endpoint(value)


if __name__ == '__main__':
    unittest.main()
