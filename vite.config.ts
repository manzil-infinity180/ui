import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { visualizer } from "rollup-plugin-visualizer";
import EnvironmentPlugin from 'vite-plugin-environment'

// DOCS: https://www.npmjs.com/package/rollup-plugin-visualizer
// https://vitejs.dev/config/
export default defineConfig({
  plugins: [
    react(),
    EnvironmentPlugin({
      VITE_BASE_URL: process.env.VITE_BASE_URL,
    }),
    visualizer({
      filename: "bundle-stats.html", // Output file for the analysis
      open: true, // Automatically open the file in the browser if true
      gzipSize: true, // Show gzip sizes
      brotliSize: true, // Show brotli sizes
      // template: 'network', // sunburst, treemap, network, raw-data, list, flamegraph
    }),
  ],
})
