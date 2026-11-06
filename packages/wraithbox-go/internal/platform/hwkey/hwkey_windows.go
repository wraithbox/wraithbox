package hwkey

// Open returns the hardware key store of windows, whose hosts are later
// (S12-platforms): a TPM key through the CNG platform crypto provider
// (S09-06-ca-keys, "Windows"). It isn't built, so Generate and Restore
// return an error that wraps errors.ErrUnsupported.
func Open() Provider { return unsupported{what: "the CNG platform crypto provider key on windows"} }
