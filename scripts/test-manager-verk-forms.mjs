import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
import {randomUUID} from 'node:crypto';

class Element {
  constructor(tag, text, cls) { this.tagName = tag; this.textContent = text || ''; this.className = cls || ''; this.children = []; this.value = ''; this.disabled = false; this.checked = false; this.attributes = {}; }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  setAttribute(name, value) { this.attributes[name] = value; }
  click() { return this.onclick?.(); }
}
const errors = [], deviceRequests = [];
let deviceReply = [];
const settle = () => new Promise(resolve => setImmediate(resolve));
const context = vm.createContext({state:{logged:true}, api:async path => { deviceRequests.push(path); if(deviceReply instanceof Error) throw deviceReply; return typeof deviceReply==='function' ? await deviceReply() : deviceReply; }, crypto: {randomUUID}, node: (tag, text, cls) => new Element(tag, text, cls), $: () => null, action: async (control, callback) => { if (control.disabled) return; try { return await callback(); } catch (error) { errors.push(error.message); } }, console});
const run = source => vm.runInContext(source, context);
const formsSource = fs.readFileSync('internal/manager/web/verk_forms.js', 'utf8');
vm.runInContext(formsSource, context, {filename: fs.realpathSync('internal/manager/web/verk_forms.js')});
vm.runInContext(fs.readFileSync('internal/manager/web/verk.js', 'utf8'), context, {filename: fs.realpathSync('internal/manager/web/verk.js')});
run("verkState.selected={private:true};verkState.workflow=verkNewWorkflow('private:forms');verkState.node='entry'");
const clone = value => JSON.parse(JSON.stringify(value));
const tree = root => [root, ...root.children.flatMap(tree)];
const fields = (label, root = context.root) => tree(root).filter(element => element.tagName === 'label' && element.children[0]?.textContent === label).map(element => element.children[1]);
const field = (label, root = context.root) => { const result = fields(label, root)[0]; assert(result, 'Missing field ' + label); return result; };
const buttons = (label, root = context.root) => tree(root).filter(element => element.tagName === 'button' && element.textContent === label);
const button = (label, root = context.root) => { const result = buttons(label, root)[0]; assert(result, 'Missing button ' + label); return result; };
const change = async (label, value, root = context.root) => { const control = field(label, root); control.value = value; await control.onchange?.(); };
const check = async (label, value) => { const control = field(label); control.checked = value; await control.onchange?.(); };
const mount = expression => { run('globalThis.root=node("div");globalThis.widget=' + expression); return context.root; };
const read = () => clone(run('widget.read()'));

mount("verkValueList(root,'Variabili locali',[{name:'count',type:'number',value:3},{name:'flag',type:'boolean',value:false}])");
assert.deepEqual(read(), [{name:'count',type:'number',value:3},{name:'flag',type:'boolean',value:false}]);
field('Valore').value='Infinity';assert.throws(read,/finito/);field('Valore').value='4';
await change('Tipo','integer');field('Valore').value='1.5';assert.throws(read,/intero/);field('Valore').value='4';
await button('Aggiungi variabile').click();assert.equal(read().length,3);
const duplicate=fields('Nome')[2];duplicate.value='count';assert.throws(read,/univoci/);duplicate.value='extra';
const objectType=fields('Tipo')[2];objectType.value='object';await objectType.onchange();
fields('Valore')[2].value='{"key":"value"}';assert.deepEqual(read()[2].value,{key:'value'});
fields('Valore')[2].value='[]';assert.throws(read,/object/);fields('Valore')[2].value='{}';
await buttons('Rimuovi valore')[2].click();assert.equal(read().length,2);
await change('Tipo','array');field('Valore').value='{"x":1}';assert.throws(read,/array/);field('Valore').value='[1,"x"]';assert.deepEqual(read()[0].value,[1,'x']);
await change('Tipo','null');assert.equal(read()[0].value,null);assert.equal(field('Valore').disabled,true);
assert.throws(()=>run("verkFormParse('boolean','yes')"),/booleano/);
assert.throws(()=>run("verkFormParse('number','')"),/finito/);
mount("verkValueList(root,'Input iniziali',[])");await button('Aggiungi input').click();assert.equal(read()[0].type,'string');

