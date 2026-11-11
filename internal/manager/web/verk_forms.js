'use strict';

const verkFormTypes = ['string', 'number', 'boolean', 'object', 'array', 'integer', 'null'];
const verkGroupNames = [['AND', 'AND · tutte vere'], ['OR', 'OR · almeno una vera'], ['NOT', 'NOT · inverti una condizione'], ['XOR', 'XOR · numero dispari di vere'], ['NAND', 'NAND · non tutte vere'], ['NOR', 'NOR · nessuna vera'], ['XNOR', 'XNOR · numero pari di vere'], ['ONE', 'ONE · esattamente una vera']];
const verkComparisons = [['eq', 'è uguale a'], ['neq', 'è diverso da'], ['gt', 'è maggiore di'], ['gte', 'è maggiore o uguale'], ['lt', 'è minore di'], ['lte', 'è minore o uguale'], ['contains', 'contiene'], ['starts_with', 'inizia con'], ['ends_with', 'finisce con'], ['exists', 'è presente']];

function verkFormSection(parent, title, help) {
  const section = node('section', undefined, 'verk-form-section');
  section.append(node('h4', title));
  if (help) section.append(node('p', help, 'muted'));
  parent.append(section);
  return section;
}
function verkFormButton(parent, label, callback, disabled = false) {
  const button = verkButton(label, callback);
  button.disabled = verkReadonly() || disabled;
  parent.append(button);
  return button;
}
function verkFormSelect(parent, label, options, value) {
  const wrapper = node('label', undefined, 'verk-field'); wrapper.append(node('span', label));
  const select = node('select'); select.setAttribute('aria-label', label); select.disabled = verkReadonly();
  for (const choice of options) { const [id, text] = Array.isArray(choice) ? choice : [choice, choice]; const option = node('option', text); option.value = id; select.append(option); }
  select.value = value; wrapper.append(select); parent.append(wrapper); return select;
}
function verkFormCheck(parent, label, checked) {
  const wrapper = node('label', undefined, 'verk-check'); wrapper.append(node('span', label));
  const input = node('input'); input.type = 'checkbox'; input.checked = !!checked; input.disabled = verkReadonly(); input.setAttribute('aria-label', label); wrapper.append(input); parent.append(wrapper); return input;
}
function verkFormChange(control, callback) { control.onchange = () => action(control, callback); }
function verkFormDefault(type) { return ({number: 0, integer: 0, boolean: false, string: '', object: {}, array: [], null: null})[type]; }
function verkFormType(value) { return value === null ? 'null' : Array.isArray(value) ? 'array' : typeof value; }
function verkFormParse(type, raw) {
  if (type === 'string') return String(raw);
  if (type === 'null') return null;
  if (type === 'boolean') { if (raw === 'true') return true; if (raw === 'false') return false; throw Error('Seleziona un valore booleano.'); }
  if (type === 'number' || type === 'integer') { if (!String(raw).trim() || !Number.isFinite(Number(raw)) || (type === 'integer' && !Number.isInteger(Number(raw)))) throw Error('Inserisci un numero finito' + (type === 'integer' ? ' intero.' : '.')); return Number(raw); }
  const value = JSON.parse(raw);
  if ((type === 'array' && !Array.isArray(value)) || (type === 'object' && (!value || typeof value !== 'object' || Array.isArray(value)))) throw Error('Il valore deve essere di tipo ' + type + '.');
  return value;
}
function verkTypedValue(parent, label, type, initial) {
  const field = type === 'boolean' ? verkFormSelect(parent, label, [['false', 'false'], ['true', 'true']], String(initial)) : verkField(parent, label, type === 'string' ? initial : JSON.stringify(initial), ['object', 'array'].includes(type));
  if (type === 'null') { field.value = 'null'; field.disabled = true; }
  if (type === 'number' || type === 'integer') field.inputMode = 'decimal';
  return {read: () => verkFormParse(type, field.value)};
}
function verkFormName(value, used) {
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(value) || used.has(value)) throw Error('Usa nomi univoci: lettere, numeri e underscore, senza iniziare con un numero.');
  used.add(value); return value;
}

