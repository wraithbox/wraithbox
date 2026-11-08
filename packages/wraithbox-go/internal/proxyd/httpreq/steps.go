package httpreq

import (
	"io"
	"net/http"
	"strings"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/policy/match"
)

// Step is one step a request passes on its way upstream. It may change
// the request, such as remove or add header fields, and returns a
// Refusal to stop it.
type Step interface {
	Apply(req *Request) *Refusal
}

// BodyStep is the streaming scan of the body as it goes upstream. Body
// returns the reader the body is sent from. A read error that is a
// *Refusal stops the request with it.
type BodyStep interface {
	Body(req *Request, body io.Reader) io.Reader
}

// ResponseStep changes the header fields of each response before it
// goes to the guest, such as removing the credential headers of a host
// with a binding (S07-08-requests-and-credentials, "Credential headers
// in responses").
type ResponseStep interface {
	Response(req *Request, h http.Header)
}

// Steps are the steps of S07-08-requests-and-credentials, "Order of the
// steps", which every request passes in this order:
//
//  1. canonicalize, which this package does;
//  2. RemoveCredentials (M3.6d);
//  3. the checks on the head: the transfer coding, which this package
//     does, then HeadChecks (the placeholder scan and
//     `model-content-encoding`, M4.10);
//  4. Policy (M3.7);
//  5. DependencyGate (M5.7);
//  6. Inject (M4.10);
//  7. Body, as the body goes upstream (M4.10).
//
// RemoveCredentials and Policy are required. Without either, Run
// refuses every request with http-step-missing, so no wiring mistake
// sends a request upstream with the guest's credentials or without HTTP
// policy (SEC04-no-guest-secrets, SEC05-default-deny). The other steps
// only add checks or credentials, and are skipped when nil.
type Steps struct {
	RemoveCredentials Step
	HeadChecks        []Step
	Policy            Step
	DependencyGate    Step
	Inject            Step
	Body              BodyStep
	Response          ResponseStep
}

// Run passes req through steps 1 to 6, in order, and stops at the first
// refusal. Step 7 runs on the body as it is sent.
func (s *Steps) Run(req *Request) *Refusal {
	if s == nil || s.RemoveCredentials == nil || s.Policy == nil {
		return refuse(http.StatusInternalServerError, RuleStepMissing,
			"the credential removal step or the HTTP policy step is not in place")
	}
	if ref := Canonicalize(req); ref != nil {
		return ref
	}
	if ref := s.RemoveCredentials.Apply(req); ref != nil {
		return ref
	}
	if ref := TransferCoding(req); ref != nil {
		return ref
	}
	for _, step := range s.HeadChecks {
		if step == nil {
			continue
		}
		if ref := step.Apply(req); ref != nil {
			return ref
		}
	}
	for _, step := range []Step{s.Policy, s.DependencyGate, s.Inject} {
		if step == nil {
			continue
		}
		if ref := step.Apply(req); ref != nil {
			return ref
		}
	}
	return nil
}

// Canonicalize sets req's canonical path, or refuses its path with
// http-path-form (S07-08-requests-and-credentials, "Request form").
func Canonicalize(req *Request) *Refusal {
	canon, r := match.CanonicalPath(req.Path)
	if r != nil {
		// The refusal's message holds offsets, never the path's bytes.
		return refuse(http.StatusBadRequest, RulePathForm, strings.TrimPrefix(r.Error(), r.Rule+": "))
	}
	req.CanonicalPath = canon
	return nil
}

// TransferCoding checks req's framing on its head, and sets Chunked
// (S07-08-requests-and-credentials, "Transfer coding"). It refuses with
// http-transfer-coding a `Transfer-Encoding` other than one field of one
// `chunked`, one next to a `Content-Length`, and any on HTTP/1.0, which
// RFC 9112, section 6.1, treats as faulty framing.
func TransferCoding(req *Request) *Refusal {
	switch te := req.TransferEncoding; {
	case len(te) == 0:
		return nil
	case len(te) > 1:
		return refuse(http.StatusBadRequest, RuleTransferCoding, "the request has more than one Transfer-Encoding field")
	case !equalFold(te[0], "chunked"):
		return refuse(http.StatusBadRequest, RuleTransferCoding, "the Transfer-Encoding is not one chunked")
	case req.ContentLength >= 0:
		return refuse(http.StatusBadRequest, RuleTransferCoding, "the request has both Transfer-Encoding and Content-Length")
	case req.Proto == "HTTP/1.0":
		return refuse(http.StatusBadRequest, RuleTransferCoding, "an HTTP/1.0 request has a Transfer-Encoding")
	}
	req.Chunked = true
	return nil
}
