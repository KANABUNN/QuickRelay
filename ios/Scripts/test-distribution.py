"""Reject common profile errors before invoking Apple signing tools."""
import copy
import datetime
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('distribution', Path(__file__).with_name('validate-distribution.py'))
distribution = importlib.util.module_from_spec(spec)
spec.loader.exec_module(distribution)


class ProfileTests(unittest.TestCase):
    def setUp(self):
        self.profile = {
            'TeamIdentifier': ['TESTTEAM00'], 'DeveloperCertificates': [b'test certificate'],
            'ExpirationDate': datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=1),
            'Entitlements': {
                'application-identifier': 'TESTTEAM00.jp.example.test',
                'aps-environment': 'production', 'get-task-allow': False,
                'beta-reports-active': True,
                'com.apple.developer.usernotifications.time-sensitive': True,
            },
        }

    def test_distribution_profile(self):
        distribution.validate_profile(self.profile, 'TESTTEAM00', 'jp.example.test')

    def test_wrong_app_team_or_environment(self):
        for key, value in [('application-identifier', 'TESTTEAM00.jp.example.other'),
                           ('aps-environment', 'development'), ('get-task-allow', True),
                           ('beta-reports-active', False),
                           ('com.apple.developer.usernotifications.time-sensitive', False)]:
            profile = copy.deepcopy(self.profile)
            profile['Entitlements'][key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                distribution.validate_profile(profile, 'TESTTEAM00', 'jp.example.test')
        with self.assertRaises(ValueError):
            distribution.validate_profile(self.profile, 'OTHERTEAM0', 'jp.example.test')

    def test_extension_profile_requires_its_own_exact_app_id(self):
        profile = copy.deepcopy(self.profile)
        profile['Entitlements']['application-identifier'] += '.LiveActivity'
        del profile['Entitlements']['aps-environment']
        del profile['Entitlements']['com.apple.developer.usernotifications.time-sensitive']
        distribution.validate_profile(profile, 'TESTTEAM00', 'jp.example.test.LiveActivity', extension=True)
        with self.assertRaises(ValueError):
            distribution.validate_profile(self.profile, 'TESTTEAM00', 'jp.example.test.LiveActivity', extension=True)

    def test_expired_or_device_limited_profile(self):
        for key, value in [('ExpirationDate', datetime.datetime(2020, 1, 1)),
                           ('ProvisionedDevices', ['device']), ('ProvisionsAllDevices', True)]:
            profile = copy.deepcopy(self.profile)
            profile[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                distribution.validate_profile(profile, 'TESTTEAM00', 'jp.example.test')


if __name__ == '__main__':
    unittest.main()
