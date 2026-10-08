"""Validate decoded Apple signing profiles and built distribution bundles."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import plistlib


def require(condition, message):
    if not condition:
        raise ValueError(message)


def load(path):
    return plistlib.loads(Path(path).read_bytes())


def validate_profile(profile, team, bundle, extension=False):
    entitlement = profile["Entitlements"]
    require(profile["TeamIdentifier"] == [team], "Profile team differs")
    require(entitlement["application-identifier"] == f"{team}.{bundle}", "Profile App ID differs")
    if not extension:
        require(entitlement["aps-environment"] == "production", "Production push missing from profile")
        require(entitlement.get("com.apple.developer.usernotifications.time-sensitive") is True,
                "Time Sensitive Notifications missing from profile")
    require(entitlement.get("get-task-allow") is False, "Development profile cannot be distributed")
    require(entitlement.get("beta-reports-active") is True, "App Store profile required")
    require(not profile.get("ProvisionedDevices") and not profile.get("ProvisionsAllDevices"),
            "Ad Hoc or enterprise profile cannot be used")
    expires = profile["ExpirationDate"].replace(tzinfo=datetime.timezone.utc)
    require(expires > datetime.datetime.now(datetime.timezone.utc), "Profile expired")
    require(len(profile["DeveloperCertificates"]) == 1, "Expected one signing certificate")


def validate_bundle(bundle, identifier, build, signed_entitlements=None, team=None, extension_entitlements=None):
    app = Path(bundle)
    info = load(app / "Info.plist")
    require(info["CFBundleIdentifier"] == identifier, "Built bundle ID differs")
    require(info["CFBundleVersion"] == build, "Built version differs")
    require(info["QuakeRelayAPNsEnvironment"] == "production", "Built APNs environment differs")
    require(info.get("ITSAppUsesNonExemptEncryption") is False, "Encryption declaration missing")
    require(info.get("CFBundleIcons", {}).get("CFBundlePrimaryIcon", {}).get("CFBundleIconName") == "AppIcon",
            "Compiled AppIcon missing")
    require(info.get("NSSupportsLiveActivities") is True, "Live Activities support missing")
    extension = app / "PlugIns" / "QuickRelayLiveActivity.appex"
    validate_extension(extension, identifier + ".LiveActivity", build, info["CFBundleShortVersionString"], extension_entitlements, team)
    privacy = load(app / "PrivacyInfo.xcprivacy")
    require(privacy["NSPrivacyTracking"] is False, "Unexpected tracking declaration")
    for sound in ("quake_warning.caf", "quake_update.caf", "normal.caf"):
        require((app / sound).stat().st_size > 0, f"Missing notification sound: {sound}")
    if signed_entitlements is not None:
        entitlement = load(signed_entitlements)
        require(entitlement["application-identifier"] == f"{team}.{identifier}", "Signed App ID differs")
        require(entitlement["aps-environment"] == "production", "Signed push environment differs")
        require(entitlement.get("get-task-allow", False) is False, "Debug signing enabled")
        require(entitlement.get("com.apple.developer.usernotifications.time-sensitive") is True,
                "Signed Time Sensitive entitlement missing")
    print(json.dumps({"bundle_id": identifier, "version": info["CFBundleShortVersionString"],
                      "build": build, "apns": "production", "privacy_manifest": True,
                      "notification_sounds": True, "live_activity_extension": True}, sort_keys=True))


def validate_extension(path, identifier, build, version, signed_entitlements=None, team=None):
    extension = Path(path)
    info = load(extension / "Info.plist")
    require(info["CFBundleIdentifier"] == identifier, "Extension App ID differs")
    require(info["CFBundleVersion"] == build and info["CFBundleShortVersionString"] == version, "Extension version differs")
    require(info.get("NSExtension", {}).get("NSExtensionPointIdentifier") == "com.apple.widgetkit-extension",
            "Live Activity WidgetKit extension missing")
    privacy = load(extension / "PrivacyInfo.xcprivacy")
    require(privacy["NSPrivacyTracking"] is False, "Unexpected extension tracking")
    if signed_entitlements is not None:
        entitlement = load(signed_entitlements)
        require(entitlement["application-identifier"] == f"{team}.{identifier}", "Signed extension App ID differs")
        require(entitlement.get("get-task-allow", False) is False, "Extension debug signing enabled")

def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    profile = sub.add_parser("profile")
    profile.add_argument("path")
    profile.add_argument("--team", required=True)
    profile.add_argument("--bundle", required=True)
    profile.add_argument("--metadata", required=True)
    profile.add_argument("--extension", action="store_true")
    app = sub.add_parser("bundle")
    app.add_argument("path")
    app.add_argument("--bundle", required=True)
    app.add_argument("--build", required=True)
    app.add_argument("--entitlements")
    app.add_argument("--team")
    app.add_argument("--extension-entitlements")
    args = parser.parse_args()
    if args.command == "profile":
        data = load(args.path)
        validate_profile(data, args.team, args.bundle, args.extension)
        metadata = {"uuid": data["UUID"], "name": data["Name"],
                    "certificate_sha1": hashlib.sha1(data["DeveloperCertificates"][0]).hexdigest().upper()}
        Path(args.metadata).write_text(json.dumps(metadata), encoding="utf-8")
        print("PASS: distribution profile, team, App ID, expiry, production push and Time Sensitive")
    else:
        validate_bundle(args.path, args.bundle, args.build, args.entitlements, args.team, args.extension_entitlements)


if __name__ == "__main__":
    main()