function verkValueList(parent, title, initial) {
  const section = verkFormSection(parent, title, 'Valori tipizzati salvati nella definizione. Non inserire credenziali.');
  const rows = node('div'); section.append(rows);
  let values = verkClone(initial || []), readers = [];
  const collect = () => { const used = new Set(); return readers.map(read => { const item = read(); item.name = verkFormName(item.name, used); return item; }); };
  function render() {
    rows.replaceChildren(); readers = [];
    values.forEach((item, index) => {
      const row = node('div', undefined, 'verk-form-row'); rows.append(row);
      const name = verkField(row, 'Nome', item.name);
      const type = verkFormSelect(row, 'Tipo', verkFormTypes, item.type);
      const value = verkTypedValue(row, 'Valore', item.type, item.value);
      readers.push(() => ({...item, name: name.value.trim(), type: item.type, value: value.read()}));
      verkFormChange(type, () => { values = collect(); values[index].type = type.value; values[index].value = verkFormDefault(type.value); render(); });
      verkFormButton(row, 'Rimuovi valore', () => { values = collect(); values.splice(index, 1); render(); });
    });
  }
  verkFormButton(section, 'Aggiungi ' + (title.includes('Input') ? 'input' : 'variabile'), () => { values = collect(); let i = values.length + 1; while (values.some(item => item.name === 'valore_' + i)) i++; values.push({name: 'valore_' + i, type: 'string', value: ''}); render(); });
  render(); return {read: collect};
}

