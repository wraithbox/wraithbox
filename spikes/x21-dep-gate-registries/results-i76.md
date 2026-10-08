# I76: the checksum-database clock, end to end

Run on 2026-10-08 (UTC), macOS 27 on Apple Silicon, go1.27.1, against the
public proxy.golang.org, sum.golang.org and index.golang.org. Proxy:
`proxy/main.go` with `proxy/sumdbclock.go`, `-go-clock sumdb`. The go
command ran with `GOSUMDB=sum.golang.org`, and reached it through the proxy
(`GOPROXY=http://<addr>/goproxy` serves `/sumdb/sum.golang.org/...`).

## clock.py 5: record numbers against first-seen time

```text
now 2026-10-08T02:58:43.000000Z; tree size before sampling 67058346
sampled 320 index entries over 16 days in 4.0s (8 at once); lookup median 87 ms, p95 176 ms
errors: 0 []
no record until this lookup created one: 0 of 320
  of those, first seen more than 7 days ago: 0
prompt records: 320; inverted pairs 4; widest inversion in first-seen time 0:00:03.799884
calibration at 2026-10-01T02:58:43.000000Z: point 66157247; window of 20: min 66157247 median 66157260 max 66157269; created by this lookup 0
calibration at now - 7d - 30 min: point 66151273 (difference 5974)
calibration at now - 7d - 60 min: point 66146854 (difference 10393)
first seen >= 7 days ago but young by the clock (prompt records): 0
first seen < 7 days ago but old by the clock: 0
records added per second over the last 7 days: 1.49
```

## goclock.py installs: lockfile installs (`go mod download`), modes off, refuse, filter

```text
{"name": "lock-cli_cli", "mode": "off", "rc": 0, "seconds": 18.5, "downloads": 179, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 339, records past the calibration tree size 0, calibration errors 0, point 66157893 tree 67058346"}
{"name": "lock-cli_cli", "mode": "refuse", "rc": 0, "seconds": 18.8, "downloads": 179, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 339, records past the calibration tree size 0, calibration errors 0, point 66157902 tree 67059172"}
{"name": "lock-cli_cli", "mode": "filter", "rc": 0, "seconds": 20.0, "downloads": 179, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 339, records past the calibration tree size 0, calibration errors 0, point 66157904 tree 67059172"}
{"name": "lock-junegunn_fzf", "mode": "off", "rc": 0, "seconds": 4.4, "downloads": 11, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 171, records past the calibration tree size 0, calibration errors 0, point 66157931 tree 67059172"}
{"name": "lock-junegunn_fzf", "mode": "refuse", "rc": 0, "seconds": 3.4, "downloads": 11, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 170, records past the calibration tree size 0, calibration errors 0, point 66157931 tree 67059172"}
{"name": "lock-junegunn_fzf", "mode": "filter", "rc": 0, "seconds": 3.5, "downloads": 11, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 168, records past the calibration tree size 0, calibration errors 0, point 66157931 tree 67059172"}
{"name": "lock-caddyserver_caddy", "mode": "off", "rc": 0, "seconds": 32.0, "downloads": 165, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 325, records past the calibration tree size 0, calibration errors 0, point 66157931 tree 67059172"}
{"name": "lock-caddyserver_caddy", "mode": "refuse", "rc": 0, "seconds": 32.4, "downloads": 165, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 325, records past the calibration tree size 0, calibration errors 0, point 66157974 tree 67059172"}
{"name": "lock-caddyserver_caddy", "mode": "filter", "rc": 0, "seconds": 32.3, "downloads": 165, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 309, records past the calibration tree size 0, calibration errors 0, point 66158009 tree 67059172"}
{"name": "lock-prometheus_node_exporter", "mode": "off", "rc": 0, "seconds": 5.7, "downloads": 54, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 214, records past the calibration tree size 0, calibration errors 0, point 66158051 tree 67059013"}
{"name": "lock-prometheus_node_exporter", "mode": "refuse", "rc": 0, "seconds": 4.6, "downloads": 54, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 209, records past the calibration tree size 0, calibration errors 0, point 66158057 tree 67059013"}
{"name": "lock-prometheus_node_exporter", "mode": "filter", "rc": 0, "seconds": 4.5, "downloads": 54, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 186, records past the calibration tree size 0, calibration errors 0, point 66158060 tree 67059013"}
{"name": "lock-gohugoio_hugo", "mode": "off", "rc": 0, "seconds": 30.4, "downloads": 180, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 340, records past the calibration tree size 0, calibration errors 0, point 66158060 tree 67059013"}
{"name": "lock-gohugoio_hugo", "mode": "refuse", "rc": 0, "seconds": 30.5, "downloads": 180, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 335, records past the calibration tree size 0, calibration errors 0, point 66158093 tree 67059419"}
{"name": "lock-gohugoio_hugo", "mode": "filter", "rc": 0, "seconds": 30.6, "downloads": 180, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 340, records past the calibration tree size 0, calibration errors 0, point 66158130 tree 67059419"}
```

## goclock.py baseline: the same lockfile installs straight from proxy.golang.org, no gate

```text
{"name": "lock-cli_cli", "mode": "no gate", "rc": 0, "seconds": 16.2}
{"name": "lock-junegunn_fzf", "mode": "no gate", "rc": 0, "seconds": 2.6}
{"name": "lock-caddyserver_caddy", "mode": "no gate", "rc": 0, "seconds": 31.3}
{"name": "lock-prometheus_node_exporter", "mode": "no gate", "rc": 0, "seconds": 3.9}
{"name": "lock-gohugoio_hugo", "mode": "no gate", "rc": 0, "seconds": 28.1}
```

## Fresh install: `go get` of five fast-moving modules @latest, then `go mod download`

Rerun after fixing the @v/list filter to pass 404 answers through. The go
command probes path prefixes such as `k8s.io/@v/list`, and the first run
filtered the words of those 404 bodies as if they were versions.

```text
{"name": "fresh", "mode": "off", "rc": 0, "seconds": 7.0, "downloads": 21, "young": 1, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 30, "proxy": "sumdb lookups 141, records past the calibration tree size 0, calibration errors 0, point 66160126 tree 67060334"}
    young: github.com/aws/aws-sdk-go-v2/service/s3 v1.114.1
{"name": "fresh", "mode": "refuse", "rc": 1, "seconds": 3.6, "downloads": 6, "young": 1, "refused": 1, "hidden": 0, "lists": 0, "go_sumdb_lookups": 7, "proxy": "sumdb lookups 124, records past the calibration tree size 0, calibration errors 0, point 66160145 tree 67060334"}
    refused: github.com/aws/aws-sdk-go-v2/service/s3 v1.114.1 min-age
    server response: wraith box dependency gate (x21 spike): Go github.com/aws/aws-sdk-go-v2/service/s3@v1.114.1 published 2026-10-06T16:44:36Z, younger than 168h0m0s
{"name": "fresh", "mode": "filter", "rc": 0, "seconds": 9.2, "downloads": 21, "young": 0, "refused": 0, "hidden": 3, "lists": 21, "go_sumdb_lookups": 29, "proxy": "sumdb lookups 3423, records past the calibration tree size 0, calibration errors 0, point 66160151 tree 67060334"}
```

Hidden in filter mode: aws-sdk-go-v2/service/s3 1 (v1.114.1), aws/smithy-go 2.
The "published" time in a refusal is estimated from the record number at
the rate between the calibration point and the tree size. An earlier run
gave 16:50:47 for the same version.

## goclock.py lists full, lists lazy: cost of filtering @v/list (16 lookups at once, cold cache)

```text
{"module": "github.com/aws/aws-sdk-go", "kept": 1862, "seconds": 5.66}
{"module": "github.com/aws/aws-sdk-go-v2/service/s3", "kept": 300, "seconds": 0.92}
{"module": "k8s.io/client-go", "kept": 670, "seconds": 1.86}
{"module": "google.golang.org/grpc", "kept": 248, "seconds": 0.78}
{"module": "github.com/spf13/cobra", "kept": 27, "seconds": 0.11}
{"module": "golang.org/x/net", "kept": 59, "seconds": 0.23}
{"module": "github.com/hashicorp/terraform", "kept": 474, "seconds": 1.31}
{"module": "github.com/prometheus/client_golang", "kept": 57, "seconds": 0.22}
sumdb lookups 4074, records past the calibration tree size 0, calibration errors 0, point 66157893 tree 67058346
  golang.org/x/text: listed 50, hidden 0, lookups 50, 1.53s
  github.com/aws/aws-sdk-go: listed 1865, hidden 3, lookups 1865, 5.60s
  github.com/aws/aws-sdk-go-v2/service/s3: listed 301, hidden 1, lookups 301, 0.82s
  k8s.io/client-go: listed 670, hidden 0, lookups 670, 1.83s
  google.golang.org/grpc: listed 248, hidden 0, lookups 248, 0.75s
  github.com/spf13/cobra: listed 27, hidden 0, lookups 27, 0.08s
  golang.org/x/net: listed 59, hidden 0, lookups 59, 0.13s
  github.com/hashicorp/terraform: listed 476, hidden 2, lookups 476, 1.26s
  github.com/prometheus/client_golang: listed 58, hidden 1, lookups 58, 0.20s

{"module": "github.com/aws/aws-sdk-go", "kept": 1865, "seconds": 0.06}
{"module": "github.com/aws/aws-sdk-go-v2/service/s3", "kept": 300, "seconds": 0.07}
{"module": "k8s.io/client-go", "kept": 670, "seconds": 0.05}
{"module": "google.golang.org/grpc", "kept": 248, "seconds": 0.04}
{"module": "github.com/spf13/cobra", "kept": 27, "seconds": 0.04}
{"module": "golang.org/x/net", "kept": 59, "seconds": 0.04}
{"module": "github.com/hashicorp/terraform", "kept": 476, "seconds": 0.04}
{"module": "github.com/prometheus/client_golang", "kept": 57, "seconds": 0.05}
sumdb lookups 31, records past the calibration tree size 0, calibration errors 0, point 66157893 tree 67058346
  golang.org/x/text: listed 50, hidden 0, lookups 1, 1.30s
  github.com/aws/aws-sdk-go: listed 1865, hidden 0, lookups 1, 0.03s
  github.com/aws/aws-sdk-go-v2/service/s3: listed 301, hidden 1, lookups 2, 0.04s
  k8s.io/client-go: listed 670, hidden 0, lookups 1, 0.02s
  google.golang.org/grpc: listed 248, hidden 0, lookups 1, 0.02s
  github.com/spf13/cobra: listed 27, hidden 0, lookups 1, 0.02s
  golang.org/x/net: listed 59, hidden 0, lookups 1, 0.02s
  github.com/hashicorp/terraform: listed 476, hidden 0, lookups 1, 0.02s
  github.com/prometheus/client_golang: listed 58, hidden 1, lookups 2, 0.03s
```

The first list (golang.org/x/text) includes calibration. `listcheck.py`
shows why versions were hidden. aws-sdk-go v1.10.49, v1.12.42 and v1.12.68
have no record: sum.golang.org answers 404 ("case-insensitive file name
collision"), so the proxy can't serve them either. terraform v1.17.0-rc1
and v1.16.5 have records past the point, so they are young. One
`listcheck.py` run with 16 lookups at once, while installs ran in parallel,
got a connection reset from sum.golang.org.

## goclock.py unreachable: index.golang.org unreachable

With `-index-url http://127.0.0.1:9` there is no calibration point, and
every download and every @v/list is refused with `go-clock-uncalibrated`.
With the index broken after the first calibration (the point is kept, and
every later request tries to recalibrate and fails), installs decide on the
stale point.

```text
{"name": "unreachable-lock", "mode": "filter", "rc": 1, "seconds": 2.9, "downloads": 179, "young": 0, "refused": 179, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 0, records past the calibration tree size 0, calibration errors 179"}
{"name": "unreachable-fresh", "mode": "filter", "rc": 1, "seconds": 0.1, "downloads": 0, "young": 0, "refused": 14, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 0, records past the calibration tree size 0, calibration errors 1572"}
{"name": "stale-lock", "mode": "filter", "rc": 0, "seconds": 17.2, "downloads": 179, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 0, "proxy": "sumdb lookups 314, records past the calibration tree size 0, calibration errors 171, point 66159958 tree 67060334"}
{"name": "stale-fresh", "mode": "filter", "rc": 0, "seconds": 10.9, "downloads": 21, "young": 0, "refused": 0, "hidden": 76, "lists": 29, "go_sumdb_lookups": 29, "proxy": "sumdb lookups 5538, records past the calibration tree size 0, calibration errors 2684, point 66160042 tree 67060334"}
```

stale-fresh ran before the 404 fix, so its "hidden 76" counts words of 404
bodies. It passed. The first unreachable rows ran before the stale rows were
added and are copied from that run. Calibration isn't single-flight in the
spike: each request that arrives before a point exists calibrates on its
own, which is why the lookups and calibration errors exceed the downloads.

## goclock.py pseudo 3: `go get <module>@<commit>` of commits from before 2022

```text
{"name": "pseudo-cobra-b36196066e3b", "mode": "refuse", "rc": 1, "seconds": 10.9, "downloads": 1, "young": 1, "refused": 1, "hidden": 0, "lists": 0, "go_sumdb_lookups": 1, "proxy": "sumdb lookups 21, records past the calibration tree size 1, calibration errors 0, point 66159765 tree 67059419"}
    young: github.com/spf13/cobra v1.1.3-0.20210630212458-b36196066e3b
    refused: github.com/spf13/cobra v1.1.3-0.20210630212458-b36196066e3b min-age
{"name": "pseudo-cobra-06e4b59b206e", "mode": "refuse", "rc": 0, "seconds": 7.2, "downloads": 3, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 285, "proxy": "sumdb lookups 23, records past the calibration tree size 0, calibration errors 0, point 66159773 tree 67059419"}
{"name": "pseudo-cobra-8eaca5f0f49a", "mode": "refuse", "rc": 0, "seconds": 10.0, "downloads": 3, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 565, "proxy": "sumdb lookups 23, records past the calibration tree size 0, calibration errors 0, point 66159777 tree 67060154"}
{"name": "pseudo-mux-64954673e972", "mode": "refuse", "rc": 0, "seconds": 6.3, "downloads": 1, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 2, "proxy": "sumdb lookups 21, records past the calibration tree size 0, calibration errors 0, point 66159782 tree 67059626"}
{"name": "pseudo-mux-a31c1782bfb1", "mode": "refuse", "rc": 0, "seconds": 4.8, "downloads": 1, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 2, "proxy": "sumdb lookups 21, records past the calibration tree size 0, calibration errors 0, point 66159784 tree 67059626"}
{"name": "pseudo-mux-49c01487a141", "mode": "refuse", "rc": 0, "seconds": 2.7, "downloads": 1, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 2, "proxy": "sumdb lookups 21, records past the calibration tree size 0, calibration errors 0, point 66159789 tree 67059626"}
{"name": "pseudo-logrus-a752a62f5ec3", "mode": "refuse", "rc": 0, "seconds": 6.7, "downloads": 2, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 7, "proxy": "sumdb lookups 22, records past the calibration tree size 0, calibration errors 0, point 66159793 tree 67059626"}
{"name": "pseudo-logrus-feebf74e97cb", "mode": "refuse", "rc": 0, "seconds": 7.3, "downloads": 3, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 8, "proxy": "sumdb lookups 23, records past the calibration tree size 0, calibration errors 0, point 66159795 tree 67059626"}
{"name": "pseudo-logrus-15ca3c069407", "mode": "refuse", "rc": 0, "seconds": 7.5, "downloads": 3, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 8, "proxy": "sumdb lookups 23, records past the calibration tree size 0, calibration errors 0, point 66159801 tree 67059626"}
{"name": "pseudo-testify-404e6fa1bed3", "mode": "refuse", "rc": 0, "seconds": 5.3, "downloads": 5, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 10, "proxy": "sumdb lookups 25, records past the calibration tree size 0, calibration errors 0, point 66159806 tree 67059626"}
{"name": "pseudo-testify-2c1f261278a7", "mode": "refuse", "rc": 0, "seconds": 6.3, "downloads": 5, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 10, "proxy": "sumdb lookups 25, records past the calibration tree size 0, calibration errors 0, point 66159807 tree 67059626"}
{"name": "pseudo-testify-e2b269ecc544", "mode": "refuse", "rc": 0, "seconds": 5.5, "downloads": 5, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 10, "proxy": "sumdb lookups 25, records past the calibration tree size 0, calibration errors 0, point 66159809 tree 67059626"}
{"name": "pseudo-toml-b08c266e0aa4", "mode": "refuse", "rc": 0, "seconds": 5.4, "downloads": 1, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 7, "proxy": "sumdb lookups 21, records past the calibration tree size 0, calibration errors 0, point 66159818 tree 67059626"}
{"name": "pseudo-toml-848557949c17", "mode": "refuse", "rc": 0, "seconds": 5.9, "downloads": 1, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 7, "proxy": "sumdb lookups 21, records past the calibration tree size 0, calibration errors 0, point 66159824 tree 67059626"}
{"name": "pseudo-toml-8162ef318b13", "mode": "refuse", "rc": 0, "seconds": 5.9, "downloads": 1, "young": 0, "refused": 0, "hidden": 0, "lists": 0, "go_sumdb_lookups": 11, "proxy": "sumdb lookups 21, records past the calibration tree size 0, calibration errors 0, point 66159833 tree 67059626"}
pseudo-versions of commits before 2022: 18, installed 14, refused 4
```

urfave/cli (3 commits) is left out: the proxy answers 404 because those
commits have a `/v2` module path, before the gate sees a version. Of the 15
valid pseudo-versions, 14 were already in the checksum database and
installed. One (cobra b36196066e3b) got its record from the gate's lookup
and was refused as young.
