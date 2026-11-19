// Run with: node --test scripts/tests/test_model_filter.cjs
// Exercises real TypeScript source. React/Radix rendering and settings HTTP are fixtures.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const { createRequire } = require('node:module');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const root = path.resolve(__dirname, '../..');
const webRequire = createRequire(path.join(root, 'web/package.json'));
const ts = webRequire('typescript');
const { createTranslator } = webRequire('use-intl');
const messages = JSON.parse(fs.readFileSync(path.join(root, 'web/src/locales/zh_hans.json'), 'utf8'));
const plain = value => JSON.parse(JSON.stringify(value));

function loadSource(relative, imports = {}, extraSource = '') {
    const filename = path.join(root, relative);
    const compiled = ts.transpileModule(fs.readFileSync(filename, 'utf8') + extraSource, {
        fileName: filename,
        compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX },
    }).outputText;
    const module = { exports: {} };
    vm.runInNewContext(compiled, {
        module, exports: module.exports, Error, queueMicrotask,
        require(name) {
            assert.ok(Object.hasOwn(imports, name), `Unexpected import: ${name}`);
            return imports[name];
        },
    }, { filename });
    return module.exports;
}

const filter = loadSource('web/src/lib/model-filter.ts');
const regex = loadSource('web/src/lib/regex.ts');
const rule = (action, operator, value, join = 'and') => ({ action, operator, value, join });

function condition(row, name) {
    const match = {
        equals: () => name === row.value,
        starts: () => name.startsWith(row.value),
        ends: () => name.endsWith(row.value),
        contains: () => name.includes(row.value),
    }[row.operator]();
    return row.action === 'keep' ? match : !match;
}

function booleanResult(rows, name) {
    let result = false;
    let term = true;
    for (let index = 0; index < rows.length; index++) {
        if (index > 0 && rows[index].join === 'or') {
            result ||= term;
            term = true;
        }
        term &&= condition(rows[index], name);
    }
    return result || term;
}

test('all eight conditions treat punctuation, Unicode, spaces and control characters as literal text', () => {
    const values = ['gpt-', 'gpt+v1.0', 'a(b)|c[1]{2}^$\\', '模型-😀', 'line\nend', '\t\u0000\u2028\u2029', ' spaced '];
    for (const action of ['keep', 'exclude']) for (const operator of ['equals', 'starts', 'ends', 'contains']) for (const value of values) {
        const rows = [rule(action, operator, value)];
        const pattern = filter.compileModelFilter(rows);
        const compiled = new RegExp(pattern);
        for (const name of ['', value, `prefix${value}`, `${value}suffix`, `prefix${value}suffix`, `${value}\n`, 'other']) {
            assert.equal(compiled.test(name), condition(rows[0], name), `${action}/${operator}: ${JSON.stringify(name)}`);
        }
        assert.deepEqual(plain(filter.parseModelFilter(pattern)), rows);
    }
});

test('AND takes precedence over OR, including negative conditions within each branch', () => {
    const names = ['', 'gpt-4', 'gpt-preview', 'gpt-preview-latest', 'claude-latest', 'other', 'gpt-4\n'];
    for (let mask = 0; mask < 64; mask++) {
        const rows = [
            rule('keep', 'starts', 'gpt-'),
            rule(mask & 1 ? 'exclude' : 'keep', 'contains', 'preview', mask & 2 ? 'or' : 'and'),
            rule(mask & 4 ? 'exclude' : 'keep', 'ends', '-latest', mask & 8 ? 'or' : 'and'),
            rule(mask & 16 ? 'exclude' : 'keep', 'equals', 'other', mask & 32 ? 'or' : 'and'),
        ];
        const pattern = filter.compileModelFilter(rows);
        const compiled = new RegExp(pattern);
        for (const name of names) assert.equal(compiled.test(name), booleanResult(rows, name), `combination ${mask}: ${name}`);
        assert.deepEqual(plain(filter.parseModelFilter(pattern)), rows);
    }
    const pattern = filter.compileModelFilter([rule('exclude', 'contains', 'preview'), rule('keep', 'starts', 'gpt-', 'or')]);
    assert.equal(new RegExp(pattern).test('gpt-preview'), true, 'OR can retain a model excluded in another branch');
});

