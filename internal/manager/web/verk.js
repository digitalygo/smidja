'use strict';

const verkTypes = {
  entry: ['Ingresso', ['next'], []],
  lid: ['Esegui Lid', ['done', 'error'], ['ref', 'instruction', 'output']],
  tool: ['Esegui strumento', ['done', 'error'], ['ref', 'input', 'output']],
  verk: ['Esegui Verk', ['done', 'error'], ['ref', 'version', 'input', 'output']],
  data: ['Prepara dati', ['done', 'invalid'], ['mapping']],
  if: ['Condizione', ['true', 'false', 'invalid'], []],
  switch: ['Switch', ['case_1', 'default', 'invalid'], ['variable', 'cases']],
  ai: ['Decisione AI', ['choice_1', 'choice_2', 'invalid'], ['question', 'context', 'choices']],
  clarify: ['Chiedi chiarimento', ['answer', 'cancel'], ['question', 'output']],
  approve: ['Richiedi approvazione', ['approved', 'rejected', 'timeout'], ['proposal']],
  wait: ['Attendi', ['resume', 'timeout'], ['event', 'timeout']],
  repeat: ['Ripeti con limite', ['again', 'done', 'exhausted'], ['limit', 'condition']],
  end: ['Conclusione', [], ['outcome', 'result']]
};
const verkState = {items: [], selected: null, workflow: null, dirty: false, node: null, pending: null, zoom: 1, archived: false, generation: 0, submission: null};
const verkClone = value => JSON.parse(JSON.stringify(value));
const verkID = prefix => prefix + '-' + globalThis.crypto.randomUUID();
const verkReadonly = () => !verkState.selected || !verkState.selected.private || !!verkState.selected.archived_at;
const verkGraph = () => verkState.workflow && verkState.workflow.graph;

function verkNewNode(type, id, x = 48, y = 48) {
  const definition = verkTypes[type];
  if (!definition) throw Error('Tipo di blocco non supportato.');
  const item = {id, type, title: definition[0], x, y, params: {}, outputs: definition[1].map(port => ({id: port, label: port}))};
  if (type === 'repeat') item.params.limit = 3;
  if (type === 'end') item.params.outcome = 'completed';
  if (type === 'if') { item.default_id = 'false'; item.branches = [{id: 'true', label: 'Condizione vera', rule: {kind: 'condition', operator: 'exists', left: {mode: 'ref', value: 'inputs.value'}}}]; }
  if (type === 'ai') item.params.choices = [{id: 'choice_1', label: 'Alternativa 1'}, {id: 'choice_2', label: 'Alternativa 2'}];
  if (type === 'switch') { item.default_id = 'default'; item.params.cases = [{id: 'case_1', value: ''}]; }
  if (type === 'entry') item.triggers = [{id: 'smidr', type: 'smidr'}];
  return item;
}

function verkNewWorkflow(id) {
  return {schema_version: 2, id, version: '1', allowed_invokers: ['ceo'], graph: {schema_version: 1, nodes: [verkNewNode('entry', 'entry')], edges: []}};
}

function verkConnect(graph, from, port, to) {
  const source = graph.nodes.find(item => item.id === from);
  if (!source || !source.outputs.some(item => item.id === port) || !graph.nodes.some(item => item.id === to)) throw Error('Collegamento non valido.');
  if (graph.nodes.find(item => item.id === to).type === 'entry') throw Error('Il blocco ingresso non riceve collegamenti.');
  const existing = graph.edges.find(edge => edge.from === from && edge.port === port);
  if (existing) existing.to = to;
  else graph.edges.push({id: verkID('edge'), from, port, to});
}

function verkRemoveNode(graph, id) {
  if (graph.nodes.find(item => item.id === id)?.type === 'entry') throw Error('L’ingresso non può essere eliminato.');
  graph.nodes = graph.nodes.filter(item => item.id !== id);
  graph.edges = graph.edges.filter(edge => edge.from !== id && edge.to !== id);
}

function verkClampZoom(value) { return Math.max(0.75, Math.min(1.5, value)); }
function verkMoved(start, current) { return Math.hypot(current.x - start.x, current.y - start.y) >= 8; }
function verkPosition(start, origin, current, zoom) {
  return {x: Math.max(16, Math.round(origin.x + (current.x - start.x) / zoom)), y: Math.max(16, Math.round(origin.y + (current.y - start.y) / zoom))};
}

function verkMarkDirty() {
  verkState.dirty = true;
  const status = $('verkSaveState');
  if (status) status.textContent = 'Modifiche non salvate';
}

