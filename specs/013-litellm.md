# 013 — LiteLLM Proxy catalog sync

Status: Draft

Related issue: #110

## 1. Purpose

LlamaRack can publish **enabled Instances** into a self-hosted LiteLLM Proxy so LiteLLM clients can route through LiteLLM while inference still executes on the exact LlamaRack Instance worker.

LlamaRack remains the inference gateway. LiteLLM is a catalog/discovery front door only.

## 2. Goals

- Operators configure a LiteLLM Proxy URL and master/admin API key from **Administration → LiteLLM**.
- LlamaRack publishes enabled Instances (same set as `/v1/models`) into LiteLLM and keeps owned rows in sync.
- Public LiteLLM model names use mutable Instance slugs while ownership metadata uses immutable Instance IDs.
- A hidden inference principal named `LiteLLM` authenticates LiteLLM → LlamaRack `/v1` calls.
- Instance CRUD never fails when LiteLLM is unavailable; sync is best-effort and asynchronous.
- Disconnect removes stored secrets and the hidden principal; optional unpublish deletes owned LiteLLM rows first.

## 3. Non-goals

- Running LiteLLM inside LlamaRack.
- Managing non-LlamaRack models in LiteLLM.
- Exposing the hidden LiteLLM service account in public service-account list/detail APIs.

## 4. Persistence

### Manager settings (`manager_settings`)

- `litellm_proxy_url` — operator-configured LiteLLM Proxy base URL (http/https only).
- `litellm_api_base` — LlamaRack OpenAI base URL as LiteLLM should call it. Defaults to General `external_url` + `/v1` when empty.
- `litellm_last_sync` — JSON status: `at`, `ok`, `error`, `published`, `unpublished`.

### Provider secrets (`provider_secrets`)

- `litellm_proxy_api_key` — encrypted operator key; status APIs return `configured` + prefix only.
- `litellm_inference_api_key` — encrypted copy of the generated LlamaRack `sk-` secret used in published `litellm_params.api_key`.

### Hidden principal

- Hidden service account `LiteLLM` (`service_accounts.hidden = 1`).
- One inference API key named `LiteLLM`. It is listed on `GET /api/v1/api-keys`; there is no `hidden` column on `api_keys`. Operators may set its immutable-ID `instance_ids` allowlist (empty means all instances).
- Public service-account list/get/patch/delete routes return **404** for the hidden account. Creating another key owned by that account through public routes also returns **404**.
- The managed key's name and owner cannot be changed (400). Public rotate of that key returns 404; rotate is available only from the LiteLLM admin API so the catalog is republished.

## 5. LiteLLM owned model shape

Each published Instance becomes a LiteLLM model row. Public routing fields use the current Instance slug; durable ownership metadata uses the immutable Instance ID:

```json
{
  "model_name": "<instance.slug>",
  "litellm_params": {
    "model": "openai/<instance.slug>",
    "api_base": "<llamarack /v1>",
    "api_key": "<generated LlamaRack key>",
    "custom_llm_provider": "openai"
  },
  "model_info": {
    "id": "<litellm uuid>",
    "llamarack_managed": true,
    "llamarack_instance_id": "<immutable instance.id>"
  }
}
```

Reconcile rules:

1. `GET /model/info`; ignore rows without `model_info.llamarack_managed`.
2. Match owned rows by immutable `model_info.llamarack_instance_id`.
3. Create missing enabled Instances (`POST /model/new`) with current `instance.slug` as `model_name` and `openai/<instance.slug>`.
4. Update drifted owned rows (`POST /model/update`), including public rename-in-place after an Instance slug change while keeping the ownership ID unchanged.
5. During the stable-identity migration, adopt a legacy managed row whose ownership metadata still contains the pre-migration Instance slug when that slug uniquely maps to the migrated Instance; update it in place rather than creating a duplicate.
6. Delete owned rows whose immutable Instance owner is disabled or gone (`POST /model/delete` by LiteLLM `model_info.id`).
7. Never mutate unmanaged models.

If LiteLLM returns `STORE_MODEL_IN_DB`, persist a clear last-sync error instructing the operator to enable `STORE_MODEL_IN_DB` on the proxy.

## 6. Management API

Mounted at `/api/v1/litellm` (management JWT or management/full API key; same class as Hugging Face admin APIs). Hidden principals are managed only through these routes.

| Method | Path | Behavior |
|--------|------|----------|
| GET | `/api/v1/litellm` | Status without secrets |
| PUT | `/api/v1/litellm` | Save URL / api_base / optional proxy key; ensure principal; test + reconcile |
| POST | `/api/v1/litellm/test` | Test connection only |
| POST | `/api/v1/litellm/sync` | Sync now |
| POST | `/api/v1/litellm/rotate` | Rotate managed inference key and republish |
| DELETE | `/api/v1/litellm` | Disconnect; body `{ "unpublish": true\|false }` |

`GET /api/v1/admin/summary` MAY include LiteLLM `configured`. Include `last_sync_ok` only after a sync has been attempted so the dashboard can distinguish never-synced from a failed sync.

## 7. Lifecycle hooks

After successful Instance create/update/duplicate/delete (including model bootstrap and provider imports), LlamaRack schedules a best-effort reconcile when LiteLLM is configured.

A name-only Instance edit does not change the published model name. An explicit slug change does and is reconciled against the same immutable owner ID.

On manager start, if proxy URL and proxy key are configured, run one reconcile.

## 8. Acceptance criteria

- Enabled Instances appear in LiteLLM with public names equal to their Instance slugs.
- `model_info.llamarack_instance_id` contains the immutable Instance ID, not the slug.
- Changing an Instance slug updates the existing owned LiteLLM row instead of changing ownership or creating a duplicate.
- Legacy pre-migration managed rows can be adopted and rewritten to immutable ownership on first reconcile.
- Disabled or deleted Instances are removed from LiteLLM on the next successful reconcile.
- Hidden service account never appears in `/api/v1/admin/service-accounts`.
- The managed inference key appears in `/api/v1/api-keys` and on `/api`.
- Public PATCH of the managed key cannot change name or owner. Its ID-based `instance_ids` allowlist is editable. Public rotate of the managed key returns 404.
- Secrets never appear in GET status JSON.
- Instance CRUD succeeds when LiteLLM is down.
