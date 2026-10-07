#!/bin/sh
# Uses the checked-in project and a simulator compatible with the selected SDK.
set -eu
cd "$(dirname "$0")/.."
sdk=$(xcrun --sdk iphonesimulator --show-sdk-version)
simulator=$(xcrun simctl list devices available --json | python3 -c '
import json,re,sys
sdk=tuple(map(int,sys.argv[1].split(".")[:2]))
devices=json.load(sys.stdin)["devices"]
candidates=[]
for runtime,items in devices.items():
    match=re.search(r"\.iOS-(\d+)-(\d+)",runtime)
    if match and (17,0)<=tuple(map(int,match.groups()))<=sdk:
        for item in items:
            if item.get("isAvailable") and item["name"].startswith("iPhone"):
                candidates.append((tuple(map(int,match.groups())),item["udid"]))
if not candidates:
    raise SystemExit("No iPhone simulator compatible with selected Xcode SDK")
print(max(candidates)[1])
' "$sdk")
# Simulator ad-hoc signing supplies the application entitlement needed by
# the real Keychain tests, without an Apple account or distribution identity.
xcodebuild -project QuakeRelay.xcodeproj -scheme QuakeRelay \
  -destination "platform=iOS Simulator,id=$simulator,arch=$(uname -m)" \
  -parallel-testing-enabled NO CODE_SIGNING_ALLOWED=YES \
  CODE_SIGN_IDENTITY=- CODE_SIGN_STYLE=Manual DEVELOPMENT_TEAM= test
