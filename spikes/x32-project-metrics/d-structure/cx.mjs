import {readFileSync,writeFileSync} from 'fs';
const D='/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/d/';
const rawj=readFileSync(D+'complexity.json','utf8');const j=JSON.parse(rawj.slice(0,rawj.lastIndexOf('}')+1));
const rows=[];
for(const i of j.Issues){const m=i.Text.match(/(?:cognitive complexity|cyclomatic complexity) (\d+) of func (\S+)/);if(!m){continue}
 rows.push({lint:i.FromLinter,c:+m[1],fn:m[2].replace(/^`|`$/g,''),file:i.Pos.Filename,line:i.Pos.Line})}
const bands=[[1,5],[6,10],[11,15],[16,999]];
const out={};
for(const l of ['gocognit','gocyclo']){const r=rows.filter(x=>x.lint===l);out[l]={n:r.length};for(const [a,b] of bands)out[l][a+'-'+(b==999?'+':b)]=r.filter(x=>x.c>=a&&x.c<=b).length;
 out[l].max=Math.max(...r.map(x=>x.c));out[l].top=r.sort((a,b)=>b.c-a.c).slice(0,10).map(x=>`${x.c} ${x.file}:${x.fn}`);
 out[l].mean=(r.reduce((s,x)=>s+x.c,0)/r.length).toFixed(2);}
writeFileSync(D+'complexity_summary.json',JSON.stringify(out,null,1));
writeFileSync(D+'complexity_functions.csv','lint,complexity,file,line,func\n'+rows.map(x=>[x.lint,x.c,x.file,x.line,x.fn].join(',')).join('\n')+'\n');
console.log(JSON.stringify(out,null,1),j.Issues.length,rows.length);