mount("verkSchemaForm(root,{type:'object',additionalProperties:false,properties:{score:{type:'number',minimum:0}},required:['score']})");
assert.equal(fields('Nome campo output')[0].value,'final_message');assert.equal(fields('Nome campo output')[0].disabled,true);assert.equal(fields('Tipo campo output')[0].disabled,true);assert.equal(fields('Obbligatorio')[0].disabled,true);
assert.equal(buttons('Rimuovi campo output').length,1);
fields('Descrizione campo')[0].value='<img src=x onerror=alert(1)>';
assert.equal(read().properties.score.minimum,0);assert.equal(read().additionalProperties,false);assert(read().required.includes('final_message'));
await button('Aggiungi campo output').click();fields('Nome campo output')[2].value='passed';fields('Tipo campo output')[2].value='boolean';fields('Obbligatorio')[2].checked=true;
assert.deepEqual(read().required,['final_message','score','passed']);assert.equal(read().properties.passed.type,'boolean');
await buttons('Rimuovi campo output')[0].click();assert.equal(read().properties.score,undefined);
assert.equal(tree(context.root).some(element=>element.tagName==='img'),false);
mount("verkSchemaForm(root,{type:'object',properties:{nullable:{type:['string','null']}}})");assert.deepEqual(read().properties.nullable.type,['string','null']);
mount("verkSchemaForm(root,{allOf:[{type:'object'}]})");assert.deepEqual(read(),{allOf:[{type:'object'}]});
mount("verkSchemaForm(root,false)");assert.equal(read(),false);
mount("verkSchemaForm(root,{type:'object',properties:{forbidden:false}})");assert.equal(read().properties.forbidden,false);
mount("verkSchemaForm(root,{type:'object',properties:{any:{enum:[1,'x']}}})");assert.equal(read().properties.any.type,undefined);assert.deepEqual(read().properties.any.enum,[1,'x']);

run("verkGraph().input_values=[{name:'count',type:'number',value:2}];verkGraph().variables=[{name:'active',type:'boolean',value:true}];verkGraph().nodes.push({...verkNewNode('lid','upstream'),output_schema:{type:'object',properties:{score:{type:'number'}}}},{...verkNewNode('if','gate')});verkGraph().edges=[{from:'upstream',to:'gate',port:'done'}];verkState.node='gate'");
mount("verkOperandForm(root,{mode:'literal',type:'number',value:1},'Test')");
await change('Test: tipo','boolean');await change('Test: valore','true');assert.deepEqual(read(),{mode:'literal',type:'boolean',value:true});
await change('Test: sorgente','ref');assert.throws(read,/percorso/);
const referenceOptions=field('Test: scegli riferimento').children.map(option=>option.value);assert(referenceOptions.includes('inputs.count'));assert(referenceOptions.includes('variables.active'));assert(referenceOptions.includes('nodes.upstream.score'));assert(!referenceOptions.some(value=>value.startsWith('nodes.gate.')));
await change('Test: scegli riferimento','nodes.upstream.score');assert.equal(read().value,'nodes.upstream.score');
await change('Test: sorgente','literal');assert.equal(read().value,0);

mount("verkRuleForm(root,{kind:'group',op:'AND',children:[verkDefaultRule()]})");
for(const op of ['OR','NOT','XOR','NAND','NOR','XNOR','ONE','AND']){await change('Operatore gruppo',op);assert.equal(read().op,op);}
await button('Aggiungi gruppo').click();assert.equal(read().children[1].kind,'group');
await change('Operatore gruppo','NOT');assert.match(errors.at(-1),/esattamente/);assert.equal(read().op,'AND');
await buttons('Rimuovi regola').at(-1).click();
assert.equal(read().children.length,1);
await button('Aggiungi condizione').click();assert.equal(read().children.length,2);
await buttons('Rimuovi regola').at(-1).click();await change('Operatore gruppo','NOT');assert.equal(button('Aggiungi condizione').disabled,true);
mount("verkRuleForm(root,verkDefaultRule())");
await change('Confronto','exists');assert.equal(read().right,undefined);assert.equal(fields('Destra: valore').length,0);
await change('Confronto','neq');assert.equal(read().right.type,'number');
await button('Raggruppa condizione').click();assert.equal(read().kind,'group');
assert.throws(()=>mount("verkRuleForm(root,verkDefaultRule(),33)"),/32 livelli/);

