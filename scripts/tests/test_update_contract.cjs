// Run with: node --test scripts/tests/test_update_contract.cjs
// Executes the real update Hook, API client and Settings click handler in a VM.
// React rendering/timers and HTTP are fixtures; mutation lifecycle is TanStack's.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const { createRequire } = require('node:module');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

const root = path.resolve(__dirname, '../..');
const webRequire = createRequire(path.join(root, 'web/package.json'));
const ts = webRequire('typescript');
const { QueryClient, QueryObserver, MutationObserver } = webRequire('@tanstack/react-query');
const rc1 = 'v0.1.0-rc.1';
const rc2 = 'v0.1.0-rc.2';
const rc3 = 'v0.1.0-rc.3';
const plain = value => JSON.parse(JSON.stringify(value));

function loadSource(relative, imports, globals, importMeta = false) {
  const filename = path.join(root, relative);
  let source = fs.readFileSync(filename, 'utf8');
  if (importMeta) source = source.replaceAll('import.meta.env', '__fixtureImportMetaEnv');
  const compiled = ts.transpileModule(source, {
    fileName: filename,
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX },
  }).outputText;
  const module = { exports: {} };
  vm.runInNewContext(compiled, {
    module, exports: module.exports,
    require(name) {
      assert.ok(Object.hasOwn(imports, name), `Unexpected import: ${name}`);
      return imports[name];
    },
    ...globals,
  }, { filename });
  return module.exports;
}

function fixture(t) {
  let store;
  let clock = 1000;
  let stateValue;
  let effectIndex = 0;
  const effects = [];
  const timers = new Map();
  let timerID = 0;
  const requests = [];
  const invalidations = [];
  const queryOptions = new Map();
  const cleanup = [];
  const server = { tag: rc2, installed: rc1, status: 'available' };
  const queryClient = new QueryClient({ defaultOptions: {
    queries: { retry: false, gcTime: Infinity }, mutations: { retry: false, gcTime: Infinity },
  } });
  const invalidate = queryClient.invalidateQueries.bind(queryClient);
  queryClient.invalidateQueries = options => {
    invalidations.push([...options.queryKey]);
    return invalidate(options);
  };
  const globals = {
    Date: class extends Date { static now() { return clock; } },
    Headers,
    window: {
      setInterval(fn, delay) { assert.equal(delay, 1000); timers.set(++timerID, fn); return timerID; },
      clearInterval(id) { timers.delete(id); },
      dispatchEvent() {},
    },
    fetch: async (url, options) => {
      requests.push({ url, method: options.method, body: options.body, headers: options.headers });
      const respond = (status, data) => new Response(JSON.stringify({ code: status, message: status === 200 ? 'success' : 'synthetic update rejection', ...(status === 200 ? { data } : {}) }), { status });
      if (options.method === 'POST') {
        if (server.postError) return respond(server.postError);
        if (Object.hasOwn(server, 'receipt')) return respond(200, server.receipt);
        const target = JSON.parse(options.body || '{}').target_version;
        if (target !== server.tag) return respond(409);
        return respond(200, { status: 'update_pending', target_version: target });
      }
      if (url.endsWith('/now-version')) return respond(200, server.installed);
      return respond(200, { current_version: server.installed, tag_name: server.tag,
        status: server.status, can_update: server.status === 'available', message: '', published_at: '', body: '' });
    },
  };
  const client = loadSource('web/src/api/client.ts', { '@tanstack/react-query': { QueryClient } }, globals);
  let observer;
  let mutation;
  const api = loadSource('web/src/api/update.ts', {
    '@tanstack/react-query': {
      useQueryClient: () => queryClient,
      useQuery(options) {
        queryOptions.set(options.queryKey.join('/'), options);
        return { data: queryClient.getQueryData(options.queryKey), isLoading: false };
      },
      useMutation(options) {
        if (!observer) {
          observer = new MutationObserver(queryClient, options);
          cleanup.push(observer.subscribe(() => {}));
          mutation = {
            mutate: (variables, callbacks) => observer.mutate(variables, callbacks),
            get isPending() { return observer.getCurrentResult().isPending; },
            get isError() { return observer.getCurrentResult().isError; },
          };
        } else observer.setOptions(options);
        return mutation;
      },
    },
    react: {
      useState(initial) {
        if (stateValue === undefined) stateValue = initial();
        return [stateValue, value => { stateValue = value; }];
      },
      useEffect(fn, dependencies) {
        const slot = effectIndex++;
        const previous = effects[slot];
        if (previous && dependencies.every((value, index) => Object.is(value, previous.dependencies[index]))) return;
        previous?.dispose?.();
        effects[slot] = { dependencies, dispose: fn() };
      },
    },
    zustand: {
      create(initializer) {
        store = initializer();
        const hook = selector => selector(store);
        hook.setState = partial => Object.assign(store, partial);
        return hook;
      },
    },
    './client': client,
  }, globals);
  const Button = Symbol('Button');
  const jsx = (type, props) => ({ type, props });
  const settings = loadSource('web/src/components/modules/setting/Info.tsx', {
    'react/jsx-runtime': { jsx, jsxs: jsx },
    'use-intl': { useTranslations: () => key => key },
    'lucide-react': { Info: 'Info', Tag: 'Tag', AlertTriangle: 'AlertTriangle', Loader2: 'Loader2', Download: 'Download' },
    '@thesvg/react/github': { default: 'Github' },
    '@/api/update': api,
    '@/components/ui/button': { Button },
  }, { ...globals, __fixtureImportMetaEnv: { VITE_APP_VERSION: rc1 } }, true);
  function elements(value, predicate) {
    if (Array.isArray(value)) return value.flatMap(item => elements(item, predicate));
    if (!value || typeof value !== 'object') return [];
    return [...(predicate(value) ? [value] : []), ...elements(value.props?.children, predicate)];
  }
  function render() {
    effectIndex = 0;
    const tree = settings.SettingInfo();
    const buttons = elements(tree, node => node.type === Button);
    const updateButton = buttons.find(button => button.props.disabled !== undefined);
    const displayedVersions = elements(tree, node => node.type === 'code').map(node => node.props.children);
    return { tree, updateButton, displayedVersions };
  }
  function progress() {
    effectIndex = 0;
    return api.useUpdateProgress();
  }
  function assertIntervals(expected) {
    api.useLatestInfo();
    api.useNowVersion();
    for (const options of queryOptions.values()) assert.equal(options.refetchInterval, expected);
  }
  t.after(() => {
    for (const effect of effects) effect?.dispose?.();
    for (const dispose of cleanup) dispose();
    queryClient.clear();
  });
  return {
    api, client, server, requests, invalidations, queryClient, timers, render, progress, assertIntervals,
    get store() { return store; },
    get mutation() { return api.useUpdateCore(); },
    posts: () => requests.filter(request => request.method === 'POST'),
    async start() {
      api.useLatestInfo();
      api.useNowVersion();
      const queries = [...queryOptions.values()].map(options => new QueryObserver(queryClient, options));
      for (const query of queries) cleanup.push(query.subscribe(() => {}));
      await Promise.all(queries.map(query => query.refetch()));
    },
    async refresh() { await queryClient.refetchQueries(); },
    tick(time) { clock = time; for (const callback of timers.values()) callback(); return progress(); },
  };
}

