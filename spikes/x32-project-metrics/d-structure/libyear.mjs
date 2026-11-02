import {readFileSync,writeFileSync} from 'fs';
const D='/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/d/';
const gm=readFileSync('../../../packages/wraithbox-go/go.mod','utf8');
const mods=[];let inReq=false;
for(const l of gm.split('\n')){if(/^require \($/.test(l)){inReq=true;continue}if(inReq&&l==')'){inReq=false;continue}if(inReq){const m=l.trim().match(/^(\S+) (\S+)( \/\/ indirect)?/);if(m)mods.push({mod:m[1],ver:m[2],indirect:!!m[3]})}}
const esc=s=>s.replace(/[A-Z]/g,c=>'!'+c.toLowerCase());
const get=async u=>{for(let i=0;i<3;i++){try{const r=await fetch(u);if(r.ok)return await r.json()}catch{}}return null};
const rows=['module,version,kind,pinned_time,latest_version,latest_time,lag_days,libyears'];let tot=0,n=0;
for(const m of mods){const [a,b]=await Promise.all([get(`https://proxy.golang.org/${esc(m.mod)}/@v/${m.ver}.info`),get(`https://proxy.golang.org/${esc(m.mod)}/@latest`)]);
 let lag='',ly='';if(a&&b){lag=Math.max(0,(Date.parse(b.Time)-Date.parse(a.Time))/864e5);ly=(lag/365.25);tot+=ly;n++;lag=lag.toFixed(0);ly=ly.toFixed(3)}
 rows.push([m.mod,m.ver,m.indirect?'indirect':'direct',a?.Time||'',b?.Version||'',b?.Time||'',lag,ly].join(','))}
writeFileSync(D+'go_libyear.csv',rows.join('\n')+'\n');
console.log(mods.length,'modules, direct',mods.filter(m=>!m.indirect).length,'libyears total',tot.toFixed(2),'measured',n);