function verkReferenceOptions() {
  const graph = verkGraph(); if (!graph) return [];
  const options = [];
  for (const [prefix, values] of [['inputs', graph.input_values], ['variables', graph.variables]]) for (const item of values || []) options.push([prefix + '.' + item.name, prefix + '.' + item.name + ' · ' + item.type]);
  const upstream = new Set();
  function visit(id) { for (const edge of graph.edges.filter(edge => edge.to === id)) if (edge.from !== verkState.node && !upstream.has(edge.from)) { upstream.add(edge.from); visit(edge.from); } }
  visit(verkState.node);
  for (const item of graph.nodes.filter(item => upstream.has(item.id))) for (const [name, schema] of Object.entries(item.output_schema?.properties || {final_message: {type: 'string'}})) options.push(['nodes.' + item.id + '.' + name, item.title + ' · ' + name + ' : ' + schema.type]);
  return options;
}
function verkOperandForm(parent, initial, side) {
  let operand = verkClone(initial || {mode: 'literal', type: 'number', value: 0}), readValue;
  const box = node('div', undefined, 'verk-operand'); parent.append(box);
  function render() {
    box.replaceChildren();
    const mode = verkFormSelect(box, side + ': sorgente', [['ref', 'Variabile'], ['literal', 'Valore fisso']], operand.mode);
    if (operand.mode === 'ref') {
      const path = verkField(box, side + ': percorso', operand.value);
      const options = verkFormSelect(box, side + ': scegli riferimento', [['', 'Scegli dati dichiarati'], ...verkReferenceOptions()], '');
      verkFormChange(options, () => { if (options.value) path.value = options.value; });
      readValue = () => { if (!path.value.trim()) throw Error('Il riferimento deve avere un percorso.'); return {mode: 'ref', value: path.value}; };
    } else {
      const type = verkFormSelect(box, side + ': tipo', ['number', 'string', 'boolean'], operand.type);
      const value = verkTypedValue(box, side + ': valore', operand.type, operand.value);
      readValue = () => ({mode: 'literal', type: operand.type, value: value.read()});
      verkFormChange(type, () => { operand = {mode: 'literal', type: type.value, value: verkFormDefault(type.value)}; render(); });
    }
    verkFormChange(mode, () => { operand = mode.value === 'ref' ? {mode: 'ref', value: ''} : {mode: 'literal', type: 'number', value: 0}; render(); });
  }
  render(); return {read: () => readValue()};
}
function verkDefaultRule() { return {kind: 'condition', operator: 'eq', left: {mode: 'literal', type: 'number', value: 0}, right: {mode: 'literal', type: 'number', value: 0}}; }
function verkRuleForm(parent, initial, depth = 1) {
  if (depth > 32) throw Error('La regola supera 32 livelli.');
  let rule = verkClone(initial || verkDefaultRule()), readRule;
  const box = node('fieldset', undefined, 'verk-rule'); parent.append(box);
  function render() {
    box.replaceChildren(); box.append(node('legend', rule.kind === 'group' ? 'Gruppo logico' : 'Condizione'));
    if (rule.kind === 'group') {
      const op = verkFormSelect(box, 'Operatore gruppo', verkGroupNames, rule.op);
      const readers = [];
      (rule.children || []).forEach((child, index) => {
        const wrapper = node('div', undefined, 'verk-rule-child'); box.append(wrapper);
        readers.push(verkRuleForm(wrapper, child, depth + 1));
        verkFormButton(wrapper, 'Rimuovi regola', () => { rule = readRule(); rule.children.splice(index, 1); render(); });
      });
      readRule = () => ({kind: 'group', op: rule.op, children: readers.map(child => child.read())});
      verkFormChange(op, () => { const draft = readRule(); if (op.value === 'NOT' && draft.children.length !== 1) { op.value = rule.op; throw Error('NOT richiede esattamente una condizione o un gruppo.'); } draft.op = op.value; rule = draft; render(); });
      for (const kind of ['condition', 'group']) verkFormButton(box, kind === 'condition' ? 'Aggiungi condizione' : 'Aggiungi gruppo', () => { rule = readRule(); rule.children.push(kind === 'condition' ? verkDefaultRule() : {kind: 'group', op: 'AND', children: [verkDefaultRule()]}); render(); }, rule.op === 'NOT' && rule.children.length >= 1 || depth >= 32);
    } else {
      const left = verkOperandForm(box, rule.left, 'Sinistra');
      const op = verkFormSelect(box, 'Confronto', verkComparisons, rule.operator);
      const right = rule.operator === 'exists' ? null : verkOperandForm(box, rule.right, 'Destra');
      readRule = () => { const value = {kind: 'condition', operator: rule.operator, left: left.read()}; if (right) value.right = right.read(); return value; };
      verkFormChange(op, () => { const oldRight = rule.right; rule = readRule(); rule.operator = op.value; if (op.value !== 'exists' && !rule.right) rule.right = oldRight || {mode: 'literal', type: 'number', value: 0}; render(); });
      verkFormButton(box, 'Raggruppa condizione', () => { rule = {kind: 'group', op: 'AND', children: [readRule()]}; render(); }, depth >= 32);
    }
  }
  render(); return {read: () => readRule()};
}