test('equals and ends require the actual string end, including final newline boundaries', () => {
    for (const operator of ['equals', 'ends']) {
        const compiled = new RegExp(filter.compileModelFilter([rule('keep', operator, 'latest')]));
        assert.equal(compiled.test('latest'), true);
        assert.equal(compiled.test('latest\n'), false);
        assert.equal(compiled.test('latest\r\n'), false);
        const multiline = new RegExp(filter.compileModelFilter([rule('keep', operator, 'latest\n')]));
        assert.equal(multiline.test('latest\n'), true);
    }
});

test('empty filters, empty conditions and invalid runtime enums are distinct', () => {
    assert.equal(filter.compileModelFilter([]), '');
    assert.deepEqual(plain(filter.parseModelFilter('')), []);
    assert.throws(() => filter.compileModelFilter([rule('keep', 'contains', '')]), /must not be empty/);
    for (const patch of [{ action: 'unknown' }, { operator: 'unknown' }, { join: 'unknown' }, { value: null }]) {
        assert.throws(() => filter.compileModelFilter([{ ...rule('keep', 'contains', 'gpt'), ...patch }]), /invalid|empty/);
    }
    assert.equal(new RegExp(filter.compileModelFilter([rule('keep', 'equals', ' ')] )).test(' '), true);
});

test('handwritten and malformed expressions never undergo a lossy conversion', () => {
    for (const pattern of ['claude', 'gpt|claude', '^gpt-', '-latest$', '^(?!.*preview).+$', '[', '^(?:)[\\s\\S]*$', '^(?:(?=gpt)|)[\\s\\S]*$', '^(?:(?=gpt.*))[\\s\\S]*$']) {
        assert.equal(filter.parseModelFilter(pattern), null, pattern);
    }
    const canonical = filter.compileModelFilter([rule('keep', 'contains', 'gpt.')]);
    for (const pattern of [` ${canonical}`, `${canonical} `, canonical.replace('gpt\\.', 'gpt[.]')]) {
        assert.equal(filter.parseModelFilter(pattern), null, 'equivalent or altered bytes remain raw');
    }
});

test('new filter messages are present and valid in every supported locale', () => {
    const keys = ['add', 'edit', 'preview', 'editExpression', 'finishEditing', 'clearToVisual', 'apply', 'save', 'applyFailed'];
    for (const [file, locale] of [['zh_hans', 'zh-CN'], ['zh_hant', 'zh-TW'], ['en', 'en']]) {
        const localeMessages = JSON.parse(fs.readFileSync(path.join(root, `web/src/locales/${file}.json`), 'utf8'));
        const t = createTranslator({ locale, messages: localeMessages, namespace: 'regexFilter', onError: error => { throw error; } });
        for (const key of keys) assert.ok(t(key));
        assert.ok(t('conditionCount', { count: 2 }));
        assert.ok(t('changeJoin', { number: 2, join: 'OR' }));
    }
});

