# LlamaRack frontend

The frontend is a statically generated Nuxt application. Its manager API base is resolved when Nuxt generates the bundle, using this precedence:

```text
NUXT_PUBLIC_API_BASE
    ↓
LLAMARACK_EXTERNAL_URL
    ↓
''
```

`NUXT_PUBLIC_API_BASE` is the explicit frontend override. This remains useful for local development when the frontend and backend run on different ports:

```bash
NUXT_PUBLIC_API_BASE=http://localhost:8000 npm run dev
```

When generating a production frontend directly, `LLAMARACK_EXTERNAL_URL` can provide the canonical public URL without duplicating it in `NUXT_PUBLIC_API_BASE`:

```bash
LLAMARACK_EXTERNAL_URL=https://llamarack.example.com npm run generate
```

For Docker builds, pass either value as a build argument when a non-same-origin API base needs to be embedded in the generated frontend:

```bash
docker build \
  --build-arg LLAMARACK_EXTERNAL_URL=https://llamarack.example.com \
  .
```

The fallback is build-time only. Setting `LLAMARACK_EXTERNAL_URL` when starting an already-built image still affects backend behavior such as OIDC redirects, but it does not rewrite the static Nuxt bundle. Published images built without either frontend build value keep the empty-string configuration and therefore use the existing same-origin behavior.
