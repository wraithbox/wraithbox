# X09-keychain-unsigned spike (throwaway)

Does the ad-hoc signed Keychain model of S15-least-privilege work, and
can an ad-hoc signed binary without entitlements use a Secure Enclave
key that survives a restart? Issue #23. The answer is in
`docs/spikes/X09-keychain-unsigned.md` on `main`.

Run attended on the host (macOS 27.0.1, Apple M2), with a throwaway
keychain file under `work/`, never the login keychain.

## Layout

- `shim.c`, `shim.h`: cgo shim over Security.framework. Uses two SPI
  symbols that the SDK headers don't declare: `kSecAttrTokenOID`
  (`"toid"`) and `SecKeychainItemSetAccessWithPassword`.
- `main.go`: `x9` subcommands: `kc-create`, `unlock`, `lock`, `add`,
  `read`, `delete`, `set-access`, `set-access2`, `se-create`, `se-find`,
  `se-delete`, `se-restore`, `se-bench`. `X9_NOUI=1` sets
  `kSecUseAuthenticationUIFail` on reads, and `X9_NOUI2=1` calls
  `SecKeychainSetUserInteractionAllowed(false)` at start.
- `ckref.swift`: CryptoKit reference, a Secure Enclave key persisted as
  `dataRepresentation` and loaded again.
- `build.sh [suffix]`: builds `bin/x9-a` and `bin/x9-b`, ad-hoc signed
  with the hardened runtime. A new suffix gives new cdhashes, as an
  upgrade does.

## Run

```sh
./build.sh
bin/x9-a se-create 0 x09-eph work/oid     # ephemeral SE key, saves its token OID
bin/x9-a se-create 2 x09-perm             # permanent: -34018
bin/x9-b se-restore 3 work/oid            # same public key, other binary
bin/x9-a se-bench 200
K=$PWD/work/x09.keychain; PW=$(openssl rand -hex 24)
bin/x9-a kc-create $K $PW
bin/x9-a add $K svc-self secret self
bin/x9-b read $K svc-self                 # dialog
X9_NOUI2=1 bin/x9-b read $K svc-self      # -25293, no dialog
security delete-keychain $K
```