mount("verkBranchForm(root,{branches:[{id:'first',label:'First',rule:verkDefaultRule()},{id:'second',label:'Second',rule:verkDefaultRule()}],default_id:'otherwise',outputs:[{id:'otherwise',label:'Fallback'},{id:'invalid',label:'Bad data'}]})");
fields('Nome ramo')[0].value='First edited';await buttons('Sposta dopo')[0].click();assert.deepEqual(read().branches.map(branch=>branch.id),['second','first']);assert.equal(read().branches[1].label,'First edited');
await buttons('Sposta prima')[1].click();assert.equal(read().branches[0].id,'first');
await button('Aggiungi ramo').click();assert.equal(read().branches.length,3);await buttons('Elimina ramo')[2].click();assert.equal(read().branches.length,2);
field('Nome ramo predefinito').value='Otherwise renamed';assert.equal(read().outputs.at(-2).label,'Otherwise renamed');assert.equal(read().default_id,'otherwise');
await buttons('Elimina ramo')[0].click();await button('Elimina ramo').click();assert.throws(read,/almeno un ramo/);

mount("verkChoiceForm(root,{type:'ai',params:{choices:[{id:'yes',label:'Yes'},{id:'no',label:'No'}]},outputs:[]})");
field('Nome alternativa').value='Approve';assert.equal(read().outputs[0].label,'Approve');assert.equal(read().values[0].id,'yes');
await button('Aggiungi alternativa').click();assert.equal(read().values.length,3);await buttons('Rimuovi alternativa')[2].click();await buttons('Rimuovi alternativa')[1].click();assert.throws(read,/due alternative/);
mount("verkChoiceForm(root,{type:'switch',params:{cases:[{id:'null_case',value:null}]},outputs:[],default_id:'fallback'})");assert.equal(read().values[0].value,null);
await change('Tipo del caso','number');field('Valore del caso').value='7';assert.equal(read().values[0].value,7);
await button('Aggiungi alternativa').click();assert.equal(read().values.length,2);assert.equal(read().default_id,'fallback');
mount("verkPortLabels(root,[{id:'done',label:'Done'}])");field('Nome uscita · done').value='Result';assert.equal(read()[0].label,'Result');field('Nome uscita · done').value=' ';assert.throws(read,/nome/);

mount("verkTriggerForm(root,[{id:'owner',type:'smidr'}])");
for(const label of ['Push Git','Pull request','CI terminata','Pianificazione','Webhook'])await check(label,true);
assert.equal(read().length,6);
fields('Repository / organizzazioni · separati da virgola')[0].value='studio/a, studio/b';fields('Ambito repository')[0].value='list';fields('Branch esatti · separati da virgola')[0].value='main, release';
field('Azioni PR · separate da virgola').value='opened, synchronize';field('Esiti CI · separati da virgola').value='success';field('Cron · cinque campi').value='0 9 * * 1-5';field('Fuso orario IANA').value='Europe/Rome';field('Sorgente webhook registrata').value='catalog:updated';
assert.deepEqual(read().find(trigger=>trigger.type==='push').repositories,['studio/a','studio/b']);assert.equal(read().find(trigger=>trigger.type==='schedule').timezone,'Europe/Rome');
await check('Push Git',false);assert.equal(read().length,5);assert.equal(read().find(trigger=>trigger.type==='webhook').source_ref,'catalog:updated');
mount("verkExitForm(root,null)");assert.equal(read().notify_smidr,false);await check('Notifica Smidr',true);await check('Richiedi intervento',true);await change('Stato task','blocked');assert.deepEqual(read(),{set_state:true,state:'blocked',return_data:true,notify_smidr:true,intervention:true});