function verkPortLabels(parent, ports) {
  const section = verkFormSection(parent, 'Uscite del blocco');
  const fields = (ports || []).map(port => { const field = verkField(section, 'Nome uscita · ' + port.id, port.label); return () => { if (!field.value.trim()) throw Error('Ogni uscita richiede un nome.'); return {...port, label: field.value}; }; });
  return {read: () => fields.map(read => read())};
}
function verkBranchForm(parent, item) {
  const section = verkFormSection(parent, 'Rami in ordine di priorità', 'Viene scelto il primo ramo vero. Dati mancanti o incompatibili seguono Errore dati. AND e OR usano il cortocircuito; XOR conta un numero dispari di condizioni vere.');
  const rows = node('div'); section.append(rows);
  let branches = verkClone(item.branches || []), readers = [];
  const fallbackID = item.default_id || 'default';
  const fallback = verkField(section, 'Nome ramo predefinito', item.outputs.find(port => port.id === fallbackID)?.label || 'Altrimenti');
  const invalid = verkField(section, 'Nome uscita errore dati', item.outputs.find(port => port.id === 'invalid')?.label || 'Errore dati');
  const collect = () => readers.map(read => read());
  function render() {
    rows.replaceChildren(); readers = [];
    branches.forEach((branch, index) => {
      const row = node('article', undefined, 'verk-form-row'); rows.append(row); row.append(node('strong', 'Priorità ' + (index + 1)), node('code', branch.id));
      const label = verkField(row, 'Nome ramo', branch.label);
      const rule = verkRuleForm(row, branch.rule);
      readers.push(() => { if (!label.value.trim()) throw Error('Assegna un nome al ramo.'); return {...branch, label: label.value, rule: rule.read()}; });
      const move = delta => { branches = collect(); [branches[index], branches[index + delta]] = [branches[index + delta], branches[index]]; render(); };
      verkFormButton(row, 'Sposta prima', () => move(-1), index === 0);
      verkFormButton(row, 'Sposta dopo', () => move(1), index === branches.length - 1);
      verkFormButton(row, 'Elimina ramo', () => { branches = collect(); branches.splice(index, 1); render(); });
    });
  }
  verkFormButton(section, 'Aggiungi ramo', () => { branches = collect(); branches.push({id: verkID('branch'), label: 'Nuovo ramo', rule: {kind: 'group', op: 'AND', children: [verkDefaultRule()]}}); render(); });
  render();
  return {read: () => { const values = collect(); if (!values.length || !fallback.value.trim() || !invalid.value.trim()) throw Error('Servono almeno un ramo, un predefinito e un’uscita errore.'); return {branches: values, default_id: fallbackID, outputs: [...values.map(branch => ({id: branch.id, label: branch.label})), {id: fallbackID, label: fallback.value}, {id: 'invalid', label: invalid.value}]}; }};
}

function verkChoiceForm(parent, item) {
  const isSwitch = item.type === 'switch', key = isSwitch ? 'cases' : 'choices';
  const section = verkFormSection(parent, isSwitch ? 'Casi dello switch' : 'Alternative della decisione AI', 'Gli ID rimangono stabili; i nomi identificano le uscite sul canvas.');
  const rows = node('div'); section.append(rows);
  let choices = verkClone(item.params[key] || []), readers = [];
  const collect = () => readers.map(read => read());
  function render() {
    rows.replaceChildren(); readers = [];
    choices.forEach((choice, index) => {
      const row = node('div', undefined, 'verk-form-row'); rows.append(row); row.append(node('code', choice.id));
      const label = verkField(row, 'Nome alternativa', choice.label || item.outputs.find(port => port.id === choice.id)?.label || choice.id);
      let typed, type;
      if (isSwitch) { const kind = verkFormType(choice.value === undefined ? '' : choice.value); type = verkFormSelect(row, 'Tipo del caso', verkFormTypes, kind); typed = verkTypedValue(row, 'Valore del caso', kind, choice.value === undefined ? '' : choice.value); verkFormChange(type, () => { choices = collect(); choices[index].value = verkFormDefault(type.value); render(); }); }
      readers.push(() => { if (!label.value.trim()) throw Error('Assegna un nome all’alternativa.'); return {...choice, label: label.value, ...(typed ? {value: typed.read()} : {})}; });
      verkFormButton(row, 'Rimuovi alternativa', () => { choices = collect(); choices.splice(index, 1); render(); });
    });
  }
  const fallback = isSwitch ? verkField(section, 'Nome predefinito', item.outputs.find(port => port.id === (item.default_id || 'default'))?.label || 'Altrimenti') : null;
  const invalid = verkField(section, 'Nome errore dati', item.outputs.find(port => port.id === 'invalid')?.label || 'Errore dati');
  verkFormButton(section, 'Aggiungi alternativa', () => { choices = collect(); choices.push({id: verkID('choice'), label: 'Nuova alternativa', ...(isSwitch ? {value: ''} : {})}); render(); }); render();
  return {read: () => { const values = collect(); if (values.length < (isSwitch ? 1 : 2)) throw Error(isSwitch ? 'Inserisci almeno un caso.' : 'Inserisci almeno due alternative.'); return {key, values, outputs: [...values.map(choice => ({id: choice.id, label: choice.label})), ...(fallback ? [{id: item.default_id || 'default', label: fallback.value}] : []), {id: 'invalid', label: invalid.value}], ...(fallback ? {default_id: item.default_id || 'default'} : {})}; }};
}