let activeRuntime;
let nextRuntimeId = 0;
function slot(initial) {
    const runtime = activeRuntime;
    assert.ok(runtime, 'hooks must execute during fixture render');
    const index = runtime.index++;
    if (!(index in runtime.slots)) runtime.slots[index] = typeof initial === 'function' ? initial() : initial;
    return [runtime, index];
}
const react = {
    useState(initial) {
        const [runtime, index] = slot(initial);
        return [runtime.slots[index], update => { runtime.slots[index] = typeof update === 'function' ? update(runtime.slots[index]) : update; }];
    },
    useRef(initial) { const [runtime, index] = slot(() => ({ current: initial })); return runtime.slots[index]; },
    useId() { const [runtime, index] = slot(() => `fixture-${++nextRuntimeId}`); return runtime.slots[index]; },
    useEffect(callback, dependencies) {
        const [runtime, index] = slot(() => ({ dependencies: null, cleanup: null }));
        const state = runtime.slots[index];
        if (!state.dependencies || dependencies.some((value, i) => value !== state.dependencies[i])) {
            runtime.effects.push(() => { state.cleanup?.(); state.dependencies = dependencies; state.cleanup = callback(); });
        }
    },
    useMemo(callback, dependencies) {
        const [runtime, index] = slot(() => ({ dependencies: null, value: null }));
        const state = runtime.slots[index];
        if (!state.dependencies || dependencies.some((value, i) => value !== state.dependencies[i])) {
            state.dependencies = dependencies;
            state.value = callback();
        }
        return state.value;
    },
};
const jsx = (type, props) => ({ type, props: props ?? {} });
const translations = { useTranslations: namespace => createTranslator({ locale: 'zh-CN', messages, namespace, onError: error => { throw error; } }) };
const ui = {
    'react': react,
    'react/jsx-runtime': { jsx, jsxs: jsx, Fragment: 'fragment' },
    'use-intl': translations,
    'lucide-react': Object.fromEntries(['LoaderCircle', 'Pencil', 'Plus', 'X', 'Monitor', 'Globe', 'Clock', 'Shield', 'Filter', 'HelpCircle'].map(name => [name, name])),
    'radix-ui': { Dialog: Object.fromEntries(['Root', 'Trigger', 'Portal', 'Overlay', 'Content', 'Title', 'Description'].map(name => [name, `dialog-${name.toLowerCase()}`])) },
    '@/components/ui/button': { Button: 'button' },
    '@/components/ui/input': { Input: 'input' },
    '@/components/ui/select': Object.fromEntries(['Select', 'SelectContent', 'SelectItem', 'SelectTrigger', 'SelectValue'].map(name => [name, name.toLowerCase()])),
    '@/lib/model-filter': filter,
    '@/lib/regex': regex,
};
const components = loadSource('web/src/components/common/RegexFilterField.tsx', ui, '\nexport { ModelFilterEditor };\n');

function runtime(component, props) {
    return {
        component, props, slots: [], effects: [], index: 0,
        render() {
            this.index = 0;
            activeRuntime = this;
            try { this.tree = this.component(this.props); }
            finally { activeRuntime = null; }
            for (const effect of this.effects.splice(0)) effect();
            return this.tree;
        },
        unmount() { for (const state of this.slots) state?.cleanup?.(); },
    };
}
function nodes(tree) {
    if (Array.isArray(tree)) return tree.flatMap(nodes);
    if (!tree || typeof tree !== 'object') return [];
    return [tree, ...nodes(tree.props?.children)];
}
function text(tree) {
    if (Array.isArray(tree)) return tree.map(text).join('');
    if (tree === null || tree === undefined || typeof tree === 'boolean') return '';
    return typeof tree === 'object' ? text(tree.props?.children) : String(tree);
}
function find(fixture, predicate) { const node = nodes(fixture.tree).find(predicate); assert.ok(node, 'expected control was rendered'); return node; }
function button(fixture, label) { return find(fixture, node => node.type === 'button' && text(node) === label); }
function editor(value, onApply = async () => {}) {
    const fixture = runtime(components.ModelFilterEditor, { label: '模型过滤', value, onApply, onCancel() {}, isApplying: false, applyLabel: '应用' });
    fixture.render();
    return fixture;
}

test('the saved filter is displayed as its full expression for visual and handwritten rules', () => {
    for (const original of ['gpt|claude', filter.compileModelFilter([rule('keep', 'starts', 'gpt-'), rule('exclude', 'contains', 'preview')])]) {
        const fixture = runtime(components.RegexFilterField, {
            id: 'saved-filter', label: '模型过滤', value: original, onChange() {},
        });
        fixture.render();
        assert.equal(text(find(fixture, node => node.type === 'pre')), original);
        assert.equal(nodes(fixture.tree).some(node => node.type === 'ul'), false);
    }
});

test('the dialog keeps editable visual conditions for canonical expressions', () => {
    const fixture = editor(filter.compileModelFilter([rule('keep', 'starts', 'gpt-'), rule('exclude', 'contains', 'preview')]));
    const inputs = nodes(fixture.tree).filter(node => node.type === 'input');
    assert.deepEqual(inputs.map(node => node.props.value), ['gpt-', 'preview']);
    inputs[0].props.onChange({ target: { value: 'gemini-' } });
    fixture.render();
    const updated = new RegExp(text(find(fixture, node => node.type === 'pre')));
    assert.equal(updated.test('gemini-2.5-flash'), true);
    assert.equal(updated.test('gpt-4.1'), false);
    assert.equal(updated.test('gemini-preview'), false);
});

