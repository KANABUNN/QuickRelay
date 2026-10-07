#!/bin/bash
# A temporary keychain and a manually issued profile sign only this app.
set -euo pipefail
umask 077
repo=$(cd "$(dirname "$0")/../.." && pwd)
bundle=jp.kb-dev.quickrelay
: "${RUNNER_TEMP:?Run on an ephemeral macOS runner}"
: "${APPLE_TEAM_ID:?}"
: "${IOS_DISTRIBUTION_P12_BASE64:?}"
: "${IOS_DISTRIBUTION_P12_PASSWORD:?}"
: "${IOS_PROVISION_PROFILE_BASE64:?}"
: "${ASC_PRIVATE_KEY_BASE64:?}"
: "${ASC_KEY_ID:?}"
: "${ASC_ISSUER_ID:?}"
: "${BUILD_NUMBER:?}"
[[ "$APPLE_TEAM_ID" =~ ^[A-Z0-9]{10}$ ]]
[[ "$ASC_KEY_ID" =~ ^[A-Z0-9]{10}$ ]]
[[ "$ASC_ISSUER_ID" =~ ^[a-fA-F0-9-]{36}$ ]]
[[ "$BUILD_NUMBER" =~ ^[1-9][0-9]{0,3}\.[0-9]{1,2}\.[0-9]{1,2}$ ]]
signing=$(mktemp -d "$RUNNER_TEMP/quick-relay-signing.XXXXXX")
keychain="$signing/signing.keychain-db"
profile_path=
profile_path_legacy=
cleanup() {
  security delete-keychain "$keychain" >/dev/null 2>&1 || true
  if [ -n "$profile_path" ]; then rm -f "$profile_path"; fi
  if [ -n "$profile_path_legacy" ]; then rm -f "$profile_path_legacy"; fi
  case "$signing" in "$RUNNER_TEMP"/quick-relay-signing.*) rm -rf "$signing" ;; esac
}
trap cleanup EXIT
export QR_SIGNING_DIR="$signing"
python3 - <<'PY'
import base64, os
from pathlib import Path
p = Path(os.environ['QR_SIGNING_DIR'])
for variable, name in [('IOS_DISTRIBUTION_P12_BASE64', 'certificate.p12'),
                       ('IOS_PROVISION_PROFILE_BASE64', 'profile.mobileprovision')]:
    (p / name).write_bytes(base64.b64decode(os.environ[variable], validate=True))
(p / 'private_keys').mkdir(mode=0o700)
(p / 'private_keys' / ('AuthKey_' + os.environ['ASC_KEY_ID'] + '.p8')).write_bytes(
    base64.b64decode(os.environ['ASC_PRIVATE_KEY_BASE64'], validate=True))
PY
unset IOS_DISTRIBUTION_P12_BASE64 IOS_PROVISION_PROFILE_BASE64 ASC_PRIVATE_KEY_BASE64
security cms -D -i "$signing/profile.mobileprovision" > "$signing/profile.plist"
python3 "$repo/ios/Scripts/validate-distribution.py" profile "$signing/profile.plist" \
  --team "$APPLE_TEAM_ID" --bundle "$bundle" --metadata "$signing/profile.json"
profile_uuid=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["uuid"])' "$signing/profile.json")
identity=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["certificate_sha1"])' "$signing/profile.json")
[[ "$profile_uuid" =~ ^[A-Fa-f0-9-]{36}$ ]]
keychain_password=$(openssl rand -hex 32)
security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
# The exported Windows P12 holds the leaf and key; complete Apple's chain locally.
curl --fail --silent --show-error https://www.apple.com/certificateauthority/AppleWWDRCAG3.cer \
  -o "$signing/AppleWWDRCAG3.cer"
security import "$signing/AppleWWDRCAG3.cer" -k "$keychain" >/dev/null
security import "$signing/certificate.p12" -P "$IOS_DISTRIBUTION_P12_PASSWORD" \
  -k "$keychain" -T /usr/bin/codesign -T /usr/bin/security >/dev/null
