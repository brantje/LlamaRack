import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import OpenAI from 'openai';
import { VERSION as OPENAI_VERSION } from 'openai/version';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.dirname(HERE);
const versions = JSON.parse(fs.readFileSync(path.join(ROOT, 'versions.json'), 'utf8'));
const contract = JSON.parse(fs.readFileSync(path.join(ROOT, 'contract.json'), 'utf8'));
const forbidden = new Set(contract.forbidden_public_fields);

function requiredEnv(name) {
  const value = (process.env[name] || '').trim();
  if (!value) throw new Error(`missing required environment variable: ${name}`);
  return value;
}

function optionalEnv(name) {
  const value = (process.env[name] || '').trim();
  return value || null;
}

function requiredCapabilities() {
  return new Set((process.env.LLAMARACK_REQUIRED_CAPABILITIES || '').split(',').map((x) => x.trim()).filter(Boolean));
}

class NotApplicable extends Error {}

function capabilityModel(capability, envName) {
  const model = optionalEnv(envName);
  if (model) return model;
  if (requiredCapabilities().has(capability)) {
    throw new Error(`capability ${capability} is required but fixture ${envName} is missing`);
  }
  throw new NotApplicable(`no fixture supplied for capability ${capability}`);
}

function redact(value) {
  let text = String(value);
  for (const name of ['LLAMARACK_API_KEY', 'LLAMARACK_MANAGEMENT_KEY', 'LLAMARACK_LITELLM_MASTER_KEY']) {
    const secret = process.env[name];
    if (secret) text = text.split(secret).join('<redacted>');
  }
  text = text.replace(/Bearer\s+[A-Za-z0-9._~+/=-]+/g, 'Bearer <redacted>');
  return text.length > 1200 ? `${text.slice(0, 1200)}…` : text;
}

function assertNoForbidden(value) {
  if (Array.isArray(value)) {
    for (const child of value) assertNoForbidden(child);
    return;
  }
  if (!value || typeof value !== 'object') return;
  for (const [key, child] of Object.entries(value)) {
    if (forbidden.has(key)) throw new Error(`public response contains forbidden field: ${key}`);
    assertNoForbidden(child);
  }
}

async function runCase(results, name, fn) {
  try {
    const detail = await fn();
    results.push({ name, status: 'pass', detail: detail ?? null });
  } catch (error) {
    if (error instanceof NotApplicable) {
      results.push({ name, status: 'not_applicable', detail: redact(error.message) });
      return;
    }
    results.push({
      name,
      status: 'fail',
      detail: redact(error?.message || error),
      exception: error?.constructor?.name || 'Error',
      stack: redact(error?.stack || ''),
    });
  }
}

async function expectStatus(fn, expected) {
  try {
    await fn();
  } catch (error) {
    const actual = error?.status;
    if (actual !== expected) throw new Error(`expected HTTP ${expected}, got ${actual}: ${redact(error?.message || error)}`);
    return { status: actual };
  }
  throw new Error(`expected HTTP ${expected}, request succeeded`);
}

function artifactDir() {
  const dir = process.env.LLAMARACK_ARTIFACT_DIR || 'artifacts/compat';
  fs.mkdirSync(dir, { recursive: true });
  return dir;
}

