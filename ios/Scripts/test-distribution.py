"""Reject common profile errors before invoking Apple signing tools."""
import copy
import contextlib
import io
import json
import datetime
import importlib.util
from pathlib import Path
import plistlib
import tempfile
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

    def test_watch_profile_does_not_require_iphone_push_capabilities(self):
        profile = copy.deepcopy(self.profile)
        profile['Entitlements']['application-identifier'] += '.watchkitapp'
        del profile['Entitlements']['aps-environment']
        del profile['Entitlements']['com.apple.developer.usernotifications.time-sensitive']
        distribution.validate_profile(profile, 'TESTTEAM00', 'jp.example.test.watchkitapp', watch=True)
        with self.assertRaises(ValueError):
            distribution.validate_profile(self.profile, 'TESTTEAM00', 'jp.example.test.watchkitapp', watch=True)
        for key, value in [('get-task-allow', True), ('beta-reports-active', False),
                           ('aps-environment', 'development')]:
            invalid = copy.deepcopy(profile)
            invalid['Entitlements'][key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                distribution.validate_profile(invalid, 'TESTTEAM00', 'jp.example.test.watchkitapp', watch=True)
        for key, value in [('ExpirationDate', datetime.datetime(2020, 1, 1)),
                           ('ProvisionedDevices', ['device']), ('ProvisionsAllDevices', True)]:
            invalid = copy.deepcopy(profile)
            invalid[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                distribution.validate_profile(invalid, 'TESTTEAM00', 'jp.example.test.watchkitapp', watch=True)


class WatchBundleTests(unittest.TestCase):
    identifier = 'jp.example.test'
    build = '1000.1.0'
    version = '1.0.0'

    def setUp(self):
        folder = tempfile.TemporaryDirectory(prefix='quick-relay-distribution-')
        self.addCleanup(folder.cleanup)
        self.app = Path(folder.name) / 'QuakeRelay.app'
        self.watch = self.app / 'Watch' / 'QuickRelayWatch.app'
        self.extension = self.app / 'PlugIns' / 'QuickRelayLiveActivity.appex'
        self.app.mkdir()
        self.watch.mkdir(parents=True)
        self.extension.mkdir(parents=True)
        self.watch_info = {
            'CFBundleIdentifier': self.identifier + '.watchkitapp', 'CFBundleVersion': self.build,
            'CFBundleShortVersionString': self.version, 'WKApplication': True,
            'WKCompanionAppBundleIdentifier': self.identifier,
            'WKRunsIndependentlyOfCompanionApp': False, 'ITSAppUsesNonExemptEncryption': False,
        }
        self.write(self.watch / 'Info.plist', self.watch_info)
        self.write(self.app / 'Info.plist', {
            'CFBundleIdentifier': self.identifier, 'CFBundleVersion': self.build,
            'CFBundleShortVersionString': self.version, 'QuakeRelayAPNsEnvironment': 'production',
            'ITSAppUsesNonExemptEncryption': False, 'NSSupportsLiveActivities': True,
            'CFBundleIcons': {'CFBundlePrimaryIcon': {'CFBundleIconName': 'AppIcon'}},
        })
        self.write(self.extension / 'Info.plist', {
            'CFBundleIdentifier': self.identifier + '.LiveActivity', 'CFBundleVersion': self.build,
            'CFBundleShortVersionString': self.version,
            'NSExtension': {'NSExtensionPointIdentifier': 'com.apple.widgetkit-extension'},
        })
        for target in (self.app, self.watch, self.extension):
            self.write(target / 'PrivacyInfo.xcprivacy', {'NSPrivacyTracking': False})
        for sound in ('quake_warning.caf', 'quake_update.caf', 'normal.caf'):
            (self.app / sound).write_bytes(b'synthetic sound')

    @staticmethod
    def write(path, value):
        path.write_bytes(plistlib.dumps(value))

    def validate(self, entitlements=None):
        distribution.validate_watch(self.watch, self.identifier + '.watchkitapp', self.identifier,
                                    self.build, self.version, entitlements, 'TESTTEAM00')

    def test_archive_requires_embedded_watch_application(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            distribution.validate_bundle(self.app, self.identifier, self.build)
        self.assertTrue(json.loads(output.getvalue())['watch_app'])
        (self.watch / 'Info.plist').unlink()
        with self.assertRaises(FileNotFoundError):
            distribution.validate_bundle(self.app, self.identifier, self.build)

    def test_signed_archive_requires_watch_entitlement_validation(self):
        main = self.app.parent / 'main-entitlements.plist'
        extension = self.app.parent / 'extension-entitlements.plist'
        watch = self.app.parent / 'watch-entitlements.plist'
        self.write(main, {'application-identifier': 'TESTTEAM00.' + self.identifier,
                          'aps-environment': 'production', 'get-task-allow': False,
                          'com.apple.developer.usernotifications.time-sensitive': True})
        self.write(extension, {'application-identifier': 'TESTTEAM00.' + self.identifier + '.LiveActivity',
                               'get-task-allow': False})
        self.write(watch, {'application-identifier': 'TESTTEAM00.' + self.identifier + '.watchkitapp',
                           'get-task-allow': False})
        with self.assertRaises(ValueError):
            distribution.validate_bundle(self.app, self.identifier, self.build, main, 'TESTTEAM00', extension)
        with contextlib.redirect_stdout(io.StringIO()):
            distribution.validate_bundle(self.app, self.identifier, self.build, main, 'TESTTEAM00', extension, watch)

    def test_watch_identity_version_companion_and_target_flags(self):
        for key, value in [('CFBundleIdentifier', self.identifier), ('CFBundleVersion', '1'),
                           ('CFBundleShortVersionString', '0.9'), ('WKApplication', False),
                           ('WKCompanionAppBundleIdentifier', 'jp.example.other'),
                           ('WKRunsIndependentlyOfCompanionApp', True),
                           ('ITSAppUsesNonExemptEncryption', True)]:
            invalid = dict(self.watch_info, **{key: value})
            self.write(self.watch / 'Info.plist', invalid)
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.validate()
        self.write(self.watch / 'Info.plist', self.watch_info)
        self.validate()

    def test_watch_privacy_and_signed_identity(self):
        self.write(self.watch / 'PrivacyInfo.xcprivacy', {'NSPrivacyTracking': True})
        with self.assertRaises(ValueError):
            self.validate()
        self.write(self.watch / 'PrivacyInfo.xcprivacy', {'NSPrivacyTracking': False})
        path = self.app.parent / 'watch-entitlements.plist'
        entitlement = {'application-identifier': 'TESTTEAM00.' + self.identifier + '.watchkitapp',
                       'get-task-allow': False}
        self.write(path, entitlement)
        self.validate(path)
        for key, value in [('application-identifier', 'TESTTEAM00.' + self.identifier),
                           ('get-task-allow', True), ('aps-environment', 'development')]:
            self.write(path, dict(entitlement, **{key: value}))
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.validate(path)


if __name__ == '__main__':
    unittest.main()
