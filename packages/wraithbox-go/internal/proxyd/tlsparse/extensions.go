package tlsparse

// knownExtensions is the list of S07-16-tls-and-tunnels, "Pass": the
// ClientHello extensions wb-proxyd recognizes. A ClientHello to a pass
// host with any other type, past the GREASE values, is reset with
// tls-extension-unknown (fail closed), so a new extension that could
// carry a name or data past the relay's checks waits until it is added
// here. early_data, encrypted_client_hello and the encrypted SNI aren't
// in it: the pass relay refuses them with rules of their own. Each value
// is from IANA's "TLS ExtensionType Values".
var knownExtensions = map[uint16]string{
	0:      "server_name",
	1:      "max_fragment_length",
	5:      "status_request",
	10:     "supported_groups",
	11:     "ec_point_formats",
	13:     "signature_algorithms",
	16:     "application_layer_protocol_negotiation",
	18:     "signed_certificate_timestamp",
	21:     "padding",
	22:     "encrypt_then_mac",
	23:     "extended_master_secret",
	27:     "compress_certificate",
	28:     "record_size_limit",
	34:     "delegated_credential",
	35:     "session_ticket",
	41:     "pre_shared_key",
	43:     "supported_versions",
	44:     "cookie",
	45:     "psk_key_exchange_modes",
	47:     "certificate_authorities",
	49:     "post_handshake_auth",
	50:     "signature_algorithms_cert",
	51:     "key_share",
	17513:  "application_settings (old code point)",
	17613:  "application_settings",
	0xff01: "renegotiation_info",
}

// KnownExtension reports whether the pass relay takes an extension
// type: one of knownExtensions, or a GREASE value.
func KnownExtension(t uint16) bool {
	if IsGREASE(t) {
		return true
	}
	_, ok := knownExtensions[t]
	return ok
}

// IsGREASE reports whether t is one of the 16 GREASE values of RFC 8701,
// 0x0a0a, 0x1a1a and so on to 0xfafa.
func IsGREASE(t uint16) bool {
	return t&0x0f0f == 0x0a0a && t>>8 == t&0xff
}