function verkButton(label, callback, className) {
  const button = node('button', label, className);
  button.type = 'button';
  button.onclick = () => action(button, callback);
  return button;
}

function verkField(parent, label, value, multiline = false) {
  const wrapper = node('label', undefined, 'verk-field');
  wrapper.append(node('span', label));
  const input = node(multiline ? 'textarea' : 'input');
  input.value = value === undefined ? '' : String(value);
  input.disabled = verkReadonly();
  wrapper.append(input);
  parent.append(wrapper);
  return input;
}

function verkJSON(parent, label, value) {
  const input = verkField(parent, label, JSON.stringify(value, null, 2), true);
  input.spellcheck = false;
  input.className = 'verk-json';
  return input;
}

async function workflows() {
  const generation = ++verkState.generation;
  const [available, records] = await Promise.all([api('workflows'), api('private?kind=workflow&archived=true')]);
  if (!state.logged || generation !== verkState.generation) return;
  const privateItems = (records || []).filter(item => item.kind === 'workflow').map(item => ({...item, private: true, workflow: item.payload}));
  const privateIDs = new Set(privateItems.map(item => item.id));
  verkState.items = [...privateItems, ...(available || []).filter(item => !privateIDs.has(item.id)).map(item => ({id: item.id, workflow: item, private: false}))];
  verkRenderCatalog();
}

function verkRenderCatalog() {
  const box = $('verkCatalog');
  box.replaceChildren();
  const bar = node('div', undefined, 'verk-toolbar');
  bar.append(verkButton('Nuovo Verk privato', verkCreate, 'primary'), verkButton(verkState.archived ? 'Nascondi archiviati' : 'Mostra archiviati', () => { verkState.archived = !verkState.archived; verkRenderCatalog(); }), verkButton('Aggiorna', workflows));
  box.append(bar);
  const cards = node('div', undefined, 'verk-catalog-grid');
  for (const item of verkState.items.filter(item => verkState.archived || !item.archived_at)) {
    const card = node('article', undefined, 'card verk-catalog-card');
    card.append(node('small', item.archived_at ? 'ARCHIVIATO' : item.private ? 'PRIVATO' : 'CATALOGO · SOLA LETTURA'), node('h3', item.id), node('p', 'Versione ' + item.workflow.version));
    const count = item.workflow.graph ? item.workflow.graph.nodes.length + ' blocchi' : (item.workflow.phases || []).length + ' fasi sequenziali';
    card.append(node('p', count, 'muted'), verkButton('Apri definizione', () => verkOpen(item)));
    cards.append(card);
  }
  if (!cards.children.length) cards.append(node('p', 'Nessun Verk disponibile. Crea una definizione privata o installa un pacchetto.', 'muted'));
  box.append(cards);
}