test('clearing a handwritten expression returns to visual editing before any apply or save', async () => {
    for (const editing of [false, true]) {
        const changes = [];
        const fixture = editor('gpt|claude', async pattern => { changes.push(pattern); });
        if (editing) {
            button(fixture, '编辑表达式').props.onClick();
            fixture.render();
            find(fixture, node => node.type === 'textarea').props.onChange({ target: { value: '[' } });
            fixture.render();
        }
        const clear = () => button(fixture, '清空并使用可视化编辑');
        assert.equal(clear().props.type, 'button', 'reset must not submit a parent form');
        fixture.props.isApplying = true;
        fixture.render();
        assert.equal(clear().props.disabled, true);
        fixture.props.isApplying = false;
        fixture.render();
        clear().props.onClick();
        fixture.render();
        assert.equal(find(fixture, node => node.type === 'input').props.value, '');
        assert.equal(nodes(fixture.tree).some(node => node.type === 'textarea'), false);
        assert.deepEqual(changes, [], 'reset only changes the local draft');
        assert.equal(button(fixture, '应用').props.disabled, true);
        find(fixture, node => node.type === 'input').props.onChange({ target: { value: 'gpt-' } });
        fixture.render();
        find(fixture, node => node.type === 'form').props.onSubmit({ preventDefault() {}, stopPropagation() {} });
        await new Promise(resolve => setImmediate(resolve));
        assert.deepEqual(changes, [filter.compileModelFilter([rule('keep', 'contains', 'gpt-')])]);
    }
});

test('preview is non-interactive; only Edit expression creates an editable control', () => {
    const original = 'gpt|claude';
    const fixture = editor(original);
    const preview = find(fixture, node => node.type === 'pre');
    assert.equal(text(preview), original);
    assert.equal(preview.props.onClick, undefined);
    assert.equal(preview.props.tabIndex, undefined);
    assert.equal(nodes(fixture.tree).some(node => node.type === 'textarea'), false);
    button(fixture, '编辑表达式').props.onClick();
    fixture.render();
    const input = find(fixture, node => node.type === 'textarea');
    assert.equal(input.props.value, original);
    input.props.onChange({ target: { value: '^gpt-(?!.*preview)' } });
    fixture.render();
    button(fixture, '完成编辑').props.onClick();
    fixture.render();
    assert.equal(nodes(fixture.tree).some(node => node.type === 'textarea'), false);
    assert.equal(text(find(fixture, node => node.type === 'pre')), '^gpt-(?!.*preview)');
});

test('canonical values containing CR/LF stay raw so single-line controls cannot strip them', () => {
    const original = filter.compileModelFilter([rule('keep', 'equals', 'line\r\nend')]);
    const fixture = editor(original);
    assert.equal(nodes(fixture.tree).some(node => node.type === 'input'), false);
    assert.equal(text(find(fixture, node => node.type === 'pre')), original);
    button(fixture, '编辑表达式').props.onClick();
    fixture.render();
    assert.equal(find(fixture, node => node.type === 'textarea').props.value, original);
    button(fixture, '完成编辑').props.onClick();
    fixture.render();
    assert.equal(nodes(fixture.tree).some(node => node.type === 'input'), false);
    assert.equal(text(find(fixture, node => node.type === 'pre')), original);
});

test('blank rows cannot apply; conditions are local until Apply and all rows can be removed', async () => {
    const changes = [];
    const fixture = editor('', async pattern => { changes.push(pattern); });
    assert.equal(button(fixture, '应用').props.disabled, true);
    find(fixture, node => node.type === 'input').props.onChange({ target: { value: 'gpt.' } });
    fixture.render();
    assert.equal(changes.length, 0);
    assert.equal(new RegExp(text(find(fixture, node => node.type === 'pre'))).test('gptX'), false);
    find(fixture, node => node.type === 'button' && node.props['aria-label'] === '移除第 1 条条件').props.onClick();
    fixture.render();
    assert.equal(button(fixture, '应用').props.disabled, false);
    let stopped = false;
    find(fixture, node => node.type === 'form').props.onSubmit({ preventDefault() {}, stopPropagation() { stopped = true; } });
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(stopped, true, 'submit must not bubble through the portal to the parent channel form');
    assert.deepEqual(changes, ['']);
});

