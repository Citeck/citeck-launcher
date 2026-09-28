/// <reference types="vitest/config" />
import { getViteConfig } from 'astro/config';

export default getViteConfig({
  test: { environment: './test/jsdom-ssr-env.ts', include: ['src/**/*.test.ts'] },
});
