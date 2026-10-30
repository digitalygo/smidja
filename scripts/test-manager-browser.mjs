import { chromium } from 'playwright';
import fs from 'node:fs';
const source=fs.readFileSync('internal/manager/ui.go','utf8');
const html=source.split('const indexHTML = `')[1].split('`')[0];
(async()=>{
const browser=await chromium.launch({headless:true,chromiumSandbox:true});
const page=await browser.newPage();let errors=[];page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(10000);page.on('dialog',dialog=>dialog.accept());
let session={id:'s1',title:'Example <img src=x>',messages:[{role:'user',content:'<script>alert(1)</script>'}],turn:null};let reviewStarts=[];let reviewTask=null;let writes=[];let saveConflict=true;let pendingApproval=true;let cancelledTask=false;
await page.route('**/*',async route=>{const r=route.request();if(!r.url().startsWith('http://smidja.test/'))throw Error('Unexpected external request '+r.url());const path=new URL(r.url()).pathname;let value={};if(path==='/')return route.fulfill({contentType:'text/html',body:html});
if(path==='/api/login')value={csrf:'csrf-test'};
else if(path==='/api/status')value={configured:false};
else if(path==='/api/sessions')value=r.method()==='GET'?[session]:session;
else if(path==='/api/sessions/s1')value=session;
else if(path==='/api/sessions/s1/messages'){session.turn={status:'submitting'};value=session;}
else if(path==='/api/pr-review/status')value={configured:true};
else if(path==='/api/pr-review'){reviewStarts.push(r.postDataJSON());reviewTask={id:'pr-task',workflow:{id:'smidja.pr-review/review'},status:'queued',phase_index:0,phases:[{phase:{name:'Context'},status:'queued',attempts:[]}],inputs:{base_sha:'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',head_sha:'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',verification_policy:'Read-only'}};value=reviewTask;}
else if(path==='/api/tasks/pr-task')value=reviewTask;
else if(path==='/api/devices')value=[{id:'laptop',name:'Laptop',online:false,inventory:{tools:['read'],os:'linux',arch:'arm64'}}];
else if(path==='/api/tasks')value=[{id:'t1',workflow:{id:'review'},phase_index:0,phases:[{phase:{name:'Review'},status:'blocked',attempts:[{number:1,output:'Need clarification',decision:'BLOCKED'}]}],status:'blocked'}];
else if(path==='/api/tasks/t1/cancel'){cancelledTask=true;value={status:'cancelled'};}
else if(path==='/api/tasks/t1')value={id:'t1',workflow:{id:'review'},phase_index:0,phases:[{phase:{name:'Review'},status:'blocked',attempts:[{number:1,output:'Need clarification',decision:'BLOCKED'}]}],status:'blocked'};
else if(path==='/api/tools/execute'){const b=r.postDataJSON();if(b.name==='list')value={ok:true,data:{path:'/',entries:[{name:'example.py',path:'/example.py',directory:false}]}};else if(b.name==='read')value={ok:true,data:{path:'/example.py',content:'print(1)',sha256:'abc'}};else if(b.name==='write'){writes.push(b);value=saveConflict?{ok:false,code:'conflict',error:'File changed on disk'}:{ok:true,data:{path:'/example.py',sha256:'def'}};saveConflict=false;}else value={ok:true,data:{root:'/',output:' M file\u0000'}};}
else if(path==='/api/teams')value=[{id:'remote-team',name:'Review team',origin:'envai'}];
else if(path==='/api/team-delegations')value={ok:true,data:{task_id:'native-job',target_chat_id:'target',turn_id:'native-turn'}};
else if(path==='/api/private')value=[];
else if(path==='/api/private/activation')value=[];
else if(path==='/api/approvals')value=pendingApproval?[{id:'p1',change:{action:'create',id:'private:review',kind:'team',base_revision:0,payload:{name:'Review',agents:[{name:'Manager'}]}}}]:[];
else if(path==='/api/approvals/p1/approve'){pendingApproval=false;value={id:'private:review',revision:1};}
else if(path==='/api/packages')value=[];
else if(path==='/api/packages/update-status')value={};
else if(path==='/api/workflows')value=[];
else if(path==='/api/logout')value={ok:true};else throw Error('Unexpected route '+path);
await route.fulfill({contentType:'application/json',body:JSON.stringify(value)});});
await page.goto('http://smidja.test/');await page.locator('#token').fill('long-manager-token');await page.locator('#login button').click();await page.locator('#sessions button').click();await page.waitForFunction(()=>!document.getElementById('send').disabled);if(await page.locator('#messages script').count())throw Error('Unsafe HTML rendering');
await page.locator('#message').fill('test');await page.locator('#send').click();await page.waitForFunction(()=>document.getElementById('turnState').textContent==='submitting');if(!await page.locator('#send').isDisabled())throw Error('Send must remain disabled while submitting');
session.turn={status:'completed',turn_id:'turn-1'};session.chat_id='chat-1';session.workflow_task_id='workflow-1';await page.waitForFunction(()=>document.getElementById('turnState').textContent.includes('read-only'));if(!await page.locator('#send').isDisabled())throw Error('Workflow phase can send');delete session.workflow_task_id;
await page.locator('[data-view=tasks]').click();await page.locator('#board button').click();await page.locator('#phaseOutputs').getByText('Need clarification').waitFor();if(!await page.locator('#retryTask').isDisabled())throw Error('Blocked task should not offer classifier retry');await Promise.all([page.waitForResponse(r=>r.url().endsWith('/api/tasks/t1/cancel')),page.locator('#cancelTask').click()]);if(!cancelledTask)throw Error('Cancel task was not submitted');
await page.locator('#prSession option').waitFor({state:'attached'});await page.locator('#prRepository').fill('org/repo');await page.locator('#prNumber').fill('7');await page.locator('#prFocus').fill('security');await page.locator('#prReview button').click();await page.waitForFunction(()=>document.getElementById('taskTitle').textContent.includes('pr-task'));if(reviewStarts.length!==1||reviewStarts[0].origin_session_id!=='s1'||reviewStarts[0].focus!=='security'||!reviewStarts[0].request_id)throw Error('PR review submission missing origin, focus or deduplication');await page.getByText('bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',{exact:false}).waitFor();fs.mkdirSync('test-results',{recursive:true});await page.screenshot({path:'test-results/manager-pr-review.png',fullPage:true});
await page.locator('[data-view=files]').click();await page.locator('#browse button').first().click();await page.locator('#entries button').click();await page.locator('#editor').fill('print(2)');await page.locator('#save').click();await page.getByText('conflict: File changed on disk').waitFor();if(await page.locator('#editor').inputValue()!=='print(2)')throw Error('Conflict discarded local edit');await page.locator('#save').click();await page.waitForFunction(()=>document.getElementById('saveState').textContent==='Saved');if(writes[0].args.expected_sha256!=='abc')throw Error('Missing revision check');
await page.locator('#gitStatus').click();await page.waitForFunction(()=>document.getElementById('gitOutput').textContent.includes(' M file'));
await page.locator('[data-view=teams]').click();await page.locator('#teamSession option').waitFor({state:'attached'});await page.locator('#teamPrompt').fill('Review this');await page.locator('#delegateTeam button').click();await page.waitForFunction(()=>document.getElementById('teamReceipt').textContent.includes('native-job'));
await page.locator('[data-view=configuration]').click();await page.getByText('Exact proposed change').waitFor();await page.getByRole('button',{name:'Approve exact change'}).click();await page.getByText('No pending approvals.').waitFor();await page.locator('[data-view=packages]').click();await page.getByText('No installed packages.').waitFor();
await page.setViewportSize({width:390,height:844});await page.locator('[data-view=files]').click();if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('Mobile horizontal overflow');fs.mkdirSync('test-results',{recursive:true});await page.screenshot({path:'test-results/manager-mobile.png',fullPage:true});
await page.locator('[data-view=devices]').click();await page.getByText('Laptop · Offline').waitFor();await page.locator('#logout').click();await page.locator('#login').waitFor({state:'visible'});
if(errors.length)throw Error(errors.join('\n'));await browser.close();console.log('PASS browser login/chat/read-only/XSS/tasks/files/Git/native teams/catalogs/devices/logout/mobile');
})().catch(e=>{console.error(e);process.exit(1)});
