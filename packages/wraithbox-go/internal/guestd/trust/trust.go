// Package trust makes the guest's clients trust the VM's inspection CAs
// (S09-08-guest-trust). wb-guestd's InstallTrust writes the CAs to the
// trust files in Dir with an Installer, and every project user's shell
// gets the variables of Env, which point common clients at those files.
//
// On a macOS guest nothing here touches the system trust store: macOS
// asks a user to confirm a trusted root (S09-08-guest-trust, "System
// trust store").
package trust

// The trust directory and its files (S09-08-guest-trust, "Files"). The
// paths hold no whitespace and no character a shell treats specially.
const (
	// Dir is the trust directory: owned by root, mode 0755.
	Dir = "/etc/wraithbox/trust"
	// CAFile holds every current CA of the VM, PEM, in the request's
	// order.
	CAFile = "ca.pem"
	// BundleFile holds the guest's public roots and then every current
	// CA, PEM.
	BundleFile = "bundle.pem"
	// JavaTrustStoreFile is a PKCS12 trust store without a password,
	// with the same certificates as BundleFile.
	JavaTrustStoreFile = "java-truststore.p12"

	// CAPath, BundlePath and JavaTrustStorePath are the files' full
	// paths.
	CAPath             = Dir + "/" + CAFile
	BundlePath         = Dir + "/" + BundleFile
	JavaTrustStorePath = Dir + "/" + JavaTrustStoreFile

	// PublicRootsPath is the macOS guest's file of public roots, which
	// each install reads again for the bundle and the Java trust store.
	// A Linux guest's comes with Linux guests (S12-platforms).
	PublicRootsPath = "/etc/ssl/cert.pem"
)

// The rules of InstallTrust, for the log and the refusal's ErrorInfo.
const (
	// RuleRefused: the request isn't 1 to 3 distinct DER X.509
	// certificates of at most 1024 bytes, each a CA (basic constraints
	// with CA:TRUE) with a P-256 public key. Nothing is written.
	RuleRefused = "trust-install-refused"
	// RuleDirUnsafe: the trust directory is a symbolic link, isn't a
	// directory, or has another owner than root or another mode than
	// 0755. Nothing is written.
	RuleDirUnsafe = "trust-dir-unsafe"
	// RuleFailed: the public roots couldn't be read, a file couldn't be
	// built, or a write, sync or rename failed. The files renamed into
	// place before the failure stay, and the next install writes all of
	// them again.
	RuleFailed = "trust-install-failed"
	// RuleInstalled: every file was written and renamed into place.
	RuleInstalled = "trust-installed"
)

// variables is the table of S09-08-guest-trust, "Variables": each name
// and the file it points at. Each value is built from constants.
var variables = [...]struct{ name, value string }{
	{"SSL_CERT_FILE", BundlePath},
	{"REQUESTS_CA_BUNDLE", BundlePath},
	{"GIT_SSL_CAINFO", BundlePath},
	{"CARGO_HTTP_CAINFO", BundlePath},
	{"NODE_EXTRA_CA_CERTS", CAPath},
	{"JAVA_TOOL_OPTIONS", "-Djavax.net.ssl.trustStore=" + JavaTrustStorePath},
}

// Env returns the trust variables as NAME=value entries, in a new
// slice. Every project user's shell gets them, and the toolchain user
// gets them for a toolchain reconcile (S09-08-guest-trust,
// "Variables"). They are set whether or not an install has happened:
// a client that reads a missing file fails closed.
func Env() []string {
	out := make([]string, 0, len(variables))
	for _, v := range variables {
		out = append(out, v.name+"="+v.value)
	}
	return out
}

// Names returns the names of the trust variables, in a new slice.
func Names() []string {
	out := make([]string, 0, len(variables))
	for _, v := range variables {
		out = append(out, v.name)
	}
	return out
}