test('actual Settings click: conflict refreshes both versions, then a second click accepts the refreshed target', async t => {
  const h = fixture(t);
  await h.start();
  const stalePage = h.render();
  assert.deepEqual(stalePage.displayedVersions, [rc1, rc2]);
  h.server.tag = rc3;
  await assert.rejects(stalePage.updateButton.props.onClick(), error => error instanceof h.client.ApiError && error.status === 409);
  assert.equal(h.posts().length, 1);
  assert.deepEqual(JSON.parse(h.posts()[0].body), { target_version: rc2 });
  assert.equal(h.posts()[0].headers.get('Content-Type'), 'application/json');
  assert.deepEqual(h.invalidations, [['update', 'latest'], ['update', 'now-version']]);
  assert.equal(h.store.attempt, null);
  assert.equal(h.progress(), 'idle');
  assert.equal(h.timers.size, 0);
  h.assertIntervals(3600000);
  assert.equal(h.mutation.isError, true);
  const refreshedPage = h.render();
  assert.deepEqual(refreshedPage.displayedVersions, [rc1, rc3]);
  assert.equal(refreshedPage.updateButton.props.disabled, false);
  assert.equal(h.posts().length, 1, 'refresh must not automatically submit again');
  await refreshedPage.updateButton.props.onClick();
  assert.equal(h.posts().length, 2);
  assert.deepEqual(JSON.parse(h.posts()[1].body), { target_version: rc3 });
  assert.deepEqual(plain(h.store.attempt), { targetVersion: rc3, previousVersion: rc1, deadline: 31000 });
  assert.equal(h.progress(), 'pending');
  assert.equal(h.timers.size, 1);
  h.assertIntervals(1000);
  h.server.installed = rc3;
  h.server.status = 'latest';
  await h.refresh();
  assert.equal(h.progress(), 'completed');
  assert.equal(h.timers.size, 0);
  h.assertIntervals(3600000);
});

