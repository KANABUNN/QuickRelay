"""Check relocation boundaries and shared artifacts without external dependencies."""
import json
from pathlib import Path
import plistlib
import re
import subprocess
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


# Git excludes runtime/cache trees without traversing thousands of generated files.
listed = subprocess.check_output(['git', 'ls-files', '-co', '--exclude-standard', '-z'], cwd=ROOT)
files = [ROOT / name for name in set(listed.decode().rstrip('\0').split('\0')) if name and (ROOT / name).is_file()]

for path in files:
    if path.suffix == '.json':
        json.loads(path.read_text(encoding='utf-8'))
    if path.suffix in {'.plist', '.entitlements'}:
        with path.open('rb') as stream:
            plistlib.load(stream)
    if path.suffix == '.md':
        for target in re.findall(r'\[[^\]]*\]\(([^\s)]+)\)', path.read_text(encoding='utf-8')):
            url = urlsplit(target)
            if url.scheme or not url.path:
                continue
            require((path.parent / unquote(url.path)).exists(), f'Broken link: {path.relative_to(ROOT)} -> {target}')

for obsolete in ['windows', 'pwa', 'vps', 'server/composer.json', 'README.windows-legacy.md',
                 'contracts/event-envelope.schema.json', 'contracts/talker-ipc.schema.json', 'contracts/hmac-test-vectors.json']:
    require(not (ROOT / obsolete).exists(), f'Obsolete artifact remains: {obsolete}')
for path in files:
    require(path.suffix.lower() not in {'.php', '.cs', '.csproj', '.sln', '.appxmanifest'}, f'Legacy source remains: {path}')
    if path.suffix == '.go' or path.name == 'go.mod':
        require('quakerelay/vps' not in path.read_text(encoding='utf-8'), f'Old Go module path: {path}')

# Compare the documented route set to registered Go routes; no HTTP call is made.
routes = set()
for path in (ROOT / 'server/internal/api').glob('*.go'):
    if not path.name.endswith('_test.go'):
        routes.update((method.lower(), route) for method, route in re.findall(r'HandleFunc\("(GET|POST|PUT|PATCH|DELETE) ([^"]+)"', path.read_text(encoding='utf-8')))
openapi = json.loads((ROOT / 'contracts/openapi.json').read_text(encoding='utf-8'))
documented = {(method, route) for route, methods in openapi['paths'].items() for method in methods if method in {'get', 'post', 'put', 'patch', 'delete'}}
require(routes == documented, f'API route documentation drift: {routes ^ documented}')

# All schema references must resolve locally, without accessing schema URLs.
def check_refs(value, document, path):
    if isinstance(value, dict):
        if '$ref' in value:
            link = urlsplit(value['$ref'])
            require(not link.scheme, f'Nonlocal contract reference: {value["$ref"]}')
            target = document
            if link.path:
                target = json.loads((path.parent / link.path).read_text(encoding='utf-8'))
            for part in link.fragment.removeprefix('/').split('/'):
                if part:
                    target = target[part.replace('~1', '/').replace('~0', '~')]
        for child in value.values():
            check_refs(child, document, path)
    elif isinstance(value, list):
        for child in value:
            check_refs(child, document, path)

for path in (ROOT / 'contracts').glob('*.json'):
    document = json.loads(path.read_text(encoding='utf-8'))
    check_refs(document, document, path)

ignored = ['.env', '.env.production', 'server/.env', 'server/.env.local', 'deploy/quakerelay.env',
           'AuthKey_TEST.p8', 'ios/signing/key.p8', 'ios/signing/test.mobileprovision',
           'ios/signing/test.provisionprofile', 'private.pem', 'private.key', 'signing.p12',
           'credentials/token.json', 'recovery-secret.asc', 'quick-relay-example.tar.gz.gpg', 'server/runtime.db-wal', 'server/runtime.sqlite3-shm',
           'ios/DerivedData/build.txt', 'ios/QuakeRelay.xcodeproj/xcuserdata/local.xcuserstate']
git = subprocess.run(['git', 'check-ignore', '--no-index', '-z', '--stdin'], input=('\0'.join(ignored) + '\0').encode(),
                     cwd=ROOT, capture_output=True)
require(git.returncode == 0 and set(git.stdout.decode().rstrip('\0').split('\0')) == set(ignored),
        'A secret/runtime path is not ignored')
examples = subprocess.run(['git', 'check-ignore', '--no-index', 'server/.env.example', 'contracts/examples/report.valid.json'],
                          cwd=ROOT, text=True, capture_output=True)
require(examples.returncode == 1 and not examples.stdout, 'A reviewed example is unexpectedly ignored')
print('PASS: source boundaries, JSON/plist, local documentation links, API routes, schema references, secret/runtime ignore rules')

# App identity is shared by the supplied project, XcodeGen, Keychain and APNs.
identity = 'jp.kb-dev.quickrelay'
project = (ROOT / 'ios/QuakeRelay.xcodeproj/project.pbxproj').read_text(encoding='utf-8')
spec = (ROOT / 'ios/project.yml').read_text(encoding='utf-8')
require(project.count(f'PRODUCT_BUNDLE_IDENTIFIER = {identity};') == 2, 'App target bundle ID drift')
require(project.count(f'PRODUCT_BUNDLE_IDENTIFIER = {identity}.tests;') == 2, 'Test target bundle ID drift')
require(f'PRODUCT_BUNDLE_IDENTIFIER: {identity}' in spec, 'XcodeGen bundle ID drift')
require(f'APNS_BUNDLE_ID={identity}' in (ROOT / 'server/.env.example').read_text(encoding='utf-8'), 'APNs topic drift')
info = plistlib.loads((ROOT / 'ios/QuakeRelay/Resources/Info.plist').read_bytes())
require(info['CFBundleDisplayName'] == 'Quick Relay', 'Application display name drift')
require(info['QuakeRelayAPNsEnvironment'] == '$(APNS_ENVIRONMENT)', 'APNs entitlement/plist environment drift')
for path in (ROOT / 'ios').rglob('*.swift'):
    require(path.name in project, f'Swift file absent from existing Xcode project: {path.name}')
print('PASS: app identity, APNs topic, iOS environment and existing project source references')