function verkSchemaForm(parent, initial) {
  const section = verkFormSection(parent, 'Contratto di output', 'Il messaggio finale completo è sempre conservato. I campi obbligatori mancanti o incompatibili fermano il task.');
  const base = verkClone(initial === undefined || initial === null ? {type: 'object', properties: {}, required: []} : initial);
  if (!base || typeof base !== 'object' || Array.isArray(base) || base.type && base.type !== 'object' || base.$ref || base.allOf || base.anyOf || base.oneOf || Object.values(base.properties || {}).some(schema => !schema || typeof schema !== 'object' || Array.isArray(schema))) { const advanced = verkJSON(section, 'Schema complesso · modifica avanzata', base); return {read: () => JSON.parse(advanced.value)}; }
  let fields = Object.entries(base.properties || {}).map(([name, schema]) => ({name, schema, required: (base.required || []).includes(name)}));
  fields = [{name: 'final_message', schema: {...base.properties?.final_message, type: 'string'}, required: true}, ...fields.filter(field => field.name !== 'final_message')];
  const rows = node('div'); section.append(rows); let readers = [];
  const collect = () => { const used = new Set(); return readers.map(read => { const field = read(); field.name = verkFormName(field.name, used); return field; }); };
  function render() {
    rows.replaceChildren(); readers = [];
    fields.forEach((field, index) => {
      const row = node('div', undefined, 'verk-form-row'); rows.append(row); const fixed = field.name === 'final_message';
      const name = verkField(row, 'Nome campo output', field.name); name.disabled = verkReadonly() || fixed;
      const type = verkFormSelect(row, 'Tipo campo output', [['', 'Tipo definito dallo schema'], ...verkFormTypes], typeof field.schema.type === 'string' ? field.schema.type : ''); type.disabled = verkReadonly() || fixed || Array.isArray(field.schema.type);
      const required = verkFormCheck(row, 'Obbligatorio', field.required); required.disabled = verkReadonly() || fixed;
      const description = verkField(row, 'Descrizione campo', field.schema.description || '');
      readers.push(() => ({name: fixed ? 'final_message' : name.value.trim(), schema: {...field.schema, ...(fixed ? {type: 'string'} : type.value && !Array.isArray(field.schema.type) ? {type: type.value} : {}), description: description.value}, required: fixed || required.checked}));
      if (!fixed) verkFormButton(row, 'Rimuovi campo output', () => { fields = collect(); fields.splice(index, 1); render(); });
    });
  }
  verkFormButton(section, 'Aggiungi campo output', () => { fields = collect(); let i = fields.length; while (fields.some(field => field.name === 'campo_' + i)) i++; fields.push({name: 'campo_' + i, schema: {type: 'string'}, required: false}); render(); }); render();
  return {read: () => { const values = collect(), properties = Object.create(null); for (const field of values) properties[field.name] = field.schema; return {...base, type: 'object', properties, required: values.filter(field => field.required).map(field => field.name)}; }};
}