unset IOS_DISTRIBUTION_P12_PASSWORD
security set-key-partition-list -S apple-tool:,apple:,codesign: -s \
  -k "$keychain_password" "$keychain" >/dev/null
unset keychain_password
security list-keychains -d user -s "$keychain" "$HOME/Library/Keychains/login.keychain-db"
security find-identity -v -p codesigning "$keychain" | grep -Fq "$identity"
for folder in "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles" "$HOME/Library/MobileDevice/Provisioning Profiles"; do
  mkdir -p "$folder"
  test ! -e "$folder/$profile_uuid.mobileprovision"
done
profile_path="$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles/$profile_uuid.mobileprovision"
profile_path_legacy="$HOME/Library/MobileDevice/Provisioning Profiles/$profile_uuid.mobileprovision"
cp "$signing/profile.mobileprovision" "$profile_path"
cp "$signing/profile.mobileprovision" "$profile_path_legacy"
output="$repo/.tmp/testflight"
mkdir -p "$output"
xcodebuild -quiet -project "$repo/ios/QuakeRelay.xcodeproj" -scheme QuakeRelay \
  -configuration Release -destination 'generic/platform=iOS' \
  -archivePath "$output/QuakeRelay.xcarchive" \
  DEVELOPMENT_TEAM="$APPLE_TEAM_ID" CODE_SIGN_STYLE=Manual \
  CODE_SIGN_IDENTITY="$identity" PROVISIONING_PROFILE_SPECIFIER="$profile_uuid" \
  CURRENT_PROJECT_VERSION="$BUILD_NUMBER" archive
app="$output/QuakeRelay.xcarchive/Products/Applications/QuakeRelay.app"
codesign --verify --deep --strict "$app"
codesign -d --entitlements :- "$app" > "$signing/signed-entitlements.plist" 2>/dev/null
python3 "$repo/ios/Scripts/validate-distribution.py" bundle "$app" --bundle "$bundle" \
  --build "$BUILD_NUMBER" --team "$APPLE_TEAM_ID" --entitlements "$signing/signed-entitlements.plist" \
  > "$output/validation.json"
export QR_PROFILE_UUID="$profile_uuid" QR_SIGNING_IDENTITY="$identity"
python3 - <<'PY'
import os, plistlib
from pathlib import Path
data = {'method': 'app-store-connect', 'destination': 'export', 'signingStyle': 'manual',
        'teamID': os.environ['APPLE_TEAM_ID'], 'signingCertificate': os.environ['QR_SIGNING_IDENTITY'],
        'provisioningProfiles': {'jp.kb-dev.quickrelay': os.environ['QR_PROFILE_UUID']},
        'manageAppVersionAndBuildNumber': False, 'uploadSymbols': True,
        'testFlightInternalTestingOnly': True}
(Path(os.environ['QR_SIGNING_DIR']) / 'ExportOptions.plist').write_bytes(plistlib.dumps(data))
PY
xcodebuild -quiet -exportArchive -archivePath "$output/QuakeRelay.xcarchive" \
  -exportPath "$output/export" -exportOptionsPlist "$signing/ExportOptions.plist"
ipa="$output/export/QuakeRelay.ipa"
test -s "$ipa"
shasum -a 256 "$ipa" > "$output/ipa-sha256.txt"
# altool reads ./private_keys; neither API key nor signing identity is an artifact.
cd "$signing"
xcrun altool --validate-app --type ios --file "$ipa" --apiKey "$ASC_KEY_ID" --apiIssuer "$ASC_ISSUER_ID"
xcrun altool --upload-app --type ios --file "$ipa" --apiKey "$ASC_KEY_ID" --apiIssuer "$ASC_ISSUER_ID"
printf 'Uploaded %s (%s) for internal TestFlight processing.\n' "$bundle" "$BUILD_NUMBER"
