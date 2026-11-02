import {readFileSync,writeFileSync} from 'fs';
const D='/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/d/';
const mod='github.com/wraithbox/wraithbox/packages/wraithbox-go/';
const pk={};let fails=[],skips=[],nopkgtests=[];
for(const l of readFileSync(D+'go_test.json','utf8').split('\n')){if(!l.trim())continue;let e;try{e=JSON.parse(l)}catch{continue}
 const p=(e.Package||'').replace(mod,'');pk[p]??={pass:0,fail:0,skip:0,out:''};
 if(e.Action==='pass'&&e.Test)pk[p].pass++;if(e.Action==='fail'&&e.Test)pk[p].fail++;if(e.Action==='skip'&&e.Test)pk[p].skip++;
 if(e.Action==='skip'&&e.Test)skips.push(p+' '+e.Test);if(e.Action==='fail'&&!e.Test)fails.push(p);
 if(e.Action==='output'&&/coverage:/.test(e.Output))pk[p].out=e.Output.trim();}
const cov={};
for(const l of readFileSync(D+'go_cover.out','utf8').split('\n').slice(1)){if(!l)continue;const m=l.match(/^(.*?):\d+\.\d+,\d+\.\d+ (\d+) (\d+)$/);if(!m)continue;const p=m[1].replace(mod,'').replace(/\/[^\/]+$/,'');cov[p]??={t:0,c:0};cov[p].t+=+m[2];if(+m[3]>0)cov[p].c+=+m[2]}
const rows=['package,statements,covered,pct,tests_pass,tests_skip,tests_fail'];let T=0,C=0;
for(const p of Object.keys(cov).sort()){const c=cov[p];if(p.startsWith('internal/gen'))continue;T+=c.t;C+=c.c;rows.push([p,c.t,c.c,(c.c/c.t*100).toFixed(1),pk[p]?.pass||0,pk[p]?.skip||0,pk[p]?.fail||0].join(','))}
const nocov=Object.keys(pk).filter(p=>p&&!cov[p]&&!p.startsWith('internal/gen'));
writeFileSync(D+'go_coverage_by_package.csv',rows.join('\n')+'\n');
writeFileSync(D+'go_test_skips.txt',skips.join('\n')+'\n');
console.log('total',T,C,(C/T*100).toFixed(1),'failed pkgs',fails,'skips',skips.length,'pkgs with tests no stmts in profile',nocov.length);
const tot={statements:T,covered:C,pct:+(C/T*100).toFixed(1),failed_packages:fails,skipped_tests:skips.length};
writeFileSync(D+'go_coverage_total.json',JSON.stringify(tot,null,1));
const low=Object.keys(cov).filter(p=>!p.startsWith('internal/gen')&&cov[p].t>=50).map(p=>[p,cov[p].c/cov[p].t*100,cov[p].t]).sort((a,b)=>a[1]-b[1]).slice(0,10);console.log(low.map(x=>x[0]+' '+x[1].toFixed(0)+'% of '+x[2]).join('\n'));
console.log(skips.join('\n'));
