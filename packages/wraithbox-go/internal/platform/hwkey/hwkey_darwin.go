package hwkey

// Open returns the hardware key store of darwin: a Secure Enclave key
// that isn't stored in the Keychain, whose wrapped form is its token
// object ID (S09-06-ca-keys, "macOS, every build"). It isn't built yet,
// so Generate and Restore return an error that wraps
// errors.ErrUnsupported.
func Open() Provider { return unsupported{what: "the Secure Enclave key on darwin"} }
