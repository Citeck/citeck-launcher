import { experimental_AstroContainer as AstroContainer } from 'astro/container';

type Component = Parameters<AstroContainer['renderToString']>[0];

/** Render a real .astro component into document.body, the way the page ships it (scripts are started by the test). */
export async function mount(component: Component, props: Record<string, unknown> = {}): Promise<HTMLElement> {
  const container = await AstroContainer.create();
  document.body.innerHTML = await container.renderToString(component, { props });
  return document.body;
}
