import path from 'node:path';
import { promisify } from 'node:util';
import { gzip, type InputType, type ZlibOptions } from 'node:zlib';
import babel from '@rolldown/plugin-babel';
import react, { reactCompilerPreset } from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import { compression, defineAlgorithm } from 'vite-plugin-compression2';

const gzipAsync = promisify(gzip);

async function portableGzip(buffer: InputType, options: ZlibOptions): Promise<Buffer> {
  const compressed = await gzipAsync(buffer, options);
  // RFC 1952: 255 means unknown OS. The assets contain no OS-specific data.
  compressed[9] = 255;
  return compressed;
}

export default defineConfig({
  base: './',
  plugins: [
    react(),
    babel({ presets: [reactCompilerPreset()] }),
    compression({
      algorithms: [defineAlgorithm(portableGzip, { level: 9 })],
      include: /\.(html|css|js|mjs|json|svg|txt|xml)$/,
      deleteOriginalAssets: true,
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  build: {
    outDir: path.resolve(import.meta.dirname, '../static/out'),
    emptyOutDir: true,
  },
  server: {
    hmr: process.env.DISABLE_HMR !== 'true',
    watch: process.env.DISABLE_HMR === 'true' ? null : {},
    proxy: {
      '/api': {
        target: process.env.VITE_PROXY_TARGET || 'http://127.0.0.1:8080',
        changeOrigin: false,
      },
    },
  },
});