function verkTriggerForm(parent, initial) {
  const section = verkFormSection(parent, 'Trigger di ingresso autorizzati', 'Salvare un trigger abilita gli avvii reali da quella sorgente. I webhook richiedono la firma del proprietario; nessuna sottoscrizione esterna viene creata.');
  const labels = [['smidr', 'Richiesta Smidr'], ['push', 'Push Git'], ['pr', 'Pull request'], ['ci', 'CI terminata'], ['schedule', 'Pianificazione'], ['webhook', 'Webhook']];
  let triggers = verkClone(initial || []), readers = [];
  const choices = node('div', undefined, 'verk-trigger-checks'), rows = node('div'); section.append(choices, rows);
  const collect = () => readers.map(read => read());
  function render() {
    choices.replaceChildren(); rows.replaceChildren(); readers = [];
    for (const [type, label] of labels) { const check = verkFormCheck(choices, label, triggers.some(trigger => trigger.type === type)); verkFormChange(check, () => { triggers = collect(); if (check.checked) triggers.push({id: verkID('trigger'), type, ...(['push', 'pr', 'ci'].includes(type) ? {scope: 'specific', repositories: [], branches: [], events: type === 'pr' ? ['opened', 'synchronize'] : type === 'ci' ? ['success', 'failure'] : []} : type === 'schedule' ? {schedule: '0 9 * * 1-5', timezone: 'UTC'} : type === 'webhook' ? {source_ref: ''} : {})}); else triggers = triggers.filter(trigger => trigger.type !== type); render(); }); }
    for (const trigger of triggers) {
      const row = node('div', undefined, 'verk-form-row'); rows.append(row); row.append(node('strong', labels.find(([type]) => type === trigger.type)?.[1] || trigger.type));
      let scope, repos, branches, events, schedule, zone, source;
      if (['push', 'pr', 'ci'].includes(trigger.type)) {
        scope = verkFormSelect(row, 'Ambito repository', [['specific', 'Repository specifica'], ['list', 'Elenco repository'], ['organization', 'Organizzazioni autorizzate']], trigger.scope || 'specific');
        repos = verkField(row, 'Repository / organizzazioni · separati da virgola', (trigger.repositories || []).join(', '));
        branches = verkField(row, 'Branch esatti · separati da virgola', (trigger.branches || []).join(', '));
        if (trigger.type !== 'push') events = verkField(row, trigger.type === 'pr' ? 'Azioni PR · separate da virgola' : 'Esiti CI · separati da virgola', (trigger.events || []).join(', '));
      } else if (trigger.type === 'schedule') { schedule = verkField(row, 'Cron · cinque campi', trigger.schedule); zone = verkField(row, 'Fuso orario IANA', trigger.timezone); }
      else if (trigger.type === 'webhook') source = verkField(row, 'Sorgente webhook registrata', trigger.source_ref);
      const csv = field => field.value.split(',').map(value => value.trim()).filter(Boolean);
      readers.push(() => ({...trigger, ...(scope ? {scope: scope.value, repositories: csv(repos), branches: csv(branches), ...(events ? {events: csv(events)} : {})} : {}), ...(schedule ? {schedule: schedule.value.trim(), timezone: zone.value.trim()} : {}), ...(source ? {source_ref: source.value.trim()} : {})}));
    }
  }
  render(); return {read: collect};
}
function verkExitForm(parent, initial) {
  const section = verkFormSection(parent, 'Azioni di uscita', 'Un’esecuzione automatica coinvolge Smidr soltanto quando richiedi una notifica o un intervento.');
  const value = initial || {set_state: true, state: 'completed', return_data: true, notify_smidr: false, intervention: false};
  const setState = verkFormCheck(section, 'Imposta stato task', value.set_state);
  const status = verkFormSelect(section, 'Stato task', [['completed', 'Completato'], ['blocked', 'Bloccato'], ['cancelled', 'Annullato']], value.state || 'completed');
  const data = verkFormCheck(section, 'Restituisci dati', value.return_data);
  const notify = verkFormCheck(section, 'Notifica Smidr', value.notify_smidr);
  const intervention = verkFormCheck(section, 'Richiedi intervento', value.intervention);
  return {read: () => ({...value, set_state: setState.checked, state: status.value, return_data: data.checked, notify_smidr: notify.checked, intervention: intervention.checked})};
}

