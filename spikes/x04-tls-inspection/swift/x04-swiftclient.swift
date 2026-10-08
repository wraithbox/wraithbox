// X04-tls-inspection spike: a URLSession HTTPS client. Throwaway.
//
//   x04-swiftclient <url> [count] [interval seconds]
//
// Each request uses a new ephemeral URLSession, so a new connection. Prints
// one JSON line per request. Exit 1 if the last request failed.
import Foundation

let args = CommandLine.arguments
let url = URL(string: args[1])!
let n = args.count > 3 ? Int(args[2])! : 1
let every = args.count > 3 ? UInt32(args[3])! : 0
var ok = false
for i in 0..<n {
    if i > 0 { sleep(every) }
    let s = URLSession(configuration: .ephemeral)
    var out: [String: Any] = ["t": ISO8601DateFormatter().string(from: Date()), "i": i]
    let sem = DispatchSemaphore(value: 0)
    var req = URLRequest(url: url)
    req.timeoutInterval = 20
    s.dataTask(with: req) { _, resp, error in
        if let e = error as NSError? {
            out["err"] = "\(e.domain) \(e.code) \(e.localizedDescription)"
            ok = false
        } else if let h = resp as? HTTPURLResponse {
            out["status"] = h.statusCode
            ok = true
        }
        sem.signal()
    }.resume()
    sem.wait()
    s.invalidateAndCancel()
    let d = try! JSONSerialization.data(withJSONObject: out, options: [.sortedKeys])
    print(String(data: d, encoding: .utf8)!)
    fflush(stdout)
}
exit(ok ? 0 : 1)
