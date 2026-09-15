import { defineConfig } from 'tsup';

export default defineConfig({
    entry: [
        'src/index.ts',
        'src/cli/command.ts'
    ],
    format: ['esm'],
    dts: true,
    splitting: true,
    sourcemap: true,
    clean: true,
    target: 'es2022',
})