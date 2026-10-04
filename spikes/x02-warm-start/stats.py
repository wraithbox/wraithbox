import json,sys,math
def pct(v,p):
    v=sorted(v); k=(len(v)-1)*p; f=math.floor(k); c=math.ceil(k)
    return v[f] if f==c else v[f]+(v[c]-v[f])*(k-f)
rows=[json.loads(l) for l in open(sys.argv[1]) if l.strip()]
rows=[r for r in rows if r.get('cmd') in sys.argv[2].split(',')]
keys=sys.argv[3].split(',')
print(f"n={len(rows)}")
for k in keys:
    v=[r[k] for r in rows if isinstance(r.get(k),(int,float))]
    if not v: print(k,'none'); continue
    print(f"{k:22s} p50={pct(v,.5):10.3f} p95={pct(v,.95):10.3f} min={min(v):10.3f} max={max(v):10.3f} n={len(v)}")
