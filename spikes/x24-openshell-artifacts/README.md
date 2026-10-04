# X24-openshell-artifacts spike (throwaway)

Do the OpenShell parts that S07, S09, S10 and S13 build on exist in the
assumed form, under a permissive license, at a pinnable version?
Result write-up: `docs/spikes/X24-openshell-artifacts.md` on `main`.

## Pins

| Part | Source | Pin | License |
|---|---|---|---|
| OpenShell (schema, prover, proto, OCSF crate) | github.com/NVIDIA/OpenShell | tag `v0.1.2`, commit `6648bd0c290efbc41ba131ee9831ee45cd431f94` (2026-09-28) | Apache-2.0 |
| `openshell-prover` macOS arm64 binary | release asset `openshell-prover-aarch64-apple-darwin.tar.gz` | sha256 `77f624498e1110abd0da26e926a71c834b5e58f8baa5a33252d59d7ab4629a53` | Apache-2.0 (links Z3, MIT) |
| OCSF schema | github.com/ocsf/ocsf-schema | tag `1.8.0`, commit `6fa6499a0f8c9f449d342816e90e5f687c224b0a` | Apache-2.0 |
| agent-safehouse | github.com/eugene1g/agent-safehouse | tag `v0.12.0`, commit `6bded066b6536b2f51ea18028a55150d66901fa4` | Apache-2.0 |

The prover asset's checksum matches `openshell-prover-checksums-sha256.txt`
and the GitHub release digest, and `gh attestation verify` passes (build
provenance from `release-tag.yml` at `refs/tags/v0.1.2`, commit `6648bd0c`).
The binary is ad hoc signed (`codesign -dv`: `Signature=adhoc`). The
release has prover archives for macOS arm64 and Linux x86_64 and arm64
(musl), and none for Windows.

## Files

- `policies/`: sample policies. `combined.yaml` is a Wraith Box file
  (OpenShell keys plus a `wraithbox:` top-level key). Each other case is
  checked against `boundary.yaml`, or against `<case>.boundary.yaml`.
- `run-prover.sh <prover>`: runs every case; output in `prover-output.txt`.
- `middleware/`: `buf generate` of `supervisor_middleware.proto` and
  `extension.proto` (copied from the pin, Apache-2.0), and `main.go`, a
  dependency gate stub called in process and over an in-memory gRPC
  connection; output in `middleware-output.txt`. Regenerate with
  `mise exec buf@1.72.0 go:google.golang.org/protobuf/cmd/protoc-gen-go@1.36.11 go:google.golang.org/grpc/cmd/protoc-gen-go-grpc@1.6.2 -- buf generate`,
  run with `GOWORK=off go run .`.

## Not run

The proposal risk check is a Rust library API (`openshell_prover::queries`)
with the delta step in `openshell-server`. The standalone binary only has
`check`. A Rust driver that links the library was not built in this spike;
the risk check answer comes from reading the pinned source.
