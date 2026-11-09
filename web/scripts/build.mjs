import { spawn } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// Vite clears the output directory before building; preserve the tracked embed sentinel.
const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const sentinelPath = path.resolve(webRoot, '..', 'static', 'out', 'README.md');
const typeScriptPath = path.resolve(webRoot, 'node_modules', 'typescript', 'bin', 'tsc');
const vitePath = path.resolve(webRoot, 'node_modules', 'vite', 'bin', 'vite.js');

function runNodeScript(scriptPath, args) {
    return new Promise((resolve, reject) => {
        const child = spawn(process.execPath, [scriptPath, ...args], {
            cwd: webRoot,
            stdio: 'inherit',
        });
        child.once('error', reject);
        child.once('exit', (code, signal) => {
            if (code === 0) {
                resolve();
                return;
            }
            reject(new Error(`${path.basename(scriptPath)} exited with ${code ?? `signal ${signal}`}`));
        });
    });
}

let sentinel;
try {
    sentinel = await readFile(sentinelPath);
} catch (error) {
    if (error?.code !== 'ENOENT') throw error;
}

try {
    await runNodeScript(typeScriptPath, ['--noEmit']);
    await runNodeScript(vitePath, ['build']);
} finally {
    if (sentinel !== undefined) {
        await mkdir(path.dirname(sentinelPath), { recursive: true });
        await writeFile(sentinelPath, sentinel);
    }
}
