// Reference: CryptoKit Secure Enclave key persisted as dataRepresentation.
// ckref create FILE | ckref load FILE
import CryptoKit
import Foundation

let args = CommandLine.arguments
do {
    let key: SecureEnclave.P256.Signing.PrivateKey
    if args[1] == "create" {
        key = try SecureEnclave.P256.Signing.PrivateKey()
        try key.dataRepresentation.write(to: URL(fileURLWithPath: args[2]))
        print("dataRepresentation: \(key.dataRepresentation.count) bytes, starts",
              key.dataRepresentation.prefix(16).map { String(format: "%02x", $0) }.joined())
    } else {
        key = try SecureEnclave.P256.Signing.PrivateKey(
            dataRepresentation: Data(contentsOf: URL(fileURLWithPath: args[2])))
    }
    let pub = SHA256.hash(data: key.publicKey.derRepresentation)
    print("public key SHA-256:", pub.prefix(8).map { String(format: "%02x", $0) }.joined())
    let sig = try key.signature(for: Data("x09".utf8))
    print("verifies:", key.publicKey.isValidSignature(sig, for: Data("x09".utf8)))
} catch {
    print("FAIL:", error)
    exit(1)
}
