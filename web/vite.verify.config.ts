import { defineConfig } from 'vite';
import solid from 'vite-plugin-solid';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [solid(), tailwindcss()],
  server: {
    port: 5199,
    strictPort: true,
    proxy: { '/api': 'http://localhost:7801' },
  },
});
