export function newPlaygroundSessionID() {
  if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID()
  return `pg-${Date.now().toString(16)}-${Math.random().toString(16).slice(2, 10)}`
}