test('pending global apply prevents dismissal and duplicate writes; rejection preserves the draft', async () => {
    let reject;
    let writes = 0;
    const notices = [];
    const pending = new Promise((resolve, rejectPending) => { reject = rejectPending; });
    const parent = runtime(components.RegexFilterField, {
        id: 'global-model-filter', label: '全局模型过滤', value: '', saveImmediately: true,
        onChange: () => { writes++; return pending; }, onOpenChange: open => notices.push(open),
    });
    parent.render();
    find(parent, node => node.type === 'dialog-root').props.onOpenChange(true);
    parent.render();
    const child = runtime(components.ModelFilterEditor, find(parent, node => node.type === components.ModelFilterEditor).props);
    child.render();
    find(child, node => node.type === 'input').props.onChange({ target: { value: 'gpt-' } });
    child.render();
    find(child, node => node.type === 'form').props.onSubmit({ preventDefault() {}, stopPropagation() {} });
    parent.render();
    find(parent, node => node.type === 'dialog-root').props.onOpenChange(false);
    assert.equal(find(parent, node => node.type === 'dialog-root').props.open, true);
    await find(parent, node => node.type === components.ModelFilterEditor).props.onApply('duplicate');
    assert.equal(writes, 1);
    reject(new Error('fixture save failure'));
    await new Promise(resolve => setImmediate(resolve));
    parent.render();
    child.props = find(parent, node => node.type === components.ModelFilterEditor).props;
    child.render();
    assert.equal(find(parent, node => node.type === 'dialog-root').props.open, true);
    assert.equal(find(child, node => node.type === 'input').props.value, 'gpt-');
    assert.match(text(find(child, node => node.props.role === 'alert')), /草稿已保留/);
    button(child, '取消').props.onClick();
    parent.render();
    assert.equal(find(parent, node => node.type === 'dialog-root').props.open, false);
    assert.deepEqual(notices, [true, false]);
});

test('the real global settings handler changes its displayed value only after API success', async () => {
    let reject = false;
    const calls = [];
    const settings = [{ key: 'model_filter', value: 'gpt|claude' }];
    const system = loadSource('web/src/components/modules/setting/System.tsx', {
        ...ui,
        '@/components/ui/popover': { Popover: 'popover', PopoverContent: 'popover-content', PopoverTrigger: 'popover-trigger' },
        '@/components/ui/tooltip': { Tooltip: 'tooltip', TooltipContent: 'tooltip-content', TooltipTrigger: 'tooltip-trigger' },
        '@/api/setting': {
            SettingKey: { ModelFilter: 'model_filter' }, useSettingList: () => ({ data: settings }),
            useSetSetting: () => ({ mutate() {}, async mutateAsync(payload) { calls.push(plain(payload)); if (reject) throw new Error('fixture API failure'); } }),
        },
        '@/components/common/RegexFilterField': components,
        'sonner': { toast: { success() {}, error() {} } },
    });
    const fixture = runtime(system.SettingSystem, {});
    fixture.render();
    await new Promise(resolve => setImmediate(resolve));
    fixture.render();
    const field = () => find(fixture, node => node.type === components.RegexFilterField);
    assert.equal(field().props.value, 'gpt|claude');
    assert.equal(field().props.saveImmediately, true);
    reject = true;
    await assert.rejects(field().props.onChange('^gpt-'), /fixture API failure/);
    fixture.render();
    assert.equal(field().props.value, 'gpt|claude');
    reject = false;
    await field().props.onChange('^gpt-');
    fixture.render();
    assert.equal(field().props.value, '^gpt-');
    assert.deepEqual(calls, [{ key: 'model_filter', value: '^gpt-' }, { key: 'model_filter', value: '^gpt-' }]);
});
