# riffle.jhchen.dev

The project website: Vite, React and Mantine, served as static assets on Cloudflare Workers (`wrangler.jsonc`).

Cloudflare build settings: root directory `site`, build `npm ci && npm run build`, deploy `npx wrangler deploy`, non-production deploy `npx wrangler versions upload`.

```sh
npm ci
npm run dev
```

`public/architecture.svg` is a copy of `docs/architecture.svg`; update both together.
