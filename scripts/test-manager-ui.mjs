import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const html=fs.readFileSync('internal/manager/ui.go','utf8');
class Element {
 constructor(id=''){this.id=id;this.children=[];this.dataset={};this.value='';this.textContent='';this.disabled=false;this.hidden=false;this.classList={toggle(){}};this.events={};}
 append(...nodes){this.children.push(...nodes)}
 replaceChildren(...nodes){this.children=nodes}
 addEventListener(k,f){this.events[k]=f}
 setAttribute(){}
 get options(){return this.children}
 querySelector(){return new Element()}
}
const elements={};for(const m of html.matchAll(/id="([^"]+)"/g))elements[m[1]]=new Element(m[1]);elements.machine.value='manager';elements.path.value='/';
const document={hidden:false,getElementById:id=>elements[id],createElement:()=>new Element(),querySelectorAll:s=>s==='[data-busy]'?Object.values(elements).filter(e=>e.dataset.busy):[]};
let requests=[],response={},status=200;
const context={document,window:{addEventListener(){}},setInterval(){},confirm:()=>true,fetch:async(url,options)=>{requests.push({url,options});return{ok:status===200,status,json:async()=>typeof response==='function'?response(url):response}},console,Error,JSON,encodeURIComponent};vm.createContext(context);vm.runInContext(html.split('<script>')[1].split('</script>')[0],context);
const run=s=>vm.runInContext(s,context);
(async()=>{
run("state.logged=true;state.csrf='csrf';state.session='s1'");response={id:'s1',title:'Hello <script>',messages:[{role:'assistant',content:'<img src=x>'}],turn:{status:'submitting'}};await run('conversation()');assert.equal(elements.send.disabled,true);assert.equal(elements.messages.children[0].children[1].textContent,'<img src=x>');
response.turn={status:'paused',turn_id:'t1',reason:'offline'};await run('conversation()');assert.equal(elements.recover.disabled,false);assert.equal(elements.send.disabled,true);
response.turn.status='completed';await run('conversation()');assert.equal(elements.send.disabled,false);assert.equal(elements.recover.disabled,true);response.workflow_task_id='task-1';await run('conversation()');assert.equal(elements.send.disabled,true);assert.equal(elements.message.disabled,true);assert.match(elements.turnState.textContent,/read-only/);delete response.workflow_task_id;await run('conversation()');
response={ok:true,data:{path:'/a.py',content:'print(1)',sha256:'abc'}};await run("openFile('/a.py')");assert.equal(elements.editor.value,'print(1)');elements.editor.value='print(2)';response={ok:true,data:{sha256:'def'}};await elements.save.events.click();const body=JSON.parse(requests.at(-1).options.body);assert.equal(body.args.expected_sha256,'abc');assert.equal(body.client_id,'manager');assert.equal(elements.saveState.textContent,'Saved');
response={ok:false,code:'conflict',error:'Changed'};await elements.save.events.click();assert.match(elements.notice.textContent,/conflict/);assert.equal(elements.editor.value,'print(2)');
run("state.task='t1'");response={id:'t1',workflow:{id:'review'},status:'paused',phase_index:0,phases:[{phase:{name:'Review'},status:'paused',attempts:[{number:1,output:'Full output',output_recorded:true}]}]};await run('taskDetail()');assert.equal(elements.retryTask.disabled,false);assert.equal(elements.resumeTask.disabled,true);assert.equal(elements.phaseOutputs.children.at(-1).children.at(-1).textContent,'Full output');
response.status='blocked';response.phases[0].attempts[0].decision='BLOCKED';await run('taskDetail()');assert.equal(elements.resumeTask.disabled,false);assert.equal(elements.retryTask.disabled,true);
response=[{id:'laptop',name:'Laptop',online:false,inventory:{tools:['read'],teams:['Blender'],os:'linux',arch:'arm64'}}];await run('devices()');assert.equal(elements.deviceList.children[0].children[0].textContent,'Laptop · Offline');assert.equal(elements.machine.children[1].textContent,'Laptop (offline)');
response={ok:true,data:{path:'/',entries:[{name:'a.py',path:'/a.py'}]}};await run("browse('/')");assert.equal(elements.entries.children.length,1);assert.equal(elements.editor.disabled,true);
response={ok:true,data:{root:'/',output:' M a\u0000'}};await run("git('git_status',false)");assert.equal(elements.gitOutput.textContent,'/\n M a\n');
response=url=>url.endsWith('teams')?[{id:'team-1',name:'Reviewers',origin:'private'}]:[{id:'s1',title:'CEO',chat_id:'chat-1',turn:{turn_id:'turn-1'}},{id:'empty',title:'Empty'},{id:'phase',chat_id:'phase-chat',turn:{turn_id:'phase-turn'},workflow_task_id:'task-1'}];await run('teams()');assert.equal(elements.teamRef.children.length,1);assert.equal(elements.teamSession.children.length,1);assert.equal(elements.teamRef.children[0].value,'team-1');assert.equal(elements.teamSession.children[0].value,'s1');
elements.privateKind.value='team';
response=url=>url.includes('approvals')?[{id:'proposal',change:{action:'update',id:'private:team',kind:'team',base_revision:1,payload:{name:'New',agents:[{name:'Manager'}]}}}]:[{id:'private:team',kind:'team',revision:1,payload:{name:'Old',agents:[{name:'Manager'}]}}];
await run('configuration()');assert.equal(elements.approvals.children.length,1);assert(elements.approvals.children[0].children.some(e=>e.textContent.includes('Exact proposed change')));elements.privateEntities.children[0].onclick();assert.equal(elements.privateID.disabled,true);assert.match(elements.privatePayload.value,/Old/);assert.equal(elements.archivePrivate.disabled,false);assert.equal(elements.restorePrivate.disabled,true);
const approvalCard=elements.approvals.children[0];const approveButton=approvalCard.children.find(e=>e.textContent==='Approve exact change');const rejectButton=approvalCard.children.find(e=>e.textContent==='Reject');assert(approveButton&&rejectButton);await approveButton.onclick();assert(requests.some(r=>r.url==='/api/approvals/proposal/approve'));status=409;response={error:'stale revision'};await elements.approvals.children[0].children.find(e=>e.textContent==='Approve exact change').onclick();assert.match(elements.notice.textContent,/stale revision/);status=200;
response=url=>url.includes('update-status')?{pkg:'offline'}:url.endsWith('workflows')?[{id:'pkg/review',version:'1'}]:[{id:'pkg',version:'1',digest:'abc',source:{url:'release-source'},manifest:{teams:[]}}];await run('packages()');assert.equal(elements.packageList.children.length,1);assert(elements.packageList.children[0].children.some(e=>e.textContent==='Update error: offline'));assert.equal(elements.workflowList.children.length,1);
response={error:'expired'};status=401;await assert.rejects(()=>run("api('status')"),/expired/);assert.equal(elements.app.hidden,true);assert.equal(elements.messages.children.length,0);assert.equal(elements.editor.value,'');
assert(requests.some(r=>r.options.headers['X-CSRF']==='csrf'));console.log('PASS UI runtime: chat states/read-only, safe rendering, revision saves/conflicts, task actions, offline devices, files/Git, teams, approvals, packages, session expiry');
})().catch(e=>{console.error(e);process.exitCode=1});
