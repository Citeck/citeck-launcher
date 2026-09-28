// jsdom, but modules are transformed for SSR: .astro components render through the Container API
// (a "client" transform has no Astro renderer), while the test still gets a real DOM to run the page scripts in.
import type { Environment } from 'vitest/runtime';
import { builtinEnvironments } from 'vitest/runtime';

export default <Environment>{ ...builtinEnvironments.jsdom, name: 'jsdom-ssr', viteEnvironment: 'ssr' };
