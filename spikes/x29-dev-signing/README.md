# X29-dev-signing spike (throwaway)

Can helpers in an app bundle, signed by a free personal team, share the
data protection keychain through `keychain-access-groups`? Result:
`docs/spikes/X29-dev-signing.md`.

- `app/X29.xcodeproj`: a minimal app target. Building it once in the
  Xcode IDE registers the Mac and makes the Apple Development
  certificate and the Xcode-managed profile.
- `helper/`: the `x29` helper, a cgo shim over Security.framework:
  `put`/`get`/`del`/`list` generic passwords, `se-new`/`se-sign` for a
  wrapped Secure Enclave key (its token object ID in an item), and
  `seperm-new`/`seperm-sign`/`seperm-del` for a permanent one.
- `build.sh <variant>`: builds the app, puts two helpers in
  `Contents/Helpers/x29-{a,b}.app` with the profile embedded, signs
  them. `X29_LAYOUT=bare` puts them in `Contents/MacOS` instead, which
  AMFI refuses.

Run with `X29_GROUP=<TEAMID>.nl.wraithbox.store`.