function verkToolClientForm(parent, initial) {
  const section = verkFormSection(parent, 'Dispositivo dello strumento', 'Scegli dove eseguire lo strumento. Un client offline rimane selezionabile e produce un errore esplicito all’esecuzione. Gli strumenti legati a un client richiedono quel dispositivo.');
  const defaults = [['', 'Dispositivo del Verk · manager se non specificato'], ['manager', 'Manager · questo dispositivo']];
  const select = verkFormSelect(section, 'Esegui sul dispositivo', [...defaults, ...(initial && initial !== 'manager' ? [[initial, initial + ' · disponibilità da verificare']] : [])], initial || '');
  const status = node('p', 'Caricamento dei dispositivi registrati…', 'muted'); status.setAttribute('role', 'status'); section.append(status);
  const workflow = verkState.workflow;
  api('devices').then(devices => {
    if (!state.logged || workflow !== verkState.workflow) return;
    if (!Array.isArray(devices)) throw Error('Risposta inventario non valida.');
    const current = select.value, choices = [...defaults], seen = new Set(['', 'manager']);
    for (const device of devices) {
      if (!device.id || seen.has(device.id)) continue;
      seen.add(device.id);
      choices.push([device.id, (device.name || device.id) + ' · ' + (device.revoked ? 'Revocato' : device.online ? 'Online' : 'Offline')]);
    }
    if (!seen.has(current)) choices.push([current, current + ' · non disponibile']);
    select.replaceChildren();
    for (const [id, label] of choices) { const option = node('option', label); option.value = id; select.append(option); }
    select.value = current;
    status.textContent = devices.length ? 'Inventario corrente del manager.' : 'Nessun client registrato. Il manager è disponibile.';
  }).catch(error => { if (state.logged && workflow === verkState.workflow) status.textContent = 'Inventario non disponibile: ' + error.message + ' La selezione è conservata.'; });
  return {read: () => select.value};
}

function verkNodeForms(parent, item) {
  const client = item.type === 'tool' ? verkToolClientForm(parent, item.params.client_id) : null;
  const branches = item.type === 'if' ? verkBranchForm(parent, item) : null;
  const choices = ['ai', 'switch'].includes(item.type) ? verkChoiceForm(parent, item) : null;
  const ports = !branches && !choices && item.outputs.length ? verkPortLabels(parent, item.outputs) : null;
  const schema = ['lid', 'tool', 'verk'].includes(item.type) || item.output_schema ? verkSchemaForm(parent, item.output_schema) : null;
  const triggers = item.type === 'entry' ? verkTriggerForm(parent, item.triggers) : null;
  const exit = item.type === 'end' ? verkExitForm(parent, item.exit_actions) : null;
  let condition = null, enabled;
  if (item.type === 'repeat') { const section = verkFormSection(parent, 'Condizione di ripetizione', 'Il budget limita le correzioni. L’esaurimento conserva il checkpoint e richiede una decisione esplicita.'); enabled = verkFormCheck(section, 'Usa condizione logica', !!item.params.condition); condition = verkRuleForm(section, item.params.condition || verkDefaultRule()); }
  return {apply: draft => {
    if (client) { const id = client.read(); if (id) draft.params.client_id = id; else delete draft.params.client_id; }
    if (branches) Object.assign(draft, branches.read());
    if (choices) { const value = choices.read(); draft.params[value.key] = value.values; draft.outputs = value.outputs; if (value.default_id) draft.default_id = value.default_id; }
    if (ports) draft.outputs = ports.read();
    if (schema) draft.output_schema = schema.read();
    if (triggers) draft.triggers = triggers.read();
    if (exit) draft.exit_actions = exit.read();
    if (condition) { if (enabled.checked) draft.params.condition = condition.read(); else delete draft.params.condition; }
  }};
}