async function main() {
  if (OPENAI_VERSION !== versions.openai_node) {
    throw new Error(`OpenAI Node version mismatch: installed=${OPENAI_VERSION} pinned=${versions.openai_node}`);
  }
  const baseURL = requiredEnv('LLAMARACK_BASE_URL').replace(/\/$/, '');
  const apiKey = requiredEnv('LLAMARACK_API_KEY');
  const chatModel = requiredEnv('LLAMARACK_CHAT_MODEL');
  const responsesModel = optionalEnv('LLAMARACK_RESPONSES_MODEL') || chatModel;
  const client = new OpenAI({ apiKey, baseURL, timeout: 90_000, maxRetries: 0 });
  const results = [];

  await runCase(results, 'models.list', async () => {
    const page = await client.models.list();
    assertNoForbidden(page);
    const ids = page.data.map((item) => item.id);
    if (!ids.includes(chatModel)) throw new Error(`chat fixture ${chatModel} missing from /v1/models`);
    if (new Set(ids).size !== ids.length) throw new Error('/v1/models contains duplicate IDs');
    return { ids, object: page.object };
  });

  await runCase(results, 'models.retrieve', async () => {
    const item = await client.models.retrieve(chatModel);
    assertNoForbidden(item);
    if (item.id !== chatModel) throw new Error(`retrieve returned ${item.id}, expected ${chatModel}`);
    return { id: item.id, object: item.object };
  });

  await runCase(results, 'chat.basic', async () => {
    const response = await client.chat.completions.create({
      model: chatModel,
      messages: [{ role: 'user', content: 'Reply with the single word OK.' }],
      max_tokens: 16,
      temperature: 0,
    });
    if (response.model !== chatModel) throw new Error(`response model=${response.model}, expected ${chatModel}`);
    if (!response.choices.length) throw new Error('chat response has no choices');
    return { id: response.id, model: response.model, finish_reason: response.choices[0].finish_reason, has_usage: Boolean(response.usage) };
  });

  await runCase(results, 'chat.streaming_sdk', async () => {
    const stream = await client.chat.completions.create({
      model: chatModel,
      messages: [{ role: 'user', content: 'Count from one to three using words only.' }],
      max_tokens: 32,
      temperature: 0,
      stream: true,
    });
    let chunks = 0;
    let sawContent = false;
    const finishReasons = [];
    for await (const chunk of stream) {
      chunks += 1;
      if (chunk.model && chunk.model !== chatModel) throw new Error(`stream model=${chunk.model}, expected ${chatModel}`);
      for (const choice of chunk.choices || []) {
        if (choice.delta?.content) sawContent = true;
        if (choice.finish_reason) finishReasons.push(choice.finish_reason);
      }
    }
    if (!chunks) throw new Error('stream produced no SDK chunks');
    if (!sawContent) throw new Error('stream produced no content deltas');
    return { chunks, finish_reasons: finishReasons };
  });

  await runCase(results, 'responses.basic', async () => {
    const response = await client.responses.create({
      model: responsesModel,
      input: 'Reply with the single word OK.',
      max_output_tokens: 16,
    });
    if (!response.id) throw new Error('Responses result has no id');
    if (response.model && response.model !== responsesModel) throw new Error(`Responses model=${response.model}, expected ${responsesModel}`);
    return { id: response.id, model: response.model, status: response.status, output_items: response.output?.length || 0 };
  });

  await runCase(results, 'errors.invalid_auth', () => expectStatus(
    () => new OpenAI({ apiKey: 'sk-llamarack-compat-invalid', baseURL, timeout: 30_000, maxRetries: 0 }).models.list(),
    401,
  ));

  await runCase(results, 'errors.unknown_model', () => expectStatus(
    () => client.chat.completions.create({
      model: '__llamarack_compat_missing_instance__',
      messages: [{ role: 'user', content: 'test' }],
      max_tokens: 1,
    }),
    404,
  ));

  await runCase(results, 'errors.invalid_request', () => expectStatus(
    () => client.chat.completions.create({
      model: '',
      messages: [{ role: 'user', content: 'test' }],
      max_tokens: 1,
    }),
    400,
  ));

  await runCase(results, 'embeddings.basic', async () => {
    const model = capabilityModel('embeddings', 'LLAMARACK_EMBEDDING_MODEL');
    const response = await client.embeddings.create({ model, input: 'LlamaRack compatibility probe' });
    if (!response.data?.[0]?.embedding?.length) throw new Error('embedding response contains no vector');
    return { model: response.model, dimensions: response.data[0].embedding.length };
  });

  await runCase(results, 'tools.round_trip', async () => {
    const model = capabilityModel('tools', 'LLAMARACK_TOOLS_MODEL');
    const tools = [{
      type: 'function',
      function: {
        name: 'compat_echo',
        description: 'Echo an integer exactly.',
        parameters: {
          type: 'object',
          properties: { value: { type: 'integer' } },
          required: ['value'],
          additionalProperties: false,
        },
      },
    }];
    const first = await client.chat.completions.create({
      model,
      messages: [{ role: 'user', content: 'Call compat_echo with value 7.' }],
      tools,
      tool_choice: { type: 'function', function: { name: 'compat_echo' } },
      max_tokens: 128,
      temperature: 0,
    });
    const message = first.choices[0]?.message;
    const call = message?.tool_calls?.[0];
    if (!call) throw new Error('tool-capable fixture returned no tool_calls');
    const args = JSON.parse(call.function.arguments);
    if (args.value !== 7) throw new Error(`unexpected tool arguments: ${JSON.stringify(args)}`);
    const assistant = { role: 'assistant', content: message.content, tool_calls: message.tool_calls };
    const second = await client.chat.completions.create({
      model,
      messages: [
        { role: 'user', content: 'Call compat_echo with value 7.' },
        assistant,
        { role: 'tool', tool_call_id: call.id, content: '7' },
      ],
      tools,
      max_tokens: 64,
      temperature: 0,
    });
    if (!second.choices.length) throw new Error('tool round-trip returned no choices');
    return { tool_call_id: call.id, arguments: args, round_trip: true };
  });

  await runCase(results, 'structured_output.json_schema', async () => {
    const model = capabilityModel('structured_output', 'LLAMARACK_STRUCTURED_MODEL');
    const response = await client.chat.completions.create({
      model,
      messages: [{ role: 'user', content: 'Return an object with ok=true and no other fields.' }],
      response_format: {
        type: 'json_schema',
        json_schema: {
          name: 'compat_result',
          strict: true,
          schema: {
            type: 'object',
            properties: { ok: { type: 'boolean' } },
            required: ['ok'],
            additionalProperties: false,
          },
        },
      },
      max_tokens: 64,
      temperature: 0,
    });
    const parsed = JSON.parse(response.choices[0]?.message?.content || '');
    if (JSON.stringify(parsed) !== JSON.stringify({ ok: true })) throw new Error(`unexpected structured response: ${JSON.stringify(parsed)}`);
    return parsed;
  });

  const evidence = {
    suite: 'openai-node',
    target: process.env.LLAMARACK_TARGET_ID || 'unspecified',
    versions,
    installed_openai: OPENAI_VERSION,
    node: process.version,
    required_capabilities: [...requiredCapabilities()].sort(),
    fixtures: { chat: chatModel, responses: responsesModel },
    results,
  };
  const output = path.join(artifactDir(), 'openai-node.json');
  fs.writeFileSync(output, `${JSON.stringify(evidence, null, 2)}\n`);
  console.log(output);
  const failures = results.filter((item) => item.status === 'fail');
  if (failures.length) throw new Error(`compatibility probe failed: ${failures.map((item) => item.name).join(', ')}`);
}

main().catch((error) => {
  console.error(redact(error?.stack || error));
  process.exitCode = 1;
});
