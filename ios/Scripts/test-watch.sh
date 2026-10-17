#!/bin/sh
# Build the checked-in single-target watchOS companion without signing credentials.
set -eu
cd "$(dirname "$0")/.."
xcodebuild -quiet -project QuakeRelay.xcodeproj -scheme QuickRelayWatch \
  -sdk watchsimulator -destination 'generic/platform=watchOS Simulator' \
  CODE_SIGNING_ALLOWED=NO build