function verkMayDiscard() { return !verkState.dirty || confirm('Scartare le modifiche non salvate a questo Verk?'); }
function verkCreate() {
  if (!verkMayDiscard()) return;
  const id = prompt('ID del nuovo Verk privato', 'private:');
  if (id === null) return;
  if (!/^private:[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/.test(id)) throw Error('Usa private: seguito da un nome valido.');
  verkState.selected = {id, private: true, revision: 0};
  verkState.workflow = verkNewWorkflow(id);
  verkState.node = 'entry';
  verkState.pending = null;
  verkState.dirty = true;
  verkRenderEditor();
}

function verkOpen(item) {
  if (!verkMayDiscard()) return;
  verkState.selected = item;
  verkState.workflow = verkClone(item.workflow);
  verkState.node = null;
  verkState.pending = null;
  verkState.dirty = false;
  verkState.zoom = 1;
  verkRenderEditor();
}

function verkCopy() {
  const id = prompt('ID della copia privata', 'private:');
  if (id === null) return;
  if (!/^private:[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/.test(id)) throw Error('ID privato non valido.');
  verkState.selected = {id, private: true, revision: 0};
  verkState.workflow.id = id;
  verkState.dirty = true;
  verkRenderEditor();
}

async function verkSave() {
  if (verkReadonly()) return;
  if (!confirm('Salvare questa esatta definizione privata? I task già avviati mantengono la loro versione.')) return;
  const selected = verkState.selected;
  const payload = verkClone(verkState.workflow);
  const fingerprint = JSON.stringify(payload);
  const result = await api('private', {action: selected.revision ? 'update' : 'create', id: selected.id, kind: 'workflow', base_revision: selected.revision || 0, payload});
  if (!state.logged || selected !== verkState.selected) return;
  verkState.selected = {...result, private: true, workflow: result.payload};
  verkState.dirty = JSON.stringify(verkState.workflow) !== fingerprint;
  $('verkSaveState').textContent = verkState.dirty ? 'Revisione salvata; modifiche più recenti non salvate' : 'Salvato · revisione ' + result.revision;
  await workflows();
}

async function verkLifecycle() {
  const selected = verkState.selected;
  const operation = selected.archived_at ? 'restore' : 'archive';
  if (!confirm(operation === 'archive' ? 'Archiviare questo Verk? Rimane recuperabile per 30 giorni.' : 'Ripristinare questo Verk?')) return;
  const result = await api('private', {action: operation, id: selected.id, kind: 'workflow', base_revision: selected.revision});
  if (!state.logged || selected !== verkState.selected) return;
  verkState.dirty = false;
  verkOpen({...result, private: true, workflow: result.payload});
  await workflows();
}

function verkRenderEditor() {
  const box = $('verkEditor');
  box.replaceChildren();
  if (!verkState.workflow) return;
  const toolbar = node('div', undefined, 'verk-toolbar');
  toolbar.append(node('h3', verkState.workflow.id), node('span', verkReadonly() ? 'Sola lettura' : 'Definizione privata', 'muted'));
  toolbar.append(verkButton('Copia privata', verkCopy));
  if (!verkReadonly()) toolbar.append(verkButton('Salva definizione', verkSave, 'primary'));
  if (verkState.selected.private && verkState.selected.revision) toolbar.append(verkButton(verkState.selected.archived_at ? 'Ripristina' : 'Archivia', verkLifecycle));
  toolbar.append(verkButton('Verifica eseguibilità', async () => { await api('workflows/validate', {workflow: verkState.workflow}); notice('Definizione valida per l’esecuzione.', false); }));
  const status = node('span', verkState.dirty ? 'Modifiche non salvate' : 'Definizione caricata', 'muted');
  status.id = 'verkSaveState';
  toolbar.append(status);
  box.append(toolbar);
  if (!verkGraph()) {
    const list = node('ol');
    for (const phase of verkState.workflow.phases || []) list.append(node('li', phase.name + ' · ' + phase.team_ref));
    box.append(node('p', 'Questo Verk usa una sequenza di fasi. La sua definizione originale è preservata.'), list);
    const definition = verkJSON(box, 'Definizione sequenziale', verkState.workflow);
    if (!verkReadonly()) box.append(verkButton('Applica definizione', () => { verkState.workflow = JSON.parse(definition.value); verkState.workflow.id = verkState.selected.id; verkMarkDirty(); verkRenderEditor(); }));
    verkRunForm(box);
    return;
  }
  const controls = node('div', undefined, 'verk-toolbar');
  if (!verkReadonly()) {
    const type = node('select');
    type.id = 'verkAddType';
    type.setAttribute('aria-label', 'Tipo di blocco');
    for (const [key, value] of Object.entries(verkTypes).filter(([key]) => key !== 'entry')) { const option = node('option', value[0]); option.value = key; type.append(option); }
    controls.append(type, verkButton('Aggiungi blocco', () => { const graph = verkGraph(); const item = verkNewNode(type.value, verkID(type.value), 48 + graph.nodes.length % 3 * 300, 48 + Math.floor(graph.nodes.length / 3) * 260); graph.nodes.push(item); verkState.node = item.id; verkMarkDirty(); verkRenderCanvas(); verkRenderInspector(); }));
  }
  controls.append(verkButton('−', () => verkZoom(-0.1)), verkButton('+', () => verkZoom(0.1)), verkButton('100%', () => { verkState.zoom = 1; verkRenderCanvas(); }));
  const zoom = node('output', Math.round(verkState.zoom * 100) + '%'); zoom.id = 'verkZoom'; controls.append(zoom);
  const choose = node('select'); choose.id = 'verkChooseNode'; choose.setAttribute('aria-label', 'Apri blocco');
  choose.append(node('option', 'Scegli un blocco'));
  for (const item of verkGraph().nodes) { const option = node('option', item.title + ' · ' + item.id); option.value = item.id; choose.append(option); }
  choose.onchange = () => { verkState.node = choose.value; verkRenderInspector(); };
  controls.append(choose);
  box.append(controls, node('p', 'Trascina il titolo per spostare un blocco. Scegli un’uscita e poi l’ingresso del blocco destinazione per collegarli. Il canvas si può scorrere in entrambe le direzioni.', 'muted'));
  const hint = node('p', '', 'verk-connect-status'); hint.id = 'verkConnectStatus'; hint.setAttribute('role', 'status'); box.append(hint);
  const workspace = node('div', undefined, 'verk-workspace');
  const viewport = node('div', undefined, 'verk-viewport'); viewport.id = 'verkViewport'; viewport.tabIndex = 0; viewport.setAttribute('aria-label', 'Canvas Verk, scorrimento orizzontale e verticale');
  const inspector = node('aside', undefined, 'verk-inspector'); inspector.id = 'verkInspector'; inspector.setAttribute('aria-label', 'Configurazione blocco');
  workspace.append(viewport, inspector); box.append(workspace);
  verkRenderCanvas(); verkRenderInspector();
  const configuration = node('details', undefined, 'verk-settings'); configuration.append(node('summary', 'Input, variabili e permessi di invocazione'));
  const inputs = verkValueList(configuration, 'Input iniziali', verkGraph().input_values || []);
  const variables = verkValueList(configuration, 'Variabili locali', verkGraph().variables || []);
  const advanced = node('details'); advanced.append(node('summary', 'Schema input avanzato'));
  const schema = verkJSON(advanced, 'JSON Schema degli input', verkState.workflow.input_schema || {type: 'object'}); configuration.append(advanced);
  const invokers = verkField(configuration, 'Invocatori autorizzati · separati da virgola', (verkState.workflow.allowed_invokers || ['ceo']).join(', '));
  const version = verkField(configuration, 'Versione', verkState.workflow.version);
  if (!verkReadonly()) configuration.append(verkButton('Applica impostazioni', () => {
    const values = inputs.read(), local = variables.read(), parsedSchema = JSON.parse(schema.value);
    const allowed = invokers.value.split(',').map(value => value.trim()).filter(Boolean);
    if (new Set(allowed).size !== allowed.length || allowed.some(value => value !== 'ceo' && !/^team:.+/.test(value))) throw Error('Usa ceo o team: seguito dal riferimento Lid, senza duplicati.');
    verkGraph().input_values = values; verkGraph().variables = local; verkState.workflow.allowed_invokers = allowed; verkState.workflow.input_schema = parsedSchema; verkState.workflow.version = version.value; verkMarkDirty();
  }));
  box.append(configuration);
  verkRunForm(box);
}

function verkZoom(delta) { verkState.zoom = Math.max(globalThis.innerWidth <= 600 ? 1 : 0.75, verkClampZoom(verkState.zoom + delta)); verkRenderCanvas(); }

function verkRenderCanvas() {
  const viewport = $('verkViewport');
  if (!viewport) return;
  const left = viewport.scrollLeft, top = viewport.scrollTop;
  if (globalThis.innerWidth <= 600) verkState.zoom = Math.max(1, verkState.zoom);
  const graph = verkGraph(), zoom = verkState.zoom;
  const width = Math.max(850, ...graph.nodes.map(item => item.x + 320));
  const height = Math.max(500, ...graph.nodes.map(item => item.y + 100 + item.outputs.length * 60));
  const extent = node('div', undefined, 'verk-extent'); extent.style.width = width * zoom + 'px'; extent.style.height = height * zoom + 'px';
  const canvas = node('div', undefined, 'verk-canvas'); canvas.style.width = width + 'px'; canvas.style.height = height + 'px'; canvas.style.transform = 'scale(' + zoom + ')';
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg'); svg.classList.add('verk-connections'); svg.setAttribute('width', width); svg.setAttribute('height', height); svg.setAttribute('aria-hidden', 'true');
  for (const edge of graph.edges) {
    const from = graph.nodes.find(item => item.id === edge.from), to = graph.nodes.find(item => item.id === edge.to);
    if (!from || !to) continue;
    const index = from.outputs.findIndex(port => port.id === edge.port);
    const x1 = from.x + 260, y1 = from.y + 116 + Math.max(index, 0) * 60, x2 = to.x, y2 = to.y + 30;
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', 'M' + x1 + ',' + y1 + ' C' + (x1 + 70) + ',' + y1 + ' ' + (x2 - 70) + ',' + y2 + ' ' + x2 + ',' + y2);
    svg.append(path);
  }
  canvas.append(svg);
  for (const item of graph.nodes) {
    const card = node('article', undefined, 'verk-node' + (item.id === verkState.node ? ' selected' : ''));
    card.dataset.nodeId = item.id; card.style.left = item.x + 'px'; card.style.top = item.y + 'px';
    const heading = node('div', undefined, 'verk-node-heading');
    if (item.type !== 'entry') { const input = verkButton('●', () => verkInput(item.id), 'verk-port'); input.setAttribute('aria-label', 'Collega ingresso ' + item.title); heading.append(input); }
    const title = node('button', item.title, 'verk-node-title'); title.type = 'button'; title.setAttribute('aria-label', 'Configura ' + item.title); verkBindDrag(title, card, item); heading.append(title);
    card.append(heading, node('small', verkTypes[item.type]?.[0] || item.type));
    for (const port of item.outputs) {
      const output = verkButton(port.label + ' ○', () => { if (verkReadonly()) return; verkState.pending = {from: item.id, port: port.id}; $('verkConnectStatus').textContent = 'Collega ' + item.title + ' / ' + port.label + ' a un ingresso.'; }, 'verk-output');
      output.setAttribute('aria-label', 'Uscita ' + item.title + ': ' + port.label); output.disabled = verkReadonly(); card.append(output);
    }
    canvas.append(card);
  }
  extent.append(canvas); viewport.replaceChildren(extent); viewport.scrollLeft = left; viewport.scrollTop = top;
  if ($('verkZoom')) $('verkZoom').textContent = Math.round(zoom * 100) + '%';
  const choose = $('verkChooseNode');
  if (choose) { choose.replaceChildren(node('option', 'Scegli un blocco')); for (const item of graph.nodes) { const option = node('option', item.title + ' · ' + item.id); option.value = item.id; choose.append(option); } choose.value = verkState.node || ''; }
}

function verkInput(id) {
  if (verkState.pending && !verkReadonly()) {
    verkConnect(verkGraph(), verkState.pending.from, verkState.pending.port, id);
    verkState.pending = null; $('verkConnectStatus').textContent = 'Collegamento creato.'; verkMarkDirty(); verkRenderCanvas();
  } else { verkState.node = id; verkRenderInspector(); }
}

function verkBindDrag(button, card, item) {
  let gesture = null, suppressClick = false;
  button.onpointerdown = event => {
    if (event.button !== 0 || verkReadonly()) return;
    gesture = {start: {x: event.clientX, y: event.clientY}, origin: {x: item.x, y: item.y}, moved: false};
    button.setPointerCapture(event.pointerId);
  };
  button.onpointermove = event => {
    if (!gesture) return;
    const current = {x: event.clientX, y: event.clientY};
    if (!gesture.moved && !verkMoved(gesture.start, current)) return;
    gesture.moved = true;
    const position = verkPosition(gesture.start, gesture.origin, current, verkState.zoom);
    item.x = position.x; item.y = position.y; card.style.left = item.x + 'px'; card.style.top = item.y + 'px';
  };
  const finish = () => { if (!gesture) return; suppressClick = gesture.moved; if (gesture.moved) { verkMarkDirty(); verkRenderCanvas(); } gesture = null; };
  button.onpointerup = finish; button.onpointercancel = finish;
  button.onclick = () => { if (suppressClick) { suppressClick = false; return; } verkState.node = item.id; verkRenderInspector(); };
}

function verkRenderInspector() {
  const box = $('verkInspector');
  if (!box) return;
  box.replaceChildren();
  const item = verkGraph().nodes.find(value => value.id === verkState.node);
  if (!item) { box.append(node('h3', 'Configura il percorso'), node('p', 'Scegli un blocco per modificarne i parametri e i collegamenti.', 'muted')); return; }
  box.append(node('h3', verkTypes[item.type]?.[0] || item.type), node('small', item.id));
  const form = node('form');
  const title = verkField(form, 'Titolo', item.title);
  const fields = new Map();
  const jsonFields = new Set(['input', 'mapping', 'result']);
  for (const key of verkTypes[item.type]?.[2] || Object.keys(item.params || {})) {
    if (['condition', 'choices', 'cases'].includes(key)) continue;
    const value = item.params?.[key];
    const multiline = ['instruction', 'question', 'proposal', 'context', 'result'].includes(key);
    const control = jsonFields.has(key) ? verkJSON(form, key, value === undefined ? (key === 'condition' ? null : ['choices', 'cases'].includes(key) ? [] : {}) : value) : verkField(form, key, value, multiline);
    fields.set(key, control);
  }
  form.append(node('p', 'I riferimenti dati usano percorsi dichiarati. Nessun codice JavaScript viene eseguito.', 'muted'));
  const structured = verkNodeForms(form, item);
  if (!verkReadonly()) {
    const apply = node('button', 'Applica al blocco', 'primary'); apply.type = 'submit'; form.append(apply);
    form.onsubmit = event => { event.preventDefault(); action(apply, () => {
      const draft = verkClone(item); draft.title = title.value; draft.params = {...item.params};
      for (const [key, control] of fields) { const raw = control.value; if (raw === '') { delete draft.params[key]; continue; } draft.params[key] = jsonFields.has(key) ? JSON.parse(raw) : ['limit', 'timeout'].includes(key) ? Number(raw) : raw; }
      structured.apply(draft);
      Object.assign(item, draft);
      verkGraph().edges = verkGraph().edges.filter(edge => edge.from !== item.id || item.outputs.some(port => port.id === edge.port));
      verkMarkDirty(); verkRenderCanvas(); notice('Blocco aggiornato nella definizione. Salva il Verk per conservarlo.', false);
    }); };
    if (item.type !== 'entry') form.append(verkButton('Elimina blocco', () => { if (!confirm('Eliminare il blocco e i suoi collegamenti?')) return; verkRemoveNode(verkGraph(), item.id); verkState.node = null; verkMarkDirty(); verkRenderCanvas(); verkRenderInspector(); }));
  }
  box.append(form);
  const links = node('div', undefined, 'verk-links'); links.append(node('h4', 'Collegamenti in uscita'));
  for (const edge of verkGraph().edges.filter(edge => edge.from === item.id)) {
    const row = node('div', undefined, 'verk-link-row'); row.append(node('span', edge.port + ' → ' + edge.to));
    if (!verkReadonly()) row.append(verkButton('Rimuovi', () => { verkGraph().edges = verkGraph().edges.filter(value => value.id !== edge.id); verkMarkDirty(); verkRenderCanvas(); verkRenderInspector(); }));
    links.append(row);
  }
  box.append(links);
}

function verkRunForm(parent) {
  const form = node('form', undefined, 'verk-run');
  form.append(node('h3', 'Avvia un’esecuzione'), node('p', 'L’avvio crea un task reale in Tafla. Le azioni saranno eseguite dal manager e da Envai secondo la definizione salvata.', 'muted'));
  const inputs = verkJSON(form, 'Input JSON', {}); inputs.disabled = false;
  const focus = verkField(form, 'Focus opzionale', ''); focus.disabled = false;
  const origin = node('select'); origin.setAttribute('aria-label', 'Conversazione Smidr di origine'); const none = node('option', 'Nessuna conversazione selezionata'); none.value = ''; origin.append(none); form.append(origin);
  const selected = verkState.selected;
  api('sessions').then(sessions => { if (!state.logged || selected !== verkState.selected) return; for (const session of sessions || []) { if (session.workflow_task_id || !session.turn?.turn_id || !session.chat_id) continue; const option = node('option', session.title || session.id); option.value = session.id; origin.append(option); } }).catch(error => notice(error.message));
  const start = node('button', 'Avvia Verk', 'primary'); start.type = 'submit'; start.disabled = !!selected.archived_at; form.append(start);
  form.onsubmit = event => { event.preventDefault(); action(start, async () => {
    if (verkState.dirty) throw Error('Salva la definizione prima di avviare il Verk.');
    if (!confirm('Avviare questo Verk con gli input indicati?')) return;
    const input = {workflow: verkState.workflow, inputs: JSON.parse(inputs.value), focus: focus.value, origin_session_id: origin.value};
    const fingerprint = JSON.stringify(input);
    if (!verkState.submission || verkState.submission.fingerprint !== fingerprint) verkState.submission = {fingerprint, key: verkID('ui-verk')};
    const task = await api('tasks', {...input, invocation_key: verkState.submission.key});
    verkState.submission = null;
    notice('Verk avviato · task ' + task.id, false);
    if (typeof selectTask === 'function') selectTask(task.id);
    document.querySelector('[data-view="tasks"]')?.click();
  }); };
  parent.append(form);
}

function verkReset() {
  verkState.generation++;
  verkState.items = [];
  verkState.selected = null;
  verkState.workflow = null;
  verkState.node = null;
  verkState.pending = null;
  verkState.submission = null;
  verkState.dirty = false;
  for (const id of ['verkCatalog', 'verkEditor']) if ($(id)) $(id).replaceChildren();
}
