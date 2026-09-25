# Deployment

The site deploys to GitHub Pages from the `pages` branch of `Citeck/citeck-launcher`.

- Workflow: `.github/workflows/static.yml` — on every push to `pages` (or manual dispatch) it runs
  `yarn install --frozen-lockfile && yarn build` on Node 22 and publishes `dist/`.
- Repository settings: **Settings → Pages → Source: GitHub Actions**.
- Base path: the site is served under `/citeck-launcher/`; `astro.config.mjs` sets `site` and `base`
  accordingly. Moving it to a custom domain means changing both.

A push to `pages` is a public release of the site: run `yarn test && yarn build && yarn verify` first.
