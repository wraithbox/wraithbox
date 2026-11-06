package hwkey

// Open returns the hardware key store of linux, whose hosts are later
// (S12-platforms): a TPM 2.0 key, or a software key in the secret store
// (S09-06-ca-keys, "Linux"). It isn't built, so Generate and Restore
// return an error that wraps errors.ErrUnsupported.
func Open() Provider { return unsupported{what: "the TPM or software key on linux"} }
