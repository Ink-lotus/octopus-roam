// Offline acceptance of the frozen Windows archive and the production update
// path. Only the acceptance update builds inject a loopback release transport
// through go -overlay. Hash/package checks, API handlers, scheduler, helper,
// shutdown, restart and SQLite are the production implementations.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { createWriteStream } from 'node:fs';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const candidate = path.resolve(process.argv[2] || '');
const output = path.resolve(process.argv[3] || '');
assert(process.argv[2] && process.argv[3], 'Usage: node scripts/test-release-acceptance.mjs <frozen-candidate> <new-output-directory>');
assert(output.startsWith(path.join(repository, 'build', 'release-local') + path.sep), 'Output must be below build/release-local.');
const runRoot = path.join(output, 'runtime-acceptance');
await fs.mkdir(runRoot); // Refuse to reuse a run.
await fs.mkdir(path.join(runRoot, 'logs'));
await fs.mkdir(path.join(runRoot, 'stage'));
const archive = path.join(candidate, 'archives', 'octopus-roam-windows-amd64.zip');
const source = path.join(candidate, 'source');
const operationScript = path.join(repository, 'scripts', 'acceptance', 'runtime.ps1');
const checks = [];
const runtimes = [];
const sourceCalls = [];
const upstreamCalls = [];
const logs = new Map();
const baseEnv = { ...process.env };
for (const name of Object.keys(baseEnv)) {
    if (/^OCTOPUS_ROAM_/i.test(name) || /^(HTTP_PROXY|HTTPS_PROXY|ALL_PROXY|GOFLAGS|GOARM|GOOS|GOARCH|CGO_ENABLED)$/i.test(name)) delete baseEnv[name];
}
Object.assign(baseEnv, {
    OCTOPUS_ROAM_SKIP_STARTUP_PRICE_UPDATE: '1', GOTOOLCHAIN: 'local', GOPROXY: 'off',
    GOCACHE: path.join(repository, '.tmp-go-cache'), GOMODCACHE: path.join(repository, '.tmp-go-mod'),
    GOOS: 'windows', GOARCH: 'amd64', CGO_ENABLED: '0',
    TEMP: path.join(runRoot, 'stage'), TMP: path.join(runRoot, 'stage'),
});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const sha = data => createHash('sha256').update(data).digest('hex');
async function jsonFile(name, value) { await fs.writeFile(path.join(runRoot, name), JSON.stringify(value, null, 2) + '\n'); }
async function until(predicate, description, timeout = 20000) {
    const end = Date.now() + timeout;
    while (Date.now() < end) { try { const value = await predicate(); if (value) return value; } catch { /* Wait for readiness/restart. */ } await delay(150); }
    throw Error('Timed out: ' + description);
}
async function run(tool, args, name, env = baseEnv, cwd = source) {
    const logPath = path.join(runRoot, 'logs', name + '.log');
    const sink = createWriteStream(logPath, { flags: 'wx' });
    const child = spawn(tool, args, { cwd, env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
    let text = '';
    child.stdout.on('data', data => { sink.write(data); text += data; });
    child.stderr.on('data', data => { sink.write(data); text += data; });
    // A separately launched Windows runtime can inherit a pipe handle even
    // after its PowerShell launcher exits. Wait for the launcher, then release
    // our pipe readers; do not wait for the lifetime of the server's handles.
    const code = await new Promise((resolve, reject) => { child.on('error', reject); child.on('exit', resolve); });
    child.stdout.destroy(); child.stderr.destroy();
    await new Promise(resolve => sink.end(resolve));
    await fs.appendFile(path.join(runRoot, 'commands.jsonl'), JSON.stringify({ tool, arguments: args, cwd, exit_code: code, log: path.relative(runRoot, logPath) }) + '\n');
    assert.equal(code, 0, name + ' failed; see its log.');
    return text;
}
async function operation(action, directory, extra = [], name = action.toLowerCase(), env = baseEnv) {
    return run('pwsh', ['-NoProfile', '-File', operationScript, '-Action', action, '-Directory', directory, ...extra], name, env, repository);
}
async function check(name, fn) {
    const started = Date.now();
    try { const details = await fn(); checks.push({ name, status: 'passed', duration_ms: Date.now() - started, ...details }); console.log('PASS ' + name); }
    catch (error) { checks.push({ name, status: 'failed', error: error.message }); throw error; }
    finally { await jsonFile('results.json', checks); }
}
async function request(runtime, endpoint, method = 'GET', body, expected = 200) {
    const response = await fetch(runtime.url + endpoint, { method, headers: { ...(runtime.cookie ? { Cookie: runtime.cookie } : {}), ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) }, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(10000) });
    assert.equal(response.status, expected, method + ' ' + endpoint);
    if (endpoint.endsWith('/login')) runtime.cookie = response.headers.get('set-cookie').split(';')[0];
    if (expected === 204) return null;
    return (await response.json()).data;
}
async function listen(server) {
    await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
    return 'http://127.0.0.1:' + server.address().port;
}
async function freePort() { const probe = createServer(); const url = await listen(probe); const port = Number(new URL(url).port); await new Promise(resolve => probe.close(resolve)); return port; }
async function makeRuntime(name, template, releaseBase = '') {
    const directory = path.join(runRoot, '运行目录 ' + name);
    await fs.cp(template, directory, { recursive: true, errorOnExist: true, force: false });
    const dataDir = path.join(directory, '配置 目录');
    await fs.mkdir(dataDir);
    const config = path.join(dataDir, '配置 文件.json');
    const port = await freePort();
    await fs.writeFile(config, JSON.stringify({ server: { host: '127.0.0.1', port }, database: { type: 'sqlite', path: path.join(dataDir, 'data.db') }, log: { level: 'warn' } }));
    const env = { ...baseEnv, OCTOPUS_ROAM_ACCEPTANCE_RELEASE_URL: releaseBase };
    const runtime = { name, directory, config, url: 'http://127.0.0.1:' + port, env };
    runtimes.push(runtime);
    runtime.originalPID = JSON.parse(await operation('Start', directory, ['-Config', config], 'start-' + name, env)).id;
    await until(async () => (await fetch(runtime.url, { signal: AbortSignal.timeout(1000) })).ok, 'start ' + name);
    await request(runtime, '/api/v1/user/login', 'POST', { username: 'admin', password: 'admin', expire: -1 });
    return runtime;
}
async function status(runtime, suffix = '') { return JSON.parse(await operation('Status', runtime.directory, [], 'status-' + runtime.name + suffix)); }
async function stop(runtime, suffix = '') { return operation('Stop', runtime.directory, [], 'stop-' + runtime.name + suffix); }
async function watchLogs(runtime, signal) {
    const response = await fetch(runtime.url + '/api/v1/log/overview/stream', { headers: { Cookie: runtime.cookie }, signal });
    assert.equal(response.status, 200);
    let pending = '';
    for await (const chunk of response.body) {
        pending += Buffer.from(chunk).toString('utf8');
        let index;
        while ((index = pending.indexOf('\n\n')) >= 0) {
            const event = pending.slice(0, index); pending = pending.slice(index + 2);
            const value = event.split('\n').filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n');
            if (value) { const state = JSON.parse(value); logs.set(state.id, state); }
        }
    }
}
function recent(model, terminal) { return [...logs.values()].filter(item => item.model === model && (!terminal || item.status === terminal)).sort((a, b) => b.id - a.id)[0]; }
async function channel(runtime, label, url) {
    await request(runtime, '/api/v1/channel/create', 'POST', { name: 'acceptance-' + label, base_url: url, enabled: true, allowed_protocols: 2, follow_global_auto_update: false, keys: [{ name: 'fixture-key', key: 'synthetic-upstream-only', enabled: true }], models: ['fixture-model'], grants: [{ model_name: 'fixture-model', key_name: 'fixture-key', protocols: 2 }] });
    const grants = await request(runtime, '/api/v1/channel/grants');
    return grants.find(item => item.channel_name === 'acceptance-' + label).id;
}
async function group(runtime, name, grants, mode = 'manual') {
    const created = await request(runtime, '/api/v1/group/create', 'POST', { name, mode, relay_config: { member_max_attempts: 1, member_retry_interval_seconds: 0, member_non_stream_response_timeout_seconds: 15, member_stream_first_event_timeout_seconds: 10, member_cooldown_seconds: 1, member_affinity_seconds: 0, retry_mode: 'fixed', retry_initial_interval_seconds: 1, retry_max_interval_seconds: 1, manual_max_attempts: 1 }, items: grants.map(channel_grant_id => ({ channel_grant_id })) });
    if (mode === 'manual') return request(runtime, '/api/v1/group/update/' + created.id, 'POST', { active_item_id: created.items[0].id });
    return created;
}
function chat(runtime, model, stream = false, signal = AbortSignal.timeout(20000)) {
    return fetch(runtime.url + '/v1/chat/completions', { method: 'POST', headers: { Authorization: 'Bearer synthetic-acceptance-key', 'Content-Type': 'application/json' }, body: JSON.stringify({ model, stream, messages: [{ role: 'user', content: 'offline acceptance' }] }), signal });
}

let cancelConnections = 0;
const packages = new Map();
const fixture = createServer(async (req, res) => {
    const url = new URL(req.url, 'http://127.0.0.1');
    if (/^\/(success|rollback|unconfirmed)\/(releases|download)/.test(url.pathname)) {
        sourceCalls.push({ method: req.method, path: url.pathname });
        const scenario = url.pathname.split('/')[1];
        if (url.pathname.endsWith('/releases')) {
            res.setHeader('Content-Type', 'application/json');
            res.end(JSON.stringify([{ tag_name: 'v0.1.0-rc.2', prerelease: true, draft: false, body: 'Loopback acceptance only' }]));
        } else {
            const pkg = packages.get(scenario);
            if (url.pathname.endsWith('/SHA256SUMS')) res.end(sha(pkg) + '  ./octopus-roam-windows-amd64.zip\n');
            else if (url.pathname.endsWith('/octopus-roam-windows-amd64.zip')) { res.setHeader('Content-Type', 'application/zip'); res.end(pkg); }
            else { res.statusCode = 404; res.end(); }
        }
        return;
    }
    const body = [];
    for await (const chunk of req) body.push(chunk);
    const value = body.length ? JSON.parse(Buffer.concat(body)) : {};
    const kind = url.pathname.split('/')[1];
    upstreamCalls.push({ kind, stream: !!value.stream, model: value.model || '' });
    if (kind === 'slow') { res.on('close', () => { cancelConnections++; }); return; }
    if (kind === 'fail') { res.writeHead(503, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ error: { message: 'synthetic upstream unavailable', type: 'server_error' } })); return; }
    if (url.pathname.endsWith('/models')) { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ object: 'list', data: [{ id: 'fixture-model', object: 'model' }] })); return; }
    if (kind !== 'good') { res.statusCode = 404; res.end(); return; }
    const usage = { prompt_tokens: 3, completion_tokens: 2, total_tokens: 5 };
    if (!value.stream) { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ id: 'chatcmpl-local', object: 'chat.completion', created: 1, model: 'fixture-model', choices: [{ index: 0, message: { role: 'assistant', content: 'local acceptance pong' }, finish_reason: 'stop' }], usage })); return; }
    res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' });
    for (const delta of [{ role: 'assistant' }, { content: 'local acceptance ' }, { content: 'pong' }]) {
        res.write('data: ' + JSON.stringify({ id: 'chatcmpl-local', object: 'chat.completion.chunk', created: 1, model: 'fixture-model', choices: [{ index: 0, delta, finish_reason: null }] }) + '\n\n');
        await delay(40);
    }
    res.end('data: ' + JSON.stringify({ id: 'chatcmpl-local', object: 'chat.completion.chunk', created: 1, model: 'fixture-model', choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], usage }) + '\n\ndata: [DONE]\n\n');
});
const logAbort = new AbortController();
let logTask;
let logFailure;
try {
    const fixtureURL = await listen(fixture);
    await check('frozen_archive_hash_and_extract', async () => {
        const metadata = JSON.parse(await fs.readFile(path.join(candidate, 'build-info.json')));
        assert.equal(sha(await fs.readFile(archive)), metadata.archives.find(item => item.name === path.basename(archive)).sha256);
        await operation('Extract', path.join(runRoot, 'frozen-package'), ['-Archive', archive], 'extract-frozen');
        assert.equal(sha(await fs.readFile(path.join(runRoot, 'frozen-package', 'octopus-roam.exe'))), metadata.binaries.find(item => item.target === 'windows/amd64').sha256);
        return { candidate: path.relative(repository, candidate), archive_sha256: sha(await fs.readFile(archive)) };
    });
    const business = await makeRuntime('业务 测试', path.join(runRoot, 'frozen-package'));
    logTask = watchLogs(business, logAbort.signal).catch(error => { if (!logAbort.signal.aborted) logFailure = error; });
    await check('ordinary_stream_failover_cancel_logs_stats', async () => {
        const good = await channel(business, 'good', fixtureURL + '/good');
        const fail = await channel(business, 'fail', fixtureURL + '/fail');
        const slow = await channel(business, 'slow', fixtureURL + '/slow');
        await group(business, 'acceptance-chat', [good]);
        await group(business, 'acceptance-failover', [fail, good], 'failover');
        await group(business, 'acceptance-cancel', [slow]);
        await request(business, '/api/v1/apikey/create', 'POST', { name: 'acceptance-api-key', api_key: 'synthetic-acceptance-key', enabled: true });
        const normal = await chat(business, 'acceptance-chat');
        assert.equal(normal.status, 200);
        assert.equal((await normal.json()).choices[0].message.content, 'local acceptance pong');
        const streaming = await chat(business, 'acceptance-chat', true);
        assert.equal(streaming.status, 200);
        assert.match(streaming.headers.get('content-type'), /text\/event-stream/);
        const streamBody = await streaming.text();
        assert.match(streamBody, /local acceptance /); assert.match(streamBody, /pong/); assert.match(streamBody, /\[DONE\]/);
        const beforeFailover = upstreamCalls.length;
        const failedOver = await chat(business, 'acceptance-failover');
        assert.equal(failedOver.status, 200); assert.match(await failedOver.text(), /local acceptance pong/);
        assert.deepEqual(upstreamCalls.slice(beforeFailover).map(item => item.kind), ['fail', 'good']);
        const clientAbort = new AbortController();
        const clientRequest = chat(business, 'acceptance-cancel', false, clientAbort.signal).catch(() => null);
        await until(() => upstreamCalls.filter(item => item.kind === 'slow').length === 1, 'client cancellation upstream');
        clientAbort.abort(); await clientRequest;
        await until(() => recent('acceptance-cancel', 'canceled') && cancelConnections >= 1, 'client cancellation state');
        const previousCancelID = recent('acceptance-cancel').id;
        const adminRequest = chat(business, 'acceptance-cancel').then(async response => { await response.text(); }).catch(() => null);
        const active = await until(() => { const latest = recent('acceptance-cancel'); return latest?.id > previousCancelID && latest.status === 'running' && latest.sending ? latest : null; }, 'admin cancellation request ID');
        await request(business, '/api/v1/log/stop/' + active.id, 'POST', undefined, 204);
        await adminRequest;
        await until(() => logs.get(active.id)?.status === 'canceled' && cancelConnections >= 2, 'admin cancellation state');
        const failoverLog = await until(() => recent('acceptance-failover', 'success'), 'success log');
        assert.equal(failoverLog.target_channel, 'acceptance-good'); assert(failoverLog.error_count >= 1);
        const stats = await request(business, '/api/v1/stats/total');
        assert.equal(stats.request_success, 3); assert.equal(stats.request_failed, 2);
        assert.equal(stats.input_token, 9); assert.equal(stats.output_token, 6);
        const channelStats = await request(business, '/api/v1/channel/stats');
        assert.equal(channelStats.find(item => item.channel_name === 'acceptance-fail').request_failed, 1);
        assert.equal(channelStats.find(item => item.channel_name === 'acceptance-slow').request_failed, 0);
        await jsonFile('business-evidence.json', { upstream_calls: upstreamCalls, canceled_upstream_connections: cancelConnections, stats, requests: [...logs.values()].map(({ id, model, status, target_channel, error_count, protocol, usage }) => ({ id, model, status, target_channel, error_count, protocol, usage })) });
        return { ordinary: true, stream: true, ordered_failover: true, client_cancel: true, api_cancel: true, sse_logs: true, exact_usage: true };
    });
    assert.equal(logFailure, undefined, 'SSE log connection failed during business acceptance.');
    logAbort.abort(); await logTask;
    await check('normal_stop_and_restart_persistence', async () => {
        await stop(business, '-first');
        business.originalPID = JSON.parse(await operation('Start', business.directory, ['-Config', business.config], 'restart-business', business.env)).id;
        await until(async () => (await fetch(business.url, { signal: AbortSignal.timeout(1000) })).ok, 'business restart');
        await request(business, '/api/v1/user/login', 'POST', { username: 'admin', password: 'admin', expire: -1 });
        const stats = await request(business, '/api/v1/stats/total');
        assert.equal(stats.request_success, 3); assert.equal(stats.request_failed, 2); assert.equal(stats.input_token, 9);
        assert.equal((await request(business, '/api/v1/group/list')).length, 3);
        return { normal_exit: true, stats_persisted: true, groups_persisted: true };
    });
    await check('build_loopback_update_acceptance_packages', async () => {
        const overlayRoot = path.join(runRoot, 'overlay'); await fs.mkdir(overlayRoot);
        const original = path.join(source, 'internal', 'update', 'release.go');
        const replacement = path.join(overlayRoot, 'release.go');
        await fs.writeFile(replacement, (await fs.readFile(original, 'utf8')) + `
// Acceptance-only overlay. No production source or artifact is changed.
func init() {
    source := os.Getenv("OCTOPUS_ROAM_ACCEPTANCE_RELEASE_URL")
    target, err := url.Parse(source)
    if err != nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" || target.User != nil { panic("acceptance requires a loopback release source") }
    releaseAPIEndpoint = source + "/releases"
    releaseDownloadEndpoint = source + "/download"
    testHTTPClient = &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}}
}
`);
        const overlay = path.join(overlayRoot, 'overlay.json');
        await fs.writeFile(overlay, JSON.stringify({ Replace: { [original]: replacement } }));
        for (const [name, version] of [['old', 'v0.1.0-rc.1'], ['next', 'v0.1.0-rc.2']]) {
            const template = path.join(runRoot, name + '-package');
            await fs.cp(path.join(runRoot, 'frozen-package'), template, { recursive: true, errorOnExist: true, force: false });
            // The copied binary is an acceptance input, replaced only in this new directory.
            await run('go', ['build', '-mod=readonly', '-trimpath', '-tags=jsoniter', '-overlay', overlay, '-ldflags=-s -w -X github.com/Ink-lotus/octopus-roam/internal/conf.Version=' + version, '-o', path.join(template, 'octopus-roam.exe'), '.'], 'build-' + name);
        }
        const failed = path.join(runRoot, 'failed-package');
        await fs.cp(path.join(runRoot, 'next-package'), failed, { recursive: true, errorOnExist: true, force: false });
        const failureSource = path.join(overlayRoot, 'failed.go'); await fs.writeFile(failureSource, 'package main\nimport "os"\nfunc main(){os.Exit(17)}\n');
        await run('go', ['build', '-mod=readonly', '-trimpath', '-o', path.join(failed, 'octopus-roam.exe'), failureSource], 'build-immediate-failure');
        const scenarios = { success: 'next-package', rollback: 'failed-package', unconfirmed: 'old-package' };
        for (const [scenario, directory] of Object.entries(scenarios)) {
            const zipPath = path.join(runRoot, scenario + '.zip');
            await operation('Pack', path.join(runRoot, directory), ['-Archive', zipPath], 'pack-' + scenario);
            packages.set(scenario, await fs.readFile(zipPath));
        }
        return { source: path.relative(repository, source), overlay_sha256: sha(await fs.readFile(replacement)), injection: 'loopback release transport only; production API, checksum verification and Windows helper retained', packages: Object.fromEntries([...packages].map(([name, data]) => [name, sha(data)])) };
    });
    const updateRuntimes = [];
    for (const scenario of ['success', 'rollback', 'unconfirmed']) {
        const runtime = await makeRuntime(scenario, path.join(runRoot, 'old-package'), fixtureURL + '/' + scenario);
        await request(runtime, '/api/v1/setting/set', 'POST', { key: 'stats_save_interval', value: '13' });
        const info = await request(runtime, '/api/v1/update');
        assert.equal(info.status, 'available'); assert.equal(info.can_update, true);
        updateRuntimes.push(runtime);
    }
    await jsonFile('live-state.json', { status: 'ready_for_browser', business: business.url, updates: Object.fromEntries(updateRuntimes.map(runtime => [runtime.name, runtime.url])), note: 'Fresh synthetic instances; login uses factory admin credentials. Click update in Settings for each update scenario, then create browser-done.json.' });
    console.log('READY_FOR_BROWSER ' + path.join(runRoot, 'live-state.json'));
    // Browser interaction is intentionally a separate, reviewable step. A
    // replay can use real API POSTs, but must then record browser checks absent.
    await until(async () => { await fs.access(path.join(runRoot, 'browser-done.json')); return true; }, 'browser acceptance completion', 2700000);
    for (const runtime of updateRuntimes) {
        await check('windows_production_update_' + runtime.name, async () => {
            const expectedVersion = runtime.name === 'success' ? 'v0.1.0-rc.2' : 'v0.1.0-rc.1';
            await until(async () => { const state = await status(runtime, '-' + Date.now()); return state.ids.length === 1 && state.ids[0] !== runtime.originalPID; }, runtime.name + ' new process', 25000);
            await until(async () => await request(runtime, '/api/v1/update/now-version') === expectedVersion, runtime.name + ' backend version');
            const info = await request(runtime, '/api/v1/update');
            assert.equal(info.status, runtime.name === 'rollback' ? 'update_failed' : runtime.name === 'success' ? 'latest' : 'available');
            assert.equal((await request(runtime, '/api/v1/setting/list')).find(item => item.key === 'stats_save_interval').value, '13');
            const actualHash = sha(await fs.readFile(path.join(runtime.directory, 'octopus-roam.exe')));
            const expectedBinary = path.join(runRoot, runtime.name === 'success' ? 'next-package' : 'old-package', 'octopus-roam.exe');
            assert.equal(actualHash, sha(await fs.readFile(expectedBinary)));
            assert((await fs.readdir(path.join(runtime.directory, 'backups'))).some(name => name.startsWith('before-update-')));
            for (const asset of ['octopus-roam-windows-amd64.zip', 'SHA256SUMS']) assert(sourceCalls.some(item => item.path === '/' + runtime.name + '/download/v0.1.0-rc.2/' + asset));
            return { expected_version: expectedVersion, status: info.status, executable_sha256: actualHash, chinese_directory_and_config: true, real_process_replaced: true, setting_persisted: true };
        });
    }
    await jsonFile('release-source-requests.json', sourceCalls);
    await jsonFile('summary.json', { status: 'passed', checks: checks.length, frozen_candidate: path.relative(repository, candidate), browser_evidence: 'browser-done.json', boundaries: ['Loopback HTTP only', 'Synthetic SQLite only', 'Frozen A2 artifacts unchanged', 'Acceptance update binaries inject release transport and RC version only', 'No license installation, CI or remote repository changes'] });
} catch (error) {
    console.error(error.message);
    await jsonFile('failure-observations.json', { upstream_calls: upstreamCalls, source_calls: sourceCalls, requests: [...logs.values()].map(({ id, model, status, target_channel, error_count, sending }) => ({ id, model, status, target_channel, error_count, sending })) });
    await jsonFile('summary.json', { status: 'failed', error: error.message, completed_checks: checks.length });
    process.exitCode = 1;
} finally {
    logAbort.abort();
    if (logTask) await logTask.catch(() => {});
    const cleanup = [];
    for (const runtime of runtimes) {
        try { await stop(runtime, '-final'); cleanup.push({ name: runtime.name, normal_stop: true }); }
        catch (error) { cleanup.push({ name: runtime.name, normal_stop: false, error: error.message }); process.exitCode = 1; }
    }
    await jsonFile('process-cleanup.json', cleanup);
    fixture.closeAllConnections();
    await new Promise(resolve => fixture.close(resolve));
    console.log('ARTIFACTS ' + runRoot);
}
