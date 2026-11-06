package main

import (
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// probe is one TLS client TestC02GuestTrustVM runs in the guest, and
// what it prints when the handshake and the request succeed.
type probe struct {
	name string
	ok   string
}

// probes are the clients that must trust the CA the guest installed
// (X04-tls-inspection, S09-08-guest-trust, "Variables"). Each runs with
// the shell's environment and nothing the test adds, and is told the
// server's address in its own way, since leafHost never resolves.
var probes = []probe{
	{"curl", "wbtest-ok"},
	{"git", "refs/heads/main"},
	{"python3", "wbtest-ok"},
	{"node", "wbtest-ok"},
	{"java", "java-handshake-ok"},
}

// urlSessionProbe is URLSession, which reads only the system trust
// store and so must refuse the leaf (S09-08-guest-trust, "System trust
// store").
const urlSessionProbe = "urlsession"

// trustErrors are the NSURLError codes of a refused server certificate:
// untrusted, bad date, not yet valid, unknown root, and the rejection
// of the secure connection as a whole.
var trustErrors = []string{"-1202", "-1201", "-1203", "-1204", "-1200"}

// pythonProbe fetches the server's root with urllib, and resolves
// leafHost to loopback.
const pythonProbe = `import socket, sys, urllib.request
real = socket.getaddrinfo
def lookup(host, *args, **kwargs):
    return real("127.0.0.1" if host == "` + leafHost + `" else host, *args, **kwargs)
socket.getaddrinfo = lookup
body = urllib.request.urlopen("https://` + leafHost + `:%s/" % sys.argv[1], timeout=20).read()
sys.stdout.write(body.decode())
`

// nodeProbe fetches the server's root with https.get, and resolves
// leafHost to loopback with its lookup option.
const nodeProbe = `const https = require("https");
const lookup = (host, opts, cb) => opts && opts.all
  ? cb(null, [{ address: "127.0.0.1", family: 4 }])
  : cb(null, "127.0.0.1", 4);
https.get({ host: "` + leafHost + `", port: Number(process.argv[1]), path: "/", lookup, timeout: 20000 }, (res) => {
  res.setEncoding("utf8");
  res.on("data", (d) => process.stdout.write(d));
  res.on("end", () => process.exit(0));
}).on("error", (e) => { console.log("error " + (e.code || e.message)); process.exit(1); });
`

// javaProbe completes a handshake with the server, with leafHost as the
// server name and HTTPS host name checks. Only JAVA_TOOL_OPTIONS points
// it at the trust store.
const javaProbe = `import java.net.Socket;
import java.util.List;
import javax.net.ssl.SNIHostName;
import javax.net.ssl.SSLParameters;
import javax.net.ssl.SSLSocket;
import javax.net.ssl.SSLSocketFactory;

public class Probe {
  public static void main(String[] args) throws Exception {
    int port = Integer.parseInt(args[0]);
    Socket raw = new Socket("127.0.0.1", port);
    SSLSocketFactory f = (SSLSocketFactory) SSLSocketFactory.getDefault();
    SSLSocket s = (SSLSocket) f.createSocket(raw, "` + leafHost + `", port, true);
    SSLParameters p = s.getSSLParameters();
    p.setEndpointIdentificationAlgorithm("HTTPS");
    p.setServerNames(List.of(new SNIHostName("` + leafHost + `")));
    s.setSSLParameters(p);
    s.startHandshake();
    System.out.println("java-handshake-ok");
    s.close();
  }
}
`

// urlSessionScript is a JavaScript for Automation script that fetches
// https://leafHost/ with URLSession through the server's CONNECT proxy,
// since URLSession can't be told an address. It polls the task, so no
// block crosses into JavaScript, and prints "ok <status>" or
// "error <NSURLError code>".
const urlSessionScript = `ObjC.import("Foundation");
function run(argv) {
  const cfg = $.NSClassFromString("NSURLSessionConfiguration").ephemeralSessionConfiguration;
  const proxy = $.NSMutableDictionary.dictionary;
  proxy.setObjectForKey($.NSNumber.numberWithInt(1), $("HTTPSEnable"));
  proxy.setObjectForKey($("127.0.0.1"), $("HTTPSProxy"));
  proxy.setObjectForKey($.NSNumber.numberWithInt(Number(argv[0])), $("HTTPSPort"));
  cfg.connectionProxyDictionary = proxy;
  cfg.timeoutIntervalForRequest = 20;
  const session = $.NSClassFromString("NSURLSession").sessionWithConfiguration(cfg);
  const task = session.dataTaskWithURL($.NSURL.URLWithString("https://` + leafHost + `/"));
  task.resume;
  const deadline = Date.now() + 30000;
  while (Number(task.state) !== 3 && Date.now() < deadline) {
    $.NSRunLoop.currentRunLoop.runUntilDate($.NSDate.dateWithTimeIntervalSinceNow(0.1));
  }
  if (Number(task.state) !== 3) return "timeout";
  if (!task.error.isNil()) return "error " + task.error.code;
  return "ok " + task.response.statusCode;
}
`

// clientsScript returns the script that starts the server with leaf on
// the guest's loopback, runs each client against it, and prints each
// one's output between "--client NAME--" and "--exit NAME CODE--", or
// "--missing NAME--" for a client the image doesn't have, and then the
// server's log. The server and the leaf are written from the script's
// here-documents, so they reach the guest only on the shell's standard
// input (SEC02-no-host-fs-share).
func clientsScript(server, leaf []byte) string {
	var b strings.Builder
	doc := func(cmd, body string) {
		b.WriteString(cmd + " <<'WB_EOF'\n" + body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("WB_EOF\n")
	}
	b.WriteString("d=$(mktemp -d) || exit 9\n")
	doc(`base64 -D -o "$d/tlsserver"`, wrap76(base64.StdEncoding.EncodeToString(server)))
	b.WriteString(`chmod 700 "$d/tlsserver"` + "\n")
	doc(`cat > "$d/leaf.pem"`, string(leaf))
	doc(`cat > "$d/probe.py"`, pythonProbe)
	doc(`cat > "$d/probe.js"`, nodeProbe)
	doc(`cat > "$d/Probe.java"`, javaProbe)
	doc(`cat > "$d/urlsession.js"`, urlSessionScript)
	b.WriteString(`"$d/tlsserver" -cert "$d/leaf.pem" -ports "$d/ports" -lifetime 3m </dev/null 2>"$d/server.log" &
server_pid=$!
i=0
while [ ! -s "$d/ports" ] && [ $i -lt 300 ]; do sleep 0.1; i=$((i+1)); done
read tls proxy < "$d/ports" || { echo --no-server--; cat "$d/server.log"; exit 9; }
have() {
  p=$(command -v "$1") || return 1
  case $p in /usr/bin/*) xcode-select -p >/dev/null 2>&1 || return 1;; esac
}
run() {
  name=$1; shift
  echo "--client $name--"; "$@" </dev/null 2>&1; echo "--exit $name $?--"
}
have curl && run curl curl -sS --max-time 20 --resolve "` + leafHost + `:$tls:127.0.0.1" "https://` + leafHost + `:$tls/" || echo --missing curl--
have git && run git git -c "http.curloptResolve=` + leafHost + `:$tls:127.0.0.1" ls-remote "https://` + leafHost + `:$tls/repo.git" || echo --missing git--
have python3 && run python3 python3 -c "$(cat "$d/probe.py")" "$tls" || echo --missing python3--
have node && run node node -e "$(cat "$d/probe.js")" "$tls" || echo --missing node--
/usr/libexec/java_home >/dev/null 2>&1 && run java java "$d/Probe.java" "$tls" || echo --missing java--
run urlsession osascript -l JavaScript "$d/urlsession.js" "$proxy"
kill $server_pid
echo --server--; cat "$d/server.log"; echo --end-server--
rm -rf "$d"
`)
	return b.String()
}

// wrap76 breaks s into lines of 76 characters.
func wrap76(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76] + "\n")
		s = s[76:]
	}
	b.WriteString(s + "\n")
	return b.String()
}

// clientResult is one client's run in clientsScript's output.
type clientResult struct {
	ran  bool
	code int
	out  string
}

// parseClients returns each client's result in clientsScript's output.
// A client that is missing, or whose block is cut, has ran false.
func parseClients(stdout string) map[string]clientResult {
	results := map[string]clientResult{}
	rest := stdout
	for {
		_, after, ok := strings.Cut(rest, "--client ")
		if !ok {
			return results
		}
		name, body, ok := strings.Cut(after, "--\n")
		if !ok {
			return results
		}
		out, tail, ok := strings.Cut(body, "--exit "+name+" ")
		if !ok {
			return results
		}
		codeText, next, _ := strings.Cut(tail, "--")
		code, err := strconv.Atoi(codeText)
		if err != nil {
			return results
		}
		results[name] = clientResult{ran: true, code: code, out: out}
		rest = next
	}
}

// checkClients starts the server with leaf in a shell of project id,
// and checks that each client of probes accepts it when accept is true
// and refuses it when false, and that URLSession refuses it either way.
// what names the leaf in the test's log.
func (r *vmRun) checkClients(id string, server, leaf []byte, what string, accept bool) {
	t := r.t
	res := r.runShell(id, shellIO{stdin: strings.NewReader(clientsScript(server, leaf))})
	t.Logf("clients against %s: exit %d\n%s\nstderr:\n%s", what, res.code, res.stdout, res.stderr)
	if res.code != 0 {
		t.Errorf("the clients script against %s: exit %d, want 0", what, res.code)
	}
	results := parseClients(res.stdout)
	checkProbes(t, results, what, accept)
	checkURLSession(t, results[urlSessionProbe], between(res.stdout, "--server--\n", "--end-server--"), what)
}

