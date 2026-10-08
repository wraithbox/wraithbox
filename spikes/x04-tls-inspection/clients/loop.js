// X04 spike: Node or Bun HTTPS client. node loop.js <url> [count] [interval s]
// Each request uses a new connection (agent: false). Throwaway.
const https = require("https");
const [url, n = "1", every = "0"] = process.argv.slice(2);
let ok = false;
const one = (i) =>
  new Promise((res) => {
    const out = { t: new Date().toISOString(), i };
    const req = https.get(url, { agent: false, timeout: 20000 }, (r) => {
      out.status = r.statusCode;
      ok = true;
      r.resume();
      r.on("end", () => res(out));
    });
    req.on("error", (e) => {
      out.err = `${e.code || ""} ${e.message}`;
      ok = false;
      res(out);
    });
  });
(async () => {
  for (let i = 0; i < +n; i++) {
    if (i > 0) await new Promise((r) => setTimeout(r, +every * 1000));
    console.log(JSON.stringify(await one(i)));
  }
  process.exit(ok ? 0 : 1);
})();
