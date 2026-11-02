import {readFileSync,readdirSync,statSync,writeFileSync} from 'fs';
import {join} from 'path';
const R='../../..', D='/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/d/';
const ids=JSON.parse(readFileSync(D+'ids.json','utf8'));
const walk=(d)=>{let o=[];for(const f of readdirSync(d)){if(['node_modules','.build','.git','dist','.astro'].includes(f))continue;const p=join(d,f);let s;try{s=statSync(p)}catch{continue}if(s.isDirectory())o=o.concat(walk(p));else o.push(p)}return o};
const goAll=walk(R+'/packages/wraithbox-go').filter(f=>f.endsWith('.go'));
const swAll=walk(R+'/packages/wraithbox-swift').filter(f=>f.endsWith('.swift'));
const isGen=f=>/\.pb\.go$/.test(f)||/\.pb\.swift$|\.grpc\.swift$/.test(f);
const isTest=f=>/_test\.go$/.test(f)||/\/Tests\//.test(f);
const docs=walk(R+'/docs').filter(f=>/\.mdx?$/.test(f));
const other=[...walk(R+'/scripts'),...walk(R+'/proto'),...walk(R+'/.github'),R+'/AGENTS.md',R+'/README.md'].filter(f=>/\.(md|mjs|sh|proto|yml|yaml|go|ts)$/.test(f));
const defFile={FR:'FR00',NFR:'NFR00',SEC:'SEC00',T:'T00',C:'C00'};
const src={code:[...goAll,...swAll].filter(f=>!isGen(f)&&!isTest(f)),test:[...goAll,...swAll].filter(f=>isTest(f)&&!isGen(f)),docs,other};
const cnt={};
const kindRe=/\b(SEC|NFR|FR|T|C)(\d{2})\b/g;
for(const [k,fs] of Object.entries(src)){cnt[k]={};for(const f of fs){const t=readFileSync(f,'utf8');
 const own=Object.values(defFile).some(d=>f.includes('/'+d+'-'));
 for(const m of t.matchAll(kindRe)){const key=m[1]+m[2];
  // skip definitions: in docs, skip the defining index file for requirement kinds
  if(k==='docs'&&f.includes('/'+defFile[m[1]]+'-index'))continue;
  cnt[k][key]=(cnt[k][key]||0)+1}}}
// scope
const scope={};
for(const [kind,f] of [['FR','FR00-index.md'],['NFR','NFR00-index.md'],['SEC','SEC00-index.md']]){
 for(const m of readFileSync(R+'/docs/requirements/'+f,'utf8').matchAll(/^\| ((?:N?FR|SEC)\d+-[a-z0-9-]+) \| .*? \| (.*) \|$/gm)) scope[m[1].match(/^[A-Z]+\d+/)[0]]=m[2];
}
const rows=['kind,id,full_id,code_refs,test_refs,doc_refs,traced_code_or_test,v1_milestones_or_status'];
const sum={};
for(const kind of ['FR','NFR','SEC','T','C']){
 for(const full of ids[kind]||ids[kind]){}
}
for(const kind of ['FR','NFR','SEC','T','C']){
 const list=ids[kind];sum[kind]={total:list.length,code:0,test:0,either:0,untraced:[],untested:[]};
 for(const full of list){const id=full.match(/^[A-Z]+\d+/)[0];
  const c=cnt.code[id]||0,t=cnt.test[id]||0,d=cnt.docs[id]||0;
  rows.push([kind,id,full,c,t,d,(c||t)?1:0,'"'+(scope[id]||'').replace(/"/g,'')+'"'].join(','));
  if(c)sum[kind].code++;if(t)sum[kind].test++;if(c||t)sum[kind].either++;else sum[kind].untraced.push(full+(scope[id]?' ['+scope[id]+']':''));
  if(!t)sum[kind].untested.push(full);
 }}
writeFileSync(D+'traceability.csv',rows.join('\n')+'\n');
writeFileSync(D+'traceability_summary.json',JSON.stringify(sum,null,1));
for(const k in sum)console.log(k,sum[k].total,'code',sum[k].code,'test',sum[k].test,'either',sum[k].either,'\n untraced:',sum[k].untraced.join('; '));
