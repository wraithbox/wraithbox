import {readFileSync,writeFileSync,readdirSync,statSync} from 'fs';
import {join} from 'path';
import {execSync} from 'child_process';
const D='/Users/lsimons/git/lsimons/lsimons-tmp/wraithbox-metrics/data/d/';
const protos=execSync('find proto -name "*.proto"',{cwd:'../../..'}).toString().trim().split('\n');
const rows=['proto,service,method,streaming,in_spec_docs,go_impl_refs,go_test_refs,swift_impl_refs'];
const sh=(c)=>{try{return +execSync(c,{cwd:'../../..',shell:'/bin/zsh'}).toString().trim()}catch{return 0}};
for(const p of protos){const t=readFileSync('../../../'+p,'utf8');
 for(const sm of t.matchAll(/service (\w+) \{([\s\S]*?)\n\}/g)){
  for(const m of sm[2].matchAll(/rpc (\w+)\((stream )?(\w+)\) returns \((stream )?(\w+)\)/g)){
   const [_,name,s1,,s2]=m;
   const spec=sh(`grep -rlw '${name}' docs | wc -l`);
   const gi=sh(`grep -rlE 'func \\(.*\\) ${name}\\(' packages/wraithbox-go --include='*.go' --exclude='*_test.go' --exclude-dir=gen | wc -l`);
   const gt=sh(`grep -rlw '${name}' packages/wraithbox-go --include='*_test.go' | wc -l`);
   const sw=sh(`grep -rlw '${name}' packages/wraithbox-swift/Sources packages/wraithbox-swift/Tests --include='*.swift' --exclude='*.pb.swift' --exclude='*.grpc.swift' | wc -l`);
   rows.push([p.replace('proto/wraithbox/',''),sm[1],name,(s1?'c':'')+(s2?'s':''),spec,gi,gt,sw].join(','))}}}
writeFileSync(D+'grpc_methods.csv',rows.join('\n')+'\n');console.log(rows.join('\n'));
