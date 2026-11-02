import {readFileSync,writeFileSync,readdirSync,statSync} from 'fs';
import {join} from 'path';
const D='/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/d/';
const raw=readFileSync(D+'golist.json','utf8');
const pk=[];let depth=0,s=0;for(let i=0;i<raw.length;i++){const c=raw[i];if(c=='{'){if(depth==0)s=i;depth++}else if(c=='}'){depth--;if(depth==0)pk.push(JSON.parse(raw.slice(s,i+1)))}}
const mod='github.com/wraithbox/wraithbox/packages/wraithbox-go';
const rel=p=>p.replace(mod,'').replace(/^\//,'')||'.';
const loc=f=>{const t=readFileSync(f,'utf8');return t.split('\n').filter(l=>l.trim()&&!l.trim().startsWith('//')).length};
const P={};
for(const p of pk){const r=rel(p.ImportPath);
 const gen=r.startsWith('internal/gen');
 const code=[...(p.GoFiles||[]),...(p.CgoFiles||[]),...(p.IgnoredGoFiles||[]).filter(f=>!f.endsWith('_test.go'))];
 const tests=[...(p.TestGoFiles||[]),...(p.XTestGoFiles||[]),...(p.IgnoredGoFiles||[]).filter(f=>f.endsWith('_test.go'))];
 // all .go files in dir for completeness
 const all=readdirSync(p.Dir).filter(f=>f.endsWith('.go'));
 const codeF=all.filter(f=>!f.endsWith('_test.go')),testF=all.filter(f=>f.endsWith('_test.go'));
 const L=fs=>fs.map(f=>({f,l:loc(join(p.Dir,f))}));
 const cl=L(codeF),tl=L(testF);
 P[r]={pkg:r,gen,doc:!!(p.Doc),code_loc:cl.reduce((a,x)=>a+x.l,0),test_loc:tl.reduce((a,x)=>a+x.l,0),code_files:cl.length,test_files:tl.length,maxfile:cl.sort((a,b)=>b.l-a.l)[0]||{},
  imports:(p.Imports||[]).filter(i=>i.startsWith(mod)).map(rel),name:p.Name,top:r.split('/')[0]};}
for(const r in P){P[r].out=new Set(P[r].imports)}
for(const r in P){P[r].in=new Set()}
for(const r in P)for(const i of P[r].out)if(P[i])P[i].in.add(r);
const rows=['package,kind,code_loc,test_loc,test_to_code,code_files,test_files,largest_file,largest_file_loc,fan_in,fan_out,instability,has_pkg_comment'];
for(const r of Object.keys(P).sort()){const p=P[r];const ca=p.in.size,ce=p.out.size;
 rows.push([r,p.gen?'generated':p.top,p.code_loc,p.test_loc,p.code_loc?(p.test_loc/p.code_loc).toFixed(2):'',p.code_files,p.test_files,p.maxfile.f||'',p.maxfile.l||'',ca,ce,(ca+ce)?(ce/(ca+ce)).toFixed(2):'',p.doc?1:0].join(','))}
writeFileSync(D+'go_packages.csv',rows.join('\n')+'\n');
// cmd transitive
const trans=(r,seen=new Set())=>{for(const i of P[r].out)if(P[i]&&!seen.has(i)){seen.add(i);trans(i,seen)}return seen};
const cr=['binary,own_code_loc,direct_internal_imports,transitive_internal_pkgs,transitive_code_loc'];
for(const r of Object.keys(P).filter(r=>r.startsWith('cmd/'))){const t=[...trans(r)].filter(i=>!P[i].gen);cr.push([r,P[r].code_loc,[...P[r].out].filter(i=>!P[i]?.gen).length,t.length,t.reduce((a,i)=>a+P[i].code_loc,0)+P[r].code_loc].join(','))}
writeFileSync(D+'go_cmd_packages.csv',cr.join('\n')+'\n');
// aggregates
const ng=Object.values(P).filter(p=>!p.gen);
const tot={packages:Object.keys(P).length,nongen_packages:ng.length,code_loc:ng.reduce((a,p)=>a+p.code_loc,0),test_loc:ng.reduce((a,p)=>a+p.test_loc,0),gen_loc:Object.values(P).filter(p=>p.gen).reduce((a,p)=>a+p.code_loc,0)};
const byTop={};for(const p of ng){const t=p.top==='internal'?'internal/'+p.pkg.split('/')[1]:p.top;}
const withpc=ng.filter(p=>p.code_files>0);
tot.pkgs_with_pkg_comment=withpc.filter(p=>p.doc).length;tot.pkgs_with_code=withpc.length;
tot.top10_by_loc=ng.sort((a,b)=>b.code_loc-a.code_loc).slice(0,10).map(p=>p.pkg+' '+p.code_loc);
tot.top10_files=ng.map(p=>({p:p.pkg,...p.maxfile})).sort((a,b)=>b.l-a.l).slice(0,10).map(x=>x.p+'/'+x.f+' '+x.l);
tot.top_fanin=ng.sort((a,b)=>b.in.size-a.in.size).slice(0,8).map(p=>p.pkg+' '+p.in.size);
tot.top_fanout=ng.sort((a,b)=>b.out.size-a.out.size).slice(0,8).map(p=>p.pkg+' '+p.out.size);
tot.test_to_code=(tot.test_loc/tot.code_loc).toFixed(2);
tot.no_tests=ng.filter(p=>p.code_files>0&&p.test_files==0&&p.code_loc>30).map(p=>p.pkg+' '+p.code_loc);
writeFileSync(D+'go_structure_summary.json',JSON.stringify(tot,null,1));
console.log(JSON.stringify(tot,null,1));
