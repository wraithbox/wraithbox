//go:build darwin && !cgo

package hwkey

// Open is the hardware key store of a darwin build without cgo, such as
// go:cross from Linux. Such a build has no Secure Enclave shim
// (S10-01-libraries), so Generate and Restore return an error that
// wraps errors.ErrUnsupported, which the caller treats as
// `ca-key-restore`. The bundle's build sets CGO_ENABLED=1, so a bundle
// never ships this.
func Open() Provider { return unsupported{what: "the Secure Enclave key in a darwin build without cgo"} }
