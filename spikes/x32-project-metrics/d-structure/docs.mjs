import {readFileSync,readdirSync,statSync,writeFileSync} from 'fs';
import {join,basename} from 'path';
import {execSync} from 'child_process';
const R='../../..', D='/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/d/';
const walk=d=>readdirSync(d).flatMap(f=>{const p=join(d,f);return statSync(p).isDirectory()?walk(p):[p]});
const words=t=>(t.match(/\S+/g)||[]).length;
const rows=['area,files,lines,words,h2plus_sections'];
for(const a of readdirSync(R+'/docs/spec').sort()){
 const p=join(R,'docs/spec',a);
 const fs=statSync(p).isDirectory()?walk(p).filter(f=>f.endsWith('.md')):[p];
 let l=0,w=0,s=0;for(const f of fs){const t=readFileSync(f,'utf8');l+=t.split('\n').length;w+=words(t);s+=(t.match(/^##+ /gm)||[]).length}
 rows.push([basename(a,'.md'),fs.length,l,w,s].join(','));
}
writeFileSync(D+'docs_by_area.csv',rows.join('\n')+'\n');
const dirs=['spec','requirements','threats','tests','spikes','research','briefs','versions','agents'];
const r2=['dir,files,words'];
for(const d of dirs){const fs=walk(R+'/docs/'+d).filter(f=>/\.mdx?$/.test(f));r2.push([d,fs.length,fs.reduce((s,f)=>s+words(readFileSync(f,'utf8')),0)].join(','))}
writeFileSync(D+'docs_by_dir.csv',r2.join('\n')+'\n');
// counts
const defs=f=>[...readFileSync(R+'/docs/'+f,'utf8').matchAll(/^- \*\*([A-Z]+\d+-[a-z0-9-]+)/gm)].map(m=>m[1]);
const out={};
for(const [k,f] of [['FR','requirements/FR00-index.md'],['NFR','requirements/NFR00-index.md'],['SEC','requirements/SEC00-index.md'],['T','threats/T00-index.md'],['X','spikes/X00-index.md']]) out[k]=defs(f);
const tbl=f=>[...readFileSync(R+'/docs/'+f,'utf8').matchAll(/^\| ((?:[A-Z]+\d+)-[a-z0-9-]+) \| (.*?) \| (.*) \|$/gm)].map(m=>({id:m[1],desc:m[2],status:m[3]}));
out.C=tbl('tests/C00-index.md');out.R=tbl('research/R00-index.md');out.B=tbl('briefs/B00-index.md');out.Xtbl=tbl('spikes/X00-index.md');
const dec=t=>(t.match(/Decided /g)||[]).length;
const spk={total:out.Xtbl.length,answered:out.Xtbl.filter(x=>/Answered/.test(x.status)).length,decided_rows:out.Xtbl.filter(x=>/Decided/.test(x.status)).length,decided_mentions:out.Xtbl.reduce((s,x)=>s+dec(x.status),0),open:out.Xtbl.filter(x=>!/Answered/.test(x.status)).map(x=>x.id+': '+x.status.slice(0,40))};
const brief={total:out.B.length,answered_decided:out.B.filter(x=>/Decided/.test(x.status)).length,answered_only:out.B.filter(x=>/Answered/.test(x.status)&&!/Decided/.test(x.status)).length,other:out.B.filter(x=>!/Answered|Decided/.test(x.status)).map(x=>x.id+': '+x.status.slice(0,50))};
const v1=readFileSync(R+'/docs/versions/V1-initial.md','utf8');
const decs=[...v1.slice(v1.indexOf('## Decisions'),v1.indexOf('## Milestones')).matchAll(/^- \*\*/gm)].length;
const sum={counts:{FR:out.FR.length,NFR:out.NFR.length,SEC:out.SEC.length,T:out.T.length,C_pages:out.C.length,X:out.X.length,R:out.R.length,B:out.B.length},spikes:spk,briefs:brief,V1_decisions:decs};
writeFileSync(D+'doc_counts.json',JSON.stringify(sum,null,1));
writeFileSync(D+'ids.json',JSON.stringify({FR:out.FR,NFR:out.NFR,SEC:out.SEC,T:out.T,C:out.C.map(c=>c.id)},null,1));
console.log(JSON.stringify(sum,null,1));
