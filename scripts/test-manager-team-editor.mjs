import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const elements=new Map();
class Element {
 constructor(tag,text,cls){this.tagName=tag;this.textContent=text||'';this.className=cls||'';this.children=[];this.value='';this.disabled=false;this.dataset={};}
 set id(value){this._id=value;elements.set(value,this);} get id(){return this._id;}
 append(...items){this.children.push(...items);} replaceChildren(...items){this.children=items;} setAttribute(){}
}
for(const id of ['teams','teamList']){const e=new Element('section');e.id=id;}
let confirmed=true,refreshes=0,replies={},requests=[];
const notices=[],listeners={};
const context=vm.createContext({window:{addEventListener:(name,handler)=>listeners[name]=handler},structuredClone,state:{logged:true},$:id=>elements.get(id),node:(...args)=>new Element(...args),confirm:()=>confirmed,notice:(...args)=>notices.push(args),teams:async()=>{refreshes++;},action:async(button,fn)=>{if(button.disabled)return;try{return await fn();}catch(error){notices.push([error.message]);}},api:async(path,body)=>{requests.push({path,body});const value=replies[path];if(value instanceof Error)throw value;return typeof value==='function'?await value(body):structuredClone(value);}});
const run=code=>vm.runInContext(code,context);
vm.runInContext(fs.readFileSync('internal/manager/web/team_editor.js','utf8'),context,{filename:fs.realpathSync('internal/manager/web/team_editor.js')});
const tree=e=>[e,...e.children.flatMap(tree)];
const panel=()=>elements.get('teamEditor');
const field=label=>{const e=tree(panel()).find(e=>e.tagName==='label'&&e.children[0]?.textContent===label);assert(e,label);return e.children[1];};
const button=label=>{const e=tree(panel()).find(e=>e.tagName==='button'&&e.textContent===label);assert(e,label);return e;};
const edit=(label,value)=>{const e=field(label);e.value=value;e.oninput();};
run('teamEditorNew()');
edit('ID privato','private:team');edit('Nome del Lid','Example <script>');edit('Istruzioni','Exact instructions');edit('Modello','explicit-model');edit('Strumenti · uno per riga','read\nwrite\nread');
await button('Aggiungi agente').onclick();
assert.equal(run('teamDraft.payload.agents.length'),2);
assert.throws(()=>run('teamEditorValidate(teamDraft)'),/univoco/);
run("teamDraft.payload.agents[1].name='Manager'");assert.throws(()=>run('teamEditorValidate(teamDraft)'),/univoco/);
run("teamDraft.payload.agents[1].name='Reviewer'");
assert.deepEqual(Array.from(run('teamEditorValidate(teamDraft).agents[0].tools')),['read','write']);
confirmed=false;await run('teamEditorSave()');assert.equal(requests.length,0);
confirmed=true;replies.private=body=>({id:body.id,revision:1,payload:body.payload});await run('teamEditorSave()');
assert.equal(requests[0].body.action,'create');assert.equal(requests[0].body.base_revision,0);assert.equal(requests[0].body.payload.agents[0].instructions,'Exact instructions');assert.equal(refreshes,1);assert.equal(run('teamDraft.dirty'),false);assert.equal(field('ID privato').disabled,true);
edit('Descrizione','Unsaved draft');replies.private=new Error('revision conflict');await assert.rejects(run('teamEditorSave()'),/conflict/);assert.equal(field('Descrizione').value,'Unsaved draft');assert.equal(run('teamDraft.revision'),1);assert.equal(run('teamDraft.dirty'),true);
confirmed=false;run('teamEditorNew()');assert.equal(run('teamDraft.id'),'private:team');
confirmed=true;replies['private?kind=team&archived=true']=[{id:'private:team',revision:4,payload:{name:'Reloaded',description:'server',agents:[{name:'Manager',instructions:'Owner-only text'}]}}];await run("teamEditorOpen('private:team')");assert.equal(field('Istruzioni').value,'Owner-only text');assert.equal(run('teamDraft.revision'),4);assert.equal(run('teamDraft.dirty'),false);
replies.private=body=>({id:body.id,revision:5,archived_at:'now',payload:{name:'Reloaded',agents:[{name:'Manager',instructions:'Owner-only text'}]}});await run('teamEditorLifecycle()');assert.equal(requests.at(-1).body.action,'archive');assert.equal(requests.at(-1).body.base_revision,4);assert.equal(button('Salva Lid privato').disabled,true);assert.equal(field('Istruzioni').disabled,true);
replies.private=body=>({id:body.id,revision:6,payload:{name:'Reloaded',agents:[{name:'Manager',instructions:'Owner-only text'}]}});await run('teamEditorLifecycle()');assert.equal(requests.at(-1).body.action,'restore');assert.equal(run('teamDraft.archived'),false);
let resolve;replies['private?kind=team&archived=true']=()=>new Promise(r=>resolve=r);const opening=run("teamEditorOpen('private:team')");run('teamEditorReset()');resolve([{id:'private:team',revision:8,payload:{name:'Late private data',agents:[]}}]);await opening;assert.equal(run('teamDraft'),null);assert.equal(panel().hidden,true);
run('teamEditorNew()');edit('ID privato','private:new');edit('Nome del Lid','New');let saveResolve;replies.private=()=>new Promise(r=>saveResolve=r);const saving=run('teamEditorSave()');const count=requests.length;await run('teamEditorSave()');assert.equal(requests.length,count);edit('Descrizione','Typed while saving');saveResolve({id:'private:new',revision:1,payload:{name:'New',description:'',agents:[{name:'Manager',instructions:'',model:'',tools:[]}]}});await saving;assert.equal(field('Descrizione').value,'Typed while saving');assert.equal(run('teamDraft.dirty'),true);assert.equal(run('teamDraft.revision'),1);
run('teamEditorReset()');
replies['private?kind=team&archived=true']=[{id:'private:archived',revision:7,archived_at:'now',payload:{name:'Archived <script>',agents:[{name:'Manager'}]}}];
run("teamEditorCatalogControls($('teamList'))");
const archiveButton=tree(elements.get('teamList')).find(e=>e.textContent==='Mostra Lid archiviati');await archiveButton.onclick();
const archived=tree(elements.get('teamArchived')).find(e=>e.tagName==='button');assert.equal(archived.textContent,'Archived <script> · Archiviato');await archived.onclick();assert.equal(run('teamDraft.id'),'private:archived');
confirmed=false;await run('teamEditorLifecycle()');assert.equal(run('teamDraft.archived'),true);confirmed=true;
run('teamEditorNew()');await button('Aggiungi agente').onclick();await button('Rimuovi agente').onclick();assert.equal(run('teamDraft.payload.agents.length'),1);
await button('Chiudi editor').onclick();assert.equal(run('teamDraft'),null);
replies['private?kind=team&archived=true']=[];await archiveButton.onclick();assert.equal(tree(elements.get('teamArchived')).some(e=>e.textContent==='Nessun Lid archiviato.'),true);
await assert.rejects(run("teamEditorOpen('private:missing')"),/non disponibile/);
let prevented=false;run("teamEditorNew();teamDraft.dirty=true");listeners.beforeunload({preventDefault(){prevented=true;}});assert.equal(prevented,true);run('teamEditorReset()');
assert.equal(fs.readFileSync('internal/manager/web/team_editor.js','utf8').includes('innerHTML'),false);
console.log('Lid editor: create, owner payload, validation, revision conflict, archive/restore, draft and stale response checks passed');