run("globalThis.item=verkNewNode('repeat','repeat');globalThis.root=node('div');globalThis.widget=verkNodeForms(root,item)");
await check('Usa condizione logica',true);run('widget.apply(item)');assert.equal(run('item.params.condition.kind'),'condition');await check('Usa condizione logica',false);run('widget.apply(item)');assert.equal(run('item.params.condition'),undefined);
for(const type of ['if','ai','switch','lid','entry','end']){run('globalThis.item=verkNewNode('+JSON.stringify(type)+',"test");globalThis.root=node("div");globalThis.widget=verkNodeForms(root,item);widget.apply(item)');assert.equal(run('item.type'),type);if(type==='lid')assert(run('item.output_schema.required.includes("final_message")'));}
run('verkState.selected.private=false');
mount("verkNodeForms(root,verkNewNode('if','readonly'))");
assert(tree(context.root).filter(element=>['input','select','button'].includes(element.tagName)).every(element=>element.disabled));
const count=tree(context.root).length;await button('Aggiungi ramo').click();assert.equal(tree(context.root).length,count);
mount("verkValueList(root,'Variabili locali',[{name:'x',type:'string',value:'private instructions'}])");assert(field('Nome').disabled);assert(field('Valore').disabled);
run('verkState.selected.private=true');
deviceReply=[{id:'laptop',name:'Laptop di Luca',online:true},{id:'desktop',name:'Studio',online:false},{id:'revoked',name:'Client precedente',revoked:true},{id:'desktop',name:'Duplicate ignored',online:true}];
mount("verkToolClientForm(root,'desktop')");
assert.equal(read(),'desktop');await settle();
assert(deviceRequests.every(path=>path==='devices'));
assert(field('Esegui sul dispositivo').children.some(option=>option.value==='desktop'&&option.textContent==='Studio · Offline'));
assert(field('Esegui sul dispositivo').children.some(option=>option.value==='laptop'&&option.textContent==='Laptop di Luca · Online'));
assert.equal(field('Esegui sul dispositivo').children.filter(option=>option.value==='desktop').length,1);
assert.equal(read(),'desktop');
await change('Esegui sul dispositivo','manager');assert.equal(read(),'manager');
await change('Esegui sul dispositivo','');assert.equal(read(),'');
mount("verkToolClientForm(root,'missing-client')");await settle();assert.equal(read(),'missing-client');assert(field('Esegui sul dispositivo').children.some(option=>option.textContent==='missing-client · non disponibile'));
deviceReply=[];mount("verkToolClientForm(root,'')");await settle();assert.equal(read(),'');assert(tree(context.root).some(element=>element.textContent.includes('Nessun client registrato')));
deviceReply=new Error('offline');mount("verkToolClientForm(root,'desktop')");await settle();assert.equal(read(),'desktop');assert(tree(context.root).some(element=>element.textContent.includes('La selezione è conservata')));
deviceReply={};mount("verkToolClientForm(root,'')");await settle();assert(tree(context.root).some(element=>element.textContent.includes('Risposta inventario non valida')));
let release;deviceReply=()=>new Promise(resolve=>{release=resolve;});mount("verkToolClientForm(root,'desktop')");await change('Esegui sul dispositivo','manager');release([{id:'desktop',name:'Studio',online:false}]);await settle();assert.equal(read(),'manager');
deviceReply=()=>new Promise(resolve=>{release=resolve;});mount("verkToolClientForm(root,'')");context.state.logged=false;release([{id:'late',name:'Late private inventory',online:true}]);await settle();assert(!tree(context.root).some(element=>element.textContent.includes('Late private')));context.state.logged=true;
deviceReply=()=>new Promise(resolve=>{release=resolve;});mount("verkToolClientForm(root,'')");run('verkState.workflow=verkClone(verkState.workflow)');release([{id:'stale',name:'Stale inventory'}]);await settle();assert(!tree(context.root).some(element=>element.textContent.includes('Stale inventory')));
deviceReply=[];
run("globalThis.item=verkNewNode('tool','tool-target');item.params.client_id='offline-client';globalThis.root=node('div');globalThis.widget=verkNodeForms(root,item)");
await settle();run('widget.apply(item)');assert.equal(run('item.params.client_id'),'offline-client');
await change('Esegui sul dispositivo','manager');run('widget.apply(item)');assert.equal(run('item.params.client_id'),'manager');
await change('Esegui sul dispositivo','');run('widget.apply(item)');assert.equal(run('Object.hasOwn(item.params,"client_id")'),false);
run('verkState.selected.private=false');mount("verkToolClientForm(root,'manager')");await settle();assert.equal(field('Esegui sul dispositivo').disabled,true);
assert.doesNotMatch(formsSource,/innerHTML|eval\(|new Function|localStorage|sessionStorage/);
console.log('Structured Verk forms: nested rules, ordered branches, typed contracts and variables, triggers, exit actions and readonly behavior passed.');
