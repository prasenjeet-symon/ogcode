import { defineConfig } from 'vite';
import solidPlugin from 'vite-plugin-solid';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [solidPlugin(), tailwindcss()],
  build: {
    outDir: 'dist',
  },
  server: {
    port: 5199,
    strictPort: true,
    proxy: {
      '/api': 'http://localhost:7801',
    },
  },
});