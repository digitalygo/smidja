import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';
const source = fs.readFileSync('internal/manager/web/verk.js', 'utf8');
const elements = new Map();
class Element {
  constructor(tag = 'div', text, cls = '') { this.tagName = tag; this.textContent = text || ''; this.children = []; this.className = cls; this.style = {}; this.dataset = {}; this.attributes = {}; this.value = ''; this.disabled = false; this.scrollLeft = 0; this.scrollTop = 0; this.classList = {add: value => { this.className += ' ' + value; }}; }
  set id(value) { this._id = value; elements.set(value, this); }
  get id() { return this._id; }
  append(...children) { this.children.push(...children); if (this.tagName === 'select' && !this.value && this.children.length) this.value = this.children[0].value; }
  replaceChildren(...children) { this.children = children; }
  setAttribute(name, value) { this.attributes[name] = value; }
  setPointerCapture() {}
  click() { return this.onclick?.(); }
}
for (const id of ['verkCatalog', 'verkEditor']) { const element = new Element(); element.id = id; }
const requests = [], notices = [];
let confirmation = true, promptValue = 'private:review', replies = {}, navigation = 0;
const context = vm.createContext({console, crypto: {randomUUID}, state: {logged: true}, document: {createElementNS: (_, tag) => new Element(tag), querySelector: () => ({click() { navigation++; }})},
  $: id => elements.get(id), node: (tag, text, cls) => new Element(tag, text, cls),
  confirm: () => confirmation, prompt: () => promptValue, notice: (message, error = true) => notices.push({message, error}),
  action: async (button, callback) => { if (button.disabled) return; button.disabled = true; try { return await callback(); } catch (error) { notices.push({message: error.message, error: true}); } finally { button.disabled = false; } },
  api: async (path, body) => { requests.push({path, body}); const value = replies[path]; if (value instanceof Error) throw value; return typeof value === 'function' ? await value(body) : value; }, selectTask: id => { context.selectedTask = id; }
});
const run = code => vm.runInContext(code, context, {filename: 'verk-test-actions.js'});
vm.runInContext(fs.readFileSync('internal/manager/web/verk_forms.js', 'utf8'), context, {filename: fs.realpathSync('internal/manager/web/verk_forms.js')});
vm.runInContext(source, context, {filename: fs.realpathSync('internal/manager/web/verk.js')});
const clone = value => JSON.parse(JSON.stringify(value));
const tree = root => [root, ...root.children.flatMap(tree)];
const button = (label, root = elements.get('verkEditor')) => tree(root).find(item => item.tagName === 'button' && item.textContent === label);
const field = label => { const wrapper = tree(elements.get('verkEditor')).find(item => item.tagName === 'label' && item.children[0]?.textContent === label); assert(wrapper, label); return wrapper.children[1]; };
const flush = () => new Promise(resolve => setImmediate(resolve));
const submitButton = async label => { const target=button(label); const form=tree(elements.get('verkEditor')).find(item=>item.tagName==='form'&&tree(item).includes(target)); assert(form); form.onsubmit({preventDefault(){}}); await flush(); };
assert.equal(run('verkClampZoom(0.01)'), 0.75);
assert.equal(run('verkClampZoom(8)'), 1.5);
assert.equal(run('verkMoved({x:0,y:0},{x:4,y:4})'), false);
assert.equal(run('verkMoved({x:0,y:0},{x:9,y:0})'), true);
assert.deepEqual(clone(run('verkPosition({x:10,y:10},{x:40,y:40},{x:0,y:0},1)')), {x:30,y:30});
assert.deepEqual(clone(run('verkPosition({x:10,y:10},{x:0,y:0},{x:0,y:0},1)')), {x:16,y:16});
assert.throws(() => run("verkNewNode('unknown','bad')"));
const initial = run("verkNewWorkflow('private:review')");
replies = {'workflows': [{id:'pkg/review', version:'1', phases:[{id:'first', name:'Review', team_ref:'pkg/team'}]}], 'private?kind=workflow&archived=true': [{id:'private:review',kind:'workflow',revision:1,payload:initial}], sessions: []};
await run('workflows()');
assert.equal(tree(elements.get('verkCatalog')).filter(item => item.tagName === 'article').length, 2);
await button('Mostra archiviati', elements.get('verkCatalog')).click();
await button('Nascondi archiviati', elements.get('verkCatalog')).click();
run('verkOpen(verkState.items[0]);verkState.node="entry";verkRenderInspector()');
assert.equal(field('Titolo').value, 'Ingresso');
field('Titolo').value = 'Requested <script>title</script>';
const apply = button('Applica al blocco');
await tree(elements.get('verkInspector')).find(item => item.tagName === 'form').onsubmit({preventDefault(){}});
await flush();
assert.equal(run('verkGraph().nodes[0].title'), 'Requested <script>title</script>');
assert.equal(run('verkState.dirty'), true);
assert.equal(tree(elements.get('verkViewport')).some(item => item.tagName === 'script'), false);
run('verkState.dirty=false');
const settings = button('Applica impostazioni');
await button('Aggiungi input').click();
await button('Aggiungi variabile').click();
const settingsRows=tree(elements.get('verkEditor')).filter(item=>item.className==='verk-form-row');
const inputsRow=settingsRows.find(item=>tree(item).some(child=>child.value==='valore_1'));
const settingsLabels=tree(elements.get('verkEditor')).filter(item=>item.tagName==='label'&&item.children[0]?.textContent==='Nome');
settingsLabels[0].children[1].value='input';settingsLabels[1].children[1].value='flag';
const settingsTypes=tree(elements.get('verkEditor')).filter(item=>item.tagName==='label'&&item.children[0]?.textContent==='Tipo');
settingsTypes[1].children[1].value='boolean';await settingsTypes[1].children[1].onchange();
await settings.click();
assert.equal(run('verkGraph().variables[0].value'),false);
field('Invocatori autorizzati · separati da virgola').value='invalid';await settings.click();assert.match(notices.at(-1).message,/ceo o team:/);
field('Invocatori autorizzati · separati da virgola').value='ceo';
const graphTypes = run('Object.keys(verkTypes).filter(type=>type!=="entry")');
for (const type of graphTypes) { elements.get('verkAddType').value=type; await button('Aggiungi blocco').click(); assert.equal(run('verkGraph().nodes.at(-1).type'),type); }
assert.equal(run('verkGraph().nodes.length'),13);
run('verkState.node=verkGraph().nodes[1].id;verkRenderInspector()');
field('ref').value='private:team';field('instruction').value='Review';
tree(elements.get('verkInspector')).find(item=>item.tagName==='form').onsubmit({preventDefault(){}});await flush();
assert.equal(run('verkGraph().nodes[1].params.ref'),'private:team');
replies.devices=[{id:'desktop',name:'Desktop personale',online:false}];
run('verkState.node=verkGraph().nodes.find(item=>item.type==="tool").id;verkRenderInspector()');await flush();
assert(field('Esegui sul dispositivo').children.some(option=>option.textContent==='Desktop personale · Offline'));
field('Esegui sul dispositivo').value='desktop';field('ref').value='read';
tree(elements.get('verkInspector')).find(item=>item.tagName==='form').onsubmit({preventDefault(){}});await flush();
assert.equal(run('verkGraph().nodes.find(item=>item.type==="tool").params.client_id'),'desktop');
run('verkState.node=verkGraph().nodes[1].id;verkRenderInspector()');

