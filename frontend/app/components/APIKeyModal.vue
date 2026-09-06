<script setup lang="ts">
import type { CalendarDate } from '@internationalized/date'
import type { ComponentPublicInstance } from 'vue'
import type { APIKey, APIKeyType, Instance, ServiceAccount, User } from '~/composables/useManager'
import {
  API_KEY_DATE_LOCALE,
  API_KEY_TYPE_ITEMS,
  API_KEY_TYPE_TOOLTIP,
  calendarDateToExpiresOn,
  decodeAPIKeyOwner,
  enabledOwnerItems,
  expiresOnToCalendarDate,
  isAPIKeyDateUnavailable,
  ownerValueForKey,
  type APIKeyDraft
} from '~/utils/apiKeys'

const open = defineModel<boolean>('open', { default: false })

const props = defineProps<{
  phase: 'form' | 'secret'
  editing: boolean
  submitting?: boolean
  secret?: string
  users: Array<Pick<User, 'id' | 'username' | 'enabled'>>
  serviceAccounts: Array<Pick<ServiceAccount, 'id' | 'name' | 'enabled'>>
  instances: Instance[]
  currentUserId?: number
  initialKey?: APIKey | null
}>()

const emit = defineEmits<{
  save: [draft: APIKeyDraft]
  close: []
}>()

const name = ref('default')
const owner = ref('')
const keyType = ref<APIKeyType>('inference')
const instanceIds = ref<string[]>([])
const expiresOn = shallowRef<CalendarDate | undefined>()
const inputDate = ref<{ inputsRef: ComponentPublicInstance[] } | null>(null)
const copyError = ref('')

const ownerItems = computed(() => {
  const current = props.initialKey
    ? { kind: props.initialKey.owner_kind, id: props.initialKey.owner_id }
    : undefined
  return enabledOwnerItems(props.users, props.serviceAccounts, current)
})

const instanceItems = computed(() => {
  const live = props.instances.map(instance => ({ label: `${instance.name} (${instance.slug})`, value: instance.id }))
  const known = new Set(live.map(item => item.value))
  const extras = [...new Set([...(props.initialKey?.missing_instance_ids || []), ...instanceIds.value])]
    .filter(id => !known.has(id))
    .map(id => ({ label: `${id} (missing)`, value: id }))
  return [...live, ...extras]
})

const missingInstanceIds = computed(() => props.initialKey?.missing_instance_ids || [])
const managed = computed(() => Boolean(props.editing && props.initialKey?.managed))
const canSave = computed(() => name.value.trim().length > 0 && !!owner.value)
const modalTitle = computed(() => {
  if (props.phase === 'secret') return 'Copy this key now'
  return props.editing ? 'Edit API key' : 'Create API key'
})

function resetForm() {
  copyError.value = ''
  const key = props.initialKey
  if (key) {
    name.value = key.name
    owner.value = ownerValueForKey(key)
    keyType.value = key.key_type
    instanceIds.value = [...(key.instance_ids || [])]
    expiresOn.value = expiresOnToCalendarDate(key.expires_on)
    return
  }
  name.value = 'default'
  owner.value = props.currentUserId != null ? `user:${props.currentUserId}` : ''
  keyType.value = 'inference'
  instanceIds.value = []
  expiresOn.value = undefined
}

watch([open, () => props.phase, () => props.initialKey], ([isOpen, phase]) => {
  if (isOpen && phase === 'form') resetForm()
}, { immediate: true })

watch(keyType, type => {
  if (type !== 'inference') instanceIds.value = []
})

function close() {
  emit('close')
  open.value = false
}

function save() {
  if (!canSave.value || props.submitting) return
  const decoded = decodeAPIKeyOwner(owner.value)
  const draft: APIKeyDraft = {
    name: name.value.trim(),
    key_type: keyType.value,
    owner_user_id: decoded.owner_user_id ?? null,
    owner_service_account_id: decoded.owner_service_account_id ?? null,
    expires_on: calendarDateToExpiresOn(expiresOn.value)
  }
  if (keyType.value === 'inference') draft.instance_ids = [...instanceIds.value]
  emit('save', draft)
}

function clearExpires() {
  expiresOn.value = undefined
}
</script>