for (const [name, receipt] of [
  ['missing data', undefined], ['null', null], ['missing fields', {}], ['old string', 'update_pending'],
  ['wrong status', { status: 'latest', target_version: rc2 }],
  ['missing status', { target_version: rc2 }], ['missing target', { status: 'update_pending' }],
  ['empty target', { status: 'update_pending', target_version: '' }],
  ['wrong target type', { status: 'update_pending', target_version: 2 }],
  ['different target', { status: 'update_pending', target_version: rc3 }],
]) {
  test(`invalid receipt (${name}) fails before success callbacks or pending state`, async t => {
    const h = fixture(t);
    await h.start();
    h.server.receipt = receipt;
    let success = 0;
    let errors = 0;
    await assert.rejects(h.mutation.mutate({ targetVersion: rc2, previousVersion: rc1 }, {
      onSuccess() { success++; }, onError() { errors++; },
    }));
    assert.equal(success, 0);
    assert.equal(errors, 1);
    assert.equal(h.store.attempt, null);
    assert.equal(h.progress(), 'idle');
    assert.equal(h.timers.size, 0);
    assert.deepEqual(h.invalidations, [['update', 'latest'], ['update', 'now-version']]);
    assert.equal(h.posts().length, 1);
    assert.equal(h.mutation.isError, true);
  });
}

for (const actual of [rc1, rc3]) {
  test(`accepted ${rc2} does not complete at ${actual}, and timeout stops short polling`, async t => {
    const h = fixture(t);
    await h.start();
    await h.mutation.mutate({ targetVersion: rc2, previousVersion: rc1 });
    assert.equal(h.progress(), 'pending');
    h.server.installed = actual;
    await h.refresh();
    assert.equal(h.tick(30999), 'pending');
    h.assertIntervals(1000);
    assert.equal(h.tick(31000), 'unconfirmed');
    assert.equal(h.timers.size, 0);
    h.assertIntervals(3600000);
  });
}

test('exact target observed after timeout still confirms completion without short polling', async t => {
  const h = fixture(t);
  await h.start();
  await h.mutation.mutate({ targetVersion: rc2, previousVersion: rc1 });
  assert.equal(h.progress(), 'pending');
  assert.equal(h.tick(31000), 'unconfirmed');
  h.server.installed = rc2;
  h.server.status = 'latest';
  await h.refresh();
  assert.equal(h.progress(), 'completed');
  assert.equal(h.timers.size, 0);
  h.assertIntervals(3600000);
});

test('update_failed wins over exact version equality and stops short polling', async t => {
  const h = fixture(t);
  await h.start();
  await h.mutation.mutate({ targetVersion: rc2, previousVersion: rc1 });
  assert.equal(h.progress(), 'pending');
  h.server.installed = rc2;
  h.server.status = 'update_failed';
  await h.refresh();
  assert.equal(h.progress(), 'failed');
  assert.equal(h.timers.size, 0);
  h.assertIntervals(3600000);
});

test('unrelated API failure creates no attempt or automatic retry', async t => {
  const h = fixture(t);
  await h.start();
  h.server.postError = 429;
  await assert.rejects(h.mutation.mutate({ targetVersion: rc2, previousVersion: rc1 }), error => error.status === 429);
  assert.equal(h.store.attempt, null);
  assert.equal(h.posts().length, 1);
  assert.deepEqual(h.invalidations, []);
});

test('no attempt stays idle; explicit reset removes confirmation and polling', async t => {
  const h = fixture(t);
  await h.start();
  assert.equal(h.api.resolveUpdatePhase(null, rc2, 'update_failed', 999999), 'idle');
  await h.mutation.mutate({ targetVersion: rc2, previousVersion: rc1 });
  assert.equal(h.progress(), 'pending');
  h.api.resetUpdateAttempt();
  assert.equal(h.progress(), 'idle');
  assert.equal(h.store.attempt, null);
  assert.equal(h.timers.size, 0);
  h.assertIntervals(3600000);
});
