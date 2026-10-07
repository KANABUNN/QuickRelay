# Internal TestFlight distribution

This workflow builds the existing iOS client on a GitHub-hosted macOS runner,
signs it with a manually issued Apple Distribution identity and app-specific
App Store Connect profile, validates it, and uploads it for internal TestFlight.
It does not submit an App Store version or create a public invitation link.

## Before a release

1. Issue an App Store Connect distribution profile for `jp.kb-dev.quickrelay`.
   Its certificate must match the P12; production Push Notifications and Time
   Sensitive Notifications must be enabled. The signing script checks these,
   profile expiry, App ID, team, and that this is not a development/Ad Hoc profile.
2. Configure a `testflight` GitHub environment. Permit only the reviewed release
   tag(s). Keep signing inputs in this environment rather than repository-wide
   secrets. Require the owner to approve this environment; allow self-review for a single-owner project.
3. Set these environment secrets (including the team identifier to mask it in logs):

   | Secret | Value |
   |---|---|
   | `APPLE_TEAM_ID` | Apple team identifier |
   | `IOS_DISTRIBUTION_P12_BASE64` | Base64 of password-protected P12 |
   | `IOS_DISTRIBUTION_P12_PASSWORD` | P12 password |
   | `IOS_PROVISION_PROFILE_BASE64` | Base64 of distribution mobileprovision |
   | `ASC_PRIVATE_KEY_BASE64` | Base64 of App Store Connect API private key |
   | `ASC_KEY_ID` | API key identifier |
   | `ASC_ISSUER_ID` | API issuer identifier |

   A Developer role can upload builds. Apple team API keys cover all apps in the
   team, so obtain approval for that scope before creating or transferring one.
   APNs provider keys, DMDATA credentials and VPS settings are not CI inputs.

4. Open a PR and wait for `iOS distribution readiness` to pass with Xcode/iOS SDK
   26 or newer. It runs simulator tests and a Release archive without secrets,
   then verifies compiled icons, the privacy manifest, notification sounds and
   the production APNs configuration.

## Upload and accept

1. Tag a reviewed commit already merged into main with a new `testflight-...` tag and push that tag.
   `Upload internal TestFlight build` uses the exact tagged revision; a normal PR
   or branch push cannot use the signing job. Manual reruns require the tag too.
2. The build number is `(1000 + workflow-run-number).run-attempt.0`; marketing version
   comes from the project. Keep the run number within Apple's four-digit major
   component limit. A rerun uses a different build number. The offset avoids
   reusing earlier build numbers after moving repositories.
3. The temporary keychain, P12, API key and installed profiles are removed on
   exit. Only the IPA checksum and public validation metadata are uploaded as
   GitHub artifacts. The IPA and signing files are not retained as artifacts.
4. Wait for Apple processing. Select the build in the internal TestFlight group
   containing the owner only, then enable testing. Upload completion alone is
   not proof of processing or installation. Internal-only exports cannot be
   submitted for App Store release or external testing.
5. Before pairing the TestFlight client, the VPS must select production APNs.
   Having a production `.p8` on disk is not enough. Check the live configuration
   and change it through the separately reviewed VPS procedure.
6. Install using TestFlight, allow notifications, pair using the deployed API
   URL and a fresh pairing code, and confirm real delivery. Simulator acceptance
   and a successful signed upload do not replace this final iPhone check.

## Application declarations

The temporary icon is original vector artwork in `ios/Artwork/AppIcon.svg`;
`generate-icon.py` renders the checked-in opaque 1024px PNG using Pillow.
The privacy manifest declares UserDefaults for app-local preferences (`CA92.1`)
and device identifiers for delivery/registration functionality, without tracking.
The app uses Apple's URLSession and Keychain encryption rather than a bundled
cryptographic implementation; `ITSAppUsesNonExemptEncryption` is false.
Review these declarations whenever networking, analytics, storage or SDKs change.

References: [Apple profile creation](https://developer.apple.com/help/account/provisioning-profiles/create-an-app-store-provisioning-profile),
[build uploads](https://developer.apple.com/help/app-store-connect/manage-builds/upload-builds/),
[GitHub signing guidance](https://docs.github.com/en/actions/how-tos/deploy/deploy-to-third-party-platforms/sign-xcode-applications).