run('verkConnect(verkGraph(),"entry","next",verkGraph().nodes[1].id)');
assert.throws(()=>run('verkConnect(verkGraph(),"missing","next","entry")'));
assert.throws(()=>run('verkConnect(verkGraph(),"entry","next","entry")'));
assert.throws(()=>run('verkRemoveNode(verkGraph(),"entry")'));
run('verkConnect(verkGraph(),"entry","next",verkGraph().nodes[2].id)');
assert.equal(run('verkGraph().edges.length'),1);
run('verkRenderCanvas()');
const viewport=elements.get('verkViewport');viewport.scrollLeft=130;viewport.scrollTop=160;
const nodeCard=tree(viewport).find(item=>item.dataset.nodeId===run('verkGraph().nodes[1].id'));
const title=tree(nodeCard).find(item=>item.className==='verk-node-title');
const originalSelection=run('verkState.node');
const originalPosition=run('({x:verkGraph().nodes[1].x,y:verkGraph().nodes[1].y})');
title.onpointerdown({button:0,clientX:10,clientY:10,pointerId:1});
title.onpointermove({clientX:12,clientY:12});assert.equal(run('verkGraph().nodes[1].x'),originalPosition.x);
title.onpointermove({clientX:60,clientY:70});title.onpointerup();title.onclick();
assert.equal(run('verkGraph().nodes[1].x'),originalPosition.x+50);
assert.equal(run('verkState.node'),originalSelection);
assert.equal(viewport.scrollLeft,130);assert.equal(viewport.scrollTop,160);
run('verkState.node=null');
const currentTitle=tree(viewport).find(item=>item.className==='verk-node-title');currentTitle.onclick();assert.equal(run('verkState.node'),'entry');
const sourcePort=tree(viewport).find(item=>item.attributes['aria-label']?.startsWith('Uscita Requested'));
await sourcePort.click();assert.equal(run('verkState.pending.port'),'next');
run('verkInput(verkGraph().nodes[1].id)');assert.equal(run('verkState.pending'),null);assert.equal(run('verkGraph().edges[0].to'),run('verkGraph().nodes[1].id'));
run('verkState.node="entry";verkRenderInspector()');
await button('Rimuovi').click();assert.equal(run('verkGraph().edges.length'),0);
run('verkState.node=verkGraph().nodes[1].id;verkRenderInspector()');
confirmation=false;await button('Elimina blocco').click();assert.equal(run('verkGraph().nodes.length'),13);
confirmation=true;await button('Elimina blocco').click();assert.equal(run('verkGraph().nodes.length'),12);
run('verkZoom(-9)');assert.equal(run('verkState.zoom'),0.75);
await button('100%').click();assert.equal(run('verkState.zoom'),1);context.innerWidth=390;run('verkZoom(-9)');assert.equal(run('verkState.zoom'),1);context.innerWidth=1200;
replies['workflows/validate']={valid:true};await button('Verifica eseguibilità').click();assert.equal(requests.at(-1).path,'workflows/validate');
replies.private=body=>({id:body.id,kind:'workflow',revision:2,payload:body.payload});
confirmation=false;const beforeSave=requests.length;await button('Salva definizione').click();assert.equal(requests.length,beforeSave);
confirmation=true;await button('Salva definizione').click();assert.equal(run('verkState.selected.revision'),2);assert.equal(run('verkState.dirty'),false);
assert.equal(requests.findLast(item=>item.path==='private').body.base_revision,1);
run('verkState.dirty=true');replies.private=new Error('stale revision');await button('Salva definizione').click();assert.match(notices.at(-1).message,/stale revision/);assert.equal(run('verkState.dirty'),true);
await submitButton('Avvia Verk');assert.match(notices.at(-1).message,/Salva/);
run('verkState.dirty=false');replies.tasks=new Error('connection lost');await submitButton('Avvia Verk');const uncertain=requests.findLast(item=>item.path==='tasks').body.invocation_key;replies.tasks={id:'task-real'};await submitButton('Avvia Verk');assert.equal(requests.findLast(item=>item.path==='tasks').body.invocation_key,uncertain);assert.equal(context.selectedTask,'task-real');assert.equal(navigation,1);
const startRequest=requests.findLast(item=>item.path==='tasks');assert.equal(startRequest.body.workflow.id,'private:review');assert.deepEqual(clone(startRequest.body.inputs),{});
replies.private=body=>({id:body.id,kind:'workflow',revision:3,payload:run('verkState.workflow'),archived_at:'2026-11-05'});
await button('Archivia').click();assert.equal(run('verkReadonly()'),true);assert.equal(button('Avvia Verk').disabled,true);
promptValue='private:copy';await button('Copia privata').click();assert.equal(run('verkState.selected.revision'),0);assert.equal(run('verkState.workflow.id'),'private:copy');
replies.private=body=>({id:body.id,kind:'workflow',revision:1,payload:body.payload});await button('Salva definizione').click();assert.equal(requests.findLast(item=>item.path==='private').body.action,'create');
run('verkState.dirty=false;verkOpen(verkState.items.find(item=>!item.private))');
assert.equal(run('verkReadonly()'),true);assert.equal(field('Definizione sequenziale').disabled,true);
promptValue='private:sequence';await button('Copia privata').click();field('Definizione sequenziale').value=JSON.stringify({id:'ignored',version:'2',phases:[]});await button('Applica definizione').click();assert.equal(run('verkState.workflow.id'),'private:sequence');
confirmation=false;const oldID=run('verkState.workflow.id');run('verkCreate()');assert.equal(run('verkState.workflow.id'),oldID);
confirmation=true;promptValue=null;run('verkCreate()');assert.equal(run('verkState.workflow.id'),oldID);
promptValue='invalid';assert.throws(()=>run('verkCreate()'));
promptValue='private:new';run('verkCreate()');assert.equal(run('verkGraph().nodes.length'),1);assert.equal(run('verkState.node'),'entry');
run('verkReset()');assert.equal(run('verkState.workflow'),null);assert.equal(elements.get('verkEditor').children.length,0);
context.state.logged=false;await run('workflows()');assert.equal(run('verkState.items.length'),0);
const css=fs.readFileSync('internal/manager/web/verk.css','utf8');assert.match(css,/min-height:60px/);assert.match(css,/overflow:auto/);assert.match(css,/touch-action:none/);
assert.doesNotMatch(source,/innerHTML|eval\(|localStorage|sessionStorage|demoChanges/);
await import('./test-manager-verk-forms.mjs');
console.log('Verk editor: real API mappings, immutable catalog, revision conflicts, graph editing, drag threshold, viewport, and mobile sizing passed.');