<template>
  <UApp>
  <UModal v-model:open="open" :title="modalTitle" :dismissible="phase === 'secret' || !submitting" @update:open="value => { if (!value) emit('close') }">
    <template #body>
      <div v-if="phase === 'secret'" data-testid="fresh-api-key" class="space-y-3">
        <p class="text-sm leading-6 text-[var(--neutral-800)]">Copy this key now. It will not be shown again.</p>
        <code class="block break-all font-mono text-[length:var(--font-size-table-body)] text-[var(--accent-700)]">{{ secret }}</code>
        <AppCopyButton
          :text="secret || ''"
          color="neutral"
          variant="soft"
          size="sm"
          error-message="Unable to copy API key. Select the key and copy it manually."
          data-testid="copy-key"
          @copied="copyError = ''"
          @error="message => copyError = message"
        />
        <p v-if="copyError" class="text-xs text-[var(--danger-700)]">{{ copyError }}</p>
      </div>

      <form v-else class="space-y-4" data-testid="api-key-form" @submit.prevent="save">
        <UFormField label="Name" required>
          <UInput v-model="name" data-testid="key-name" class="w-full" autocomplete="off" required :disabled="managed" />
        </UFormField>

        <UFormField v-if="managed" label="Owner" description="This LiteLLM key is owned by a hidden service account. Rotate it from Administration → LiteLLM.">
          <UInput :model-value="initialKey?.owner_name || 'LiteLLM'" class="w-full" disabled data-testid="api-key-owner-readonly" />
        </UFormField>
        <UFormField v-else label="Owner" required>
          <div data-testid="api-key-owner">
            <USelectMenu v-model="owner" class="w-full" :items="ownerItems" value-key="value" label-key="label" placeholder="Select owner" />
          </div>
        </UFormField>

        <UFormField v-if="!editing" label="Key type" :description="API_KEY_TYPE_TOOLTIP">
          <div data-testid="api-key-type">
            <USelectMenu v-model="keyType" class="w-full" :items="API_KEY_TYPE_ITEMS" value-key="value" label-key="label" description-key="description" :search-input="false" />
          </div>
        </UFormField>
        <UFormField v-else label="Key type" description="Key type cannot be changed after create.">
          <UInput :model-value="API_KEY_TYPE_ITEMS.find(item => item.value === keyType)?.label || keyType" class="w-full" disabled data-testid="api-key-type-readonly" />
        </UFormField>

        <UFormField
          v-if="keyType === 'inference'"
          label="Instances"
          description="Leave empty to allow all instances."
        >
          <div data-testid="api-key-instances">
            <USelectMenu v-model="instanceIds" class="w-full" :items="instanceItems" value-key="value" label-key="label" multiple placeholder="All instances" />
          </div>
          <p v-if="missingInstanceIds.length" class="mt-2 text-xs text-[var(--neutral-700)]">Missing instances stay on the allowlist and never collapse to all instances: <span class="font-mono">{{ missingInstanceIds.join(', ') }}</span></p>
        </UFormField>

        <UFormField label="Expires" description="Optional. Day, month, year (dd-mm-yyyy). Valid through the end of that UTC day.">
          <div class="flex items-center gap-2" data-testid="api-key-expires">
            <UInputDate
              ref="inputDate"
              v-model="expiresOn"
              :locale="API_KEY_DATE_LOCALE"
              class="min-w-0 flex-1"
              :is-date-unavailable="isAPIKeyDateUnavailable"
            >
              <template #trailing>
                <UPopover :reference="inputDate?.inputsRef[3]?.$el">
                  <UButton
                    color="neutral"
                    variant="link"
                    size="sm"
                    icon="i-lucide-calendar"
                    aria-label="Select a date"
                    class="cursor-pointer px-0"
                    data-testid="api-key-expires-picker"
                  />
                  <template #content>
                    <UCalendar
                      v-model="expiresOn"
                      :locale="API_KEY_DATE_LOCALE"
                      :is-date-unavailable="isAPIKeyDateUnavailable"
                      class="p-2"
                    />
                  </template>
                </UPopover>
              </template>
            </UInputDate>
            <AppButton v-if="expiresOn" type="button" intent="ghost" size="xs" data-testid="clear-api-key-expires" @click="clearExpires">Clear</AppButton>
          </div>
        </UFormField>
      </form>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <AppButton v-if="phase === 'secret'" intent="primary" data-testid="api-key-secret-done" @click="close">Done</AppButton>
        <template v-else>
          <AppButton type="button" intent="secondary" :disabled="submitting" @click="close">Cancel</AppButton>
          <AppButton type="button" intent="primary" data-testid="api-key-save" :loading="submitting" :disabled="!canSave" @click="save">{{ editing ? 'Save key' : 'Create key' }}</AppButton>
        </template>
      </div>
    </template>
    </UModal>
  </UApp>
</template>