// checkProbes checks the result of each client of probes against what.
func checkProbes(t *testing.T, results map[string]clientResult, what string, accept bool) {
	t.Helper()
	for _, p := range probes {
		got, found := results[p.name]
		switch {
		case !found:
			t.Logf("result: %s against %s: not run, the image doesn't have it", p.name, what)
		case accept && (got.code != 0 || !strings.Contains(got.out, p.ok)):
			t.Errorf("%s refused %s: exit %d\n%s", p.name, what, got.code, got.out)
		case !accept && (got.code == 0 || strings.Contains(got.out, p.ok)):
			t.Errorf("%s accepted %s: exit %d\n%s", p.name, what, got.code, got.out)
		default:
			t.Logf("result: %s against %s: exit %d, as expected", p.name, what, got.code)
		}
	}
}

// checkURLSession checks that URLSession reached the server through its
// proxy and refused the leaf for its trust.
func checkURLSession(t *testing.T, got clientResult, serverLog, what string) {
	t.Helper()
	if !strings.Contains(serverLog, "CONNECT "+leafHost+":443") {
		t.Errorf("URLSession didn't reach the server through its proxy, server log:\n%s", serverLog)
	}
	code, refused := strings.CutPrefix(strings.TrimSpace(got.out), "error ")
	if !got.ran || !refused || !slices.Contains(trustErrors, code) {
		t.Errorf("URLSession against %s: %q, want the refusal of an untrusted certificate, one of %v", what, got.out, trustErrors)
		return
	}
	t.Logf("result: %s against %s: refused, NSURLError %s", urlSessionProbe, what, code)
}

func TestParseClients(t *testing.T) {
	stdout := "noise\n--client curl--\nwbtest-ok\n--exit curl 0--\n" +
		"--missing git--\n" +
		"--client python3--\nTraceback\nssl.SSLCertVerificationError\n--exit python3 1--\n" +
		"--client node--\ncut off"
	got := parseClients(stdout)
	want := map[string]clientResult{
		"curl":    {ran: true, code: 0, out: "wbtest-ok\n"},
		"python3": {ran: true, code: 1, out: "Traceback\nssl.SSLCertVerificationError\n"},
	}
	if len(got) != len(want) {
		t.Fatalf("parseClients = %v, want %v", got, want)
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("parseClients[%s] = %+v, want %+v", name, got[name], w)
		}
	}
}

func TestClientsScriptCarriesServerAndLeaf(t *testing.T) {
	server := []byte(strings.Repeat("\x00\x01binary", 40))
	leaf := []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")
	script := clientsScript(server, leaf)
	enc := between(script, "base64 -D -o \"$d/tlsserver\" <<'WB_EOF'\n", "WB_EOF\n")
	dec, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(enc), ""))
	if err != nil || string(dec) != string(server) {
		t.Errorf("the script's server decodes to %d bytes, %v; want the %d bytes given", len(dec), err, len(server))
	}
	for line := range strings.Lines(enc) {
		if len(line) > 77 {
			t.Errorf("a base64 line of %d bytes, want at most 76 and a newline", len(line))
		}
	}
	if got := between(script, "cat > \"$d/leaf.pem\" <<'WB_EOF'\n", "WB_EOF\n"); got != string(leaf) {
		t.Errorf("the script's leaf = %q, want %q", got, leaf)
	}
	for _, p := range probes {
		if !strings.Contains(script, "--missing "+p.name+"--") {
			t.Errorf("the script doesn't run %s", p.name)
		}
	}
}
