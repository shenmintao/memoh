<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useQuery } from '@pinia/colada'
import { useI18n } from 'vue-i18n'
import {
  Button, ConfirmPopover, Dialog, DialogBody, DialogDescription, DialogHeader,
  DialogPanel, DialogTitle, FieldStack, FormDialogShell, FormStack, InlineLoadingRow,
  Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
  SettingsRow, SettingsSection, Textarea, toast, useClipboard,
} from '@felinic/ui'
import {
  deleteUsersMePushEndpointsById, getBots, getUsersMeChannelIdentities,
  getUsersMePushEndpoints, getUsersMePushEndpointsByIdDeliveries,
  patchUsersMePushEndpointsById, postUsersMePushEndpoints,
  postUsersMePushEndpointsByIdRotateKey, postUsersMePushEndpointsByIdTest,
} from '@memohai/sdk'
import type { HandlersPushEndpoint, HandlersPushReceipt } from '@memohai/sdk'
import { sdkApiBaseUrl } from '@/lib/api-client'
import { resolveApiErrorMessage } from '@/utils/api-error'
import { channelTypeDisplayName } from '@/utils/channel-type-label'

// Wireframe: Profile -> one summary row -> Manage dialog (endpoint rows).
// Each row opens one focused detail; creation and one-time credentials are
// separate dialogs. No nested cards or live message-body history.
const { t } = useI18n()
const { copyText } = useClipboard()
const manageOpen = ref(false)
const createOpen = ref(false)
const detailOpen = ref(false)
const keyOpen = ref(false)
const busy = ref(false)
const selected = ref<HandlersPushEndpoint>()
const issued = ref<HandlersPushEndpoint>()
const receipts = ref<HandlersPushReceipt[]>([])
const receiptsLoading = ref(false)
const receiptError = ref('')
const name = ref('')
const botId = ref('')
const identityId = ref('')
const formError = ref('')
const { data, isLoading, error, refetch } = useQuery({
  key: ['push-endpoints'],
  query: async () => (await getUsersMePushEndpoints({ throwOnError: true })).data,
})
const { data: botsData } = useQuery({
  key: ['push-bots'],
  query: async () => (await getBots({ throwOnError: true })).data,
  enabled: () => manageOpen.value || createOpen.value || detailOpen.value,
})
const { data: identitiesData } = useQuery({
  key: ['connected-accounts'],
  query: async () => (await getUsersMeChannelIdentities({ throwOnError: true })).data,
  enabled: () => manageOpen.value || createOpen.value || detailOpen.value,
})
const endpoints = computed(() => data.value?.items ?? [])
const bots = computed(() => (botsData.value?.items ?? []).filter(bot => bot.current_user_permissions?.includes('manage')))
const identities = computed(() => identitiesData.value?.items ?? [])
const summary = computed(() => t('push.summary', { count: endpoints.value.length }))
const webhookUrl = computed(() => issued.value?.path ? sdkApiBaseUrl().replace(/\/$/, '') + issued.value.path : '')
const genericExample = '{\n  "title": "NAS",\n  "text": "备份已完成",\n  "event_id": "backup-20260911"\n}'
const smsExample = '{\n  "sender": "{sender}",\n  "message": "{message}",\n  "timestamp": "{timestamp}",\n  "local_number": "{local_number}",\n  "remark": "{remark}"\n}'

function destination(item: HandlersPushEndpoint) {
  const bot = botsData.value?.items?.find(bot => bot.id === item.bot_id)
  const identity = identities.value.find(identity => identity.channel_identity_id === item.channel_identity_id)
  return [bot?.display_name || bot?.name || item.bot_id,
    identity ? channelTypeDisplayName(t, identity.channel_type) : t('push.unlinked')].join(' · ')
}
function errorMessage(err: unknown) {
  return resolveApiErrorMessage(err, t('push.failed'))
}
function beginCreate() {
  manageOpen.value = false
  name.value = ''
  botId.value = ''
  identityId.value = ''
  formError.value = ''
  createOpen.value = true
}
async function create() {
  if (busy.value) return
  busy.value = true
  formError.value = ''
  try {
    const { data } = await postUsersMePushEndpoints({
      body: { name: name.value.trim(), bot_id: botId.value, channel_identity_id: identityId.value },
      throwOnError: true,
    })
    issued.value = data
    createOpen.value = false
    keyOpen.value = true
    await refetch()
  } catch (err) { formError.value = errorMessage(err) } finally { busy.value = false }
}
async function loadReceipts() {
  const id = selected.value?.id
  if (!id) return
  receiptsLoading.value = true
  receiptError.value = ''
  try {
    const { data } = await getUsersMePushEndpointsByIdDeliveries({ path: { id }, throwOnError: true })
    if (selected.value?.id === id) receipts.value = data?.items ?? []
  } catch (err) { receiptError.value = errorMessage(err) } finally { receiptsLoading.value = false }
}
function inspect(item: HandlersPushEndpoint) {
  selected.value = item
  receipts.value = []
  manageOpen.value = false
  detailOpen.value = true
  void loadReceipts()
}
async function act(action: 'toggle' | 'rotate' | 'delete' | 'test') {
  const item = selected.value
  if (!item?.id || busy.value) return
  busy.value = true
  try {
    const path = { id: item.id }
    if (action === 'toggle') {
      const { data } = await patchUsersMePushEndpointsById({ path, body: { enabled: !item.enabled }, throwOnError: true })
      selected.value = data
    } else if (action === 'rotate') {
      const { data } = await postUsersMePushEndpointsByIdRotateKey({ path, throwOnError: true })
      issued.value = data
      detailOpen.value = false
      keyOpen.value = true
    } else if (action === 'delete') {
      await deleteUsersMePushEndpointsById({ path, throwOnError: true })
      detailOpen.value = false
      manageOpen.value = true
    } else {
      await postUsersMePushEndpointsByIdTest({ path, throwOnError: true })
      toast.success(t('push.testAccepted'))
    }
    await refetch()
  } catch (err) { toast.error(errorMessage(err)) } finally {
    busy.value = false
    if (detailOpen.value) await loadReceipts()
  }
}
async function copy(value: string) {
  if (await copyText(value)) toast.success(t('common.copied'))
}
watch(keyOpen, open => { if (!open) issued.value = undefined })
</script>

<template>
  <SettingsSection :title="$t('push.title')">
    <InlineLoadingRow
      v-if="isLoading"
      surface="card-row"
    >
      {{ $t('common.loading') }}
    </InlineLoadingRow>
    <SettingsRow
      v-else-if="error"
      :label="$t('push.loadFailed')"
      stack="sm"
    >
      <Button
        variant="outline"
        size="sm"
        @click="refetch()"
      >
        {{ $t('common.retry') }}
      </Button>
    </SettingsRow>
    <SettingsRow
      v-else
      :label="summary"
      :description="$t('push.description')"
      stack="sm"
    >
      <Button
        variant="outline"
        size="sm"
        @click="manageOpen = true"
      >
        {{ $t('push.manage') }}
      </Button>
    </SettingsRow>
  </SettingsSection>

  <Dialog v-model:open="manageOpen">
    <DialogPanel width="xl">
      <DialogHeader>
        <DialogTitle>{{ $t('push.title') }}</DialogTitle>
        <DialogDescription>{{ $t('push.description') }}</DialogDescription>
      </DialogHeader>
      <DialogBody>
        <SettingsRow
          v-for="item in endpoints"
          :key="item.id"
          :label="item.name"
          :description="destination(item)"
          stack="sm"
        >
          <Button
            variant="outline"
            size="sm"
            @click="inspect(item)"
          >
            {{ item.enabled ? $t('push.enabled') : $t('push.paused') }}
          </Button>
        </SettingsRow>
        <SettingsRow
          :label="endpoints.length ? $t('push.addAnother') : $t('push.empty')"
          :description="$t('push.createHint')"
          stack="sm"
        >
          <Button
            variant="outline"
            size="sm"
            @click="beginCreate"
          >
            {{ $t('push.create') }}
          </Button>
        </SettingsRow>
      </DialogBody>
    </DialogPanel>
  </Dialog>

  <FormDialogShell
    v-model:open="createOpen"
    :title="$t('push.create')"
    :description="$t('push.createHint')"
    :cancel-text="$t('common.cancel')"
    :submit-text="$t('common.create')"
    :loading="busy"
    :submit-disabled="busy || !name.trim() || !botId || !identityId"
    @submit="create"
  >
    <template #body>
      <FormStack>
        <FieldStack
          :label="$t('push.name')"
          for="push-name"
        >
          <Input
            id="push-name"
            v-model="name"
            :placeholder="$t('push.nameExample')"
            :maxlength="100"
          />
        </FieldStack>
        <FieldStack
          :label="$t('push.bot')"
          for="push-bot"
        >
          <Select v-model="botId">
            <SelectTrigger id="push-bot">
              <SelectValue :placeholder="$t('push.selectBot')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem
                v-for="bot in bots"
                :key="bot.id"
                :value="bot.id!"
              >
                {{ bot.display_name || bot.name }}
              </SelectItem>
            </SelectContent>
          </Select>
        </FieldStack>
        <FieldStack
          :label="$t('push.recipient')"
          :help="$t('push.recipientHint')"
          for="push-recipient"
        >
          <Select v-model="identityId">
            <SelectTrigger id="push-recipient">
              <SelectValue :placeholder="$t('push.selectRecipient')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem
                v-for="identity in identities"
                :key="identity.id"
                :value="identity.channel_identity_id!"
              >
                {{ channelTypeDisplayName(t, identity.channel_type) }} · {{ identity.channel_identity_display_name || identity.channel_subject_id }}
              </SelectItem>
            </SelectContent>
          </Select>
        </FieldStack>
        <p
          v-if="formError"
          role="alert"
          class="text-sm text-destructive"
        >
          {{ formError }}
        </p>
      </FormStack>
    </template>
  </FormDialogShell>

  <Dialog v-model:open="keyOpen">
    <DialogPanel width="xl">
      <DialogHeader>
        <DialogTitle>{{ $t('push.configure') }}</DialogTitle>
        <DialogDescription>{{ $t('push.keyOnce') }}</DialogDescription>
      </DialogHeader>
      <DialogBody>
        <FormStack>
          <FieldStack
            :label="$t('push.url')"
            :help="$t('push.urlHint')"
            for="push-url"
          >
            <Textarea
              id="push-url"
              :model-value="webhookUrl"
              readonly
              :rows="3"
            />
            <Button
              variant="outline"
              size="sm"
              @click="copy(webhookUrl)"
            >
              {{ $t('common.copy') }}
            </Button>
          </FieldStack>
          <FieldStack
            :label="$t('push.generic')"
            :help="$t('push.genericHint')"
            for="push-generic"
          >
            <Textarea
              id="push-generic"
              :model-value="genericExample"
              readonly
              :rows="5"
            />
            <Button
              variant="outline"
              size="sm"
              @click="copy(genericExample)"
            >
              {{ $t('common.copy') }}
            </Button>
          </FieldStack>
          <FieldStack
            :label="$t('push.sms')"
            :help="$t('push.smsHint')"
            for="push-sms"
          >
            <Textarea
              id="push-sms"
              :model-value="smsExample"
              readonly
              :rows="7"
            />
            <Button
              variant="outline"
              size="sm"
              @click="copy(smsExample)"
            >
              {{ $t('common.copy') }}
            </Button>
          </FieldStack>
        </FormStack>
      </DialogBody>
    </DialogPanel>
  </Dialog>

  <Dialog v-model:open="detailOpen">
    <DialogPanel width="xl">
      <DialogHeader>
        <DialogTitle>{{ selected?.name }}</DialogTitle>
        <DialogDescription>{{ selected ? destination(selected) : '' }}</DialogDescription>
      </DialogHeader>
      <DialogBody>
        <SettingsRow
          :label="$t('push.state')"
          :description="selected?.enabled ? $t('push.enabled') : $t('push.paused')"
          stack="sm"
        >
          <Button
            variant="outline"
            size="sm"
            :disabled="busy"
            @click="act('toggle')"
          >
            {{ selected?.enabled ? $t('push.pause') : $t('push.enable') }}
          </Button>
        </SettingsRow>
        <SettingsRow
          :label="$t('push.test')"
          :description="$t('push.testHint')"
          stack="sm"
        >
          <Button
            variant="outline"
            size="sm"
            :disabled="busy || !selected?.enabled"
            @click="act('test')"
          >
            {{ $t('push.sendTest') }}
          </Button>
        </SettingsRow>
        <SettingsRow
          :label="$t('push.key')"
          :description="$t('push.rotateHint')"
          stack="sm"
        >
          <ConfirmPopover
            :message="$t('push.rotateConfirm')"
            :cancel-text="$t('common.cancel')"
            :confirm-text="$t('push.rotate')"
            @confirm="act('rotate')"
          >
            <template #trigger>
              <Button
                variant="outline"
                size="sm"
                :disabled="busy"
              >
                {{ $t('push.rotate') }}
              </Button>
            </template>
          </ConfirmPopover>
        </SettingsRow>
        <SettingsRow
          :label="$t('push.deliveries')"
          :description="$t('push.receiptHint')"
          stack="sm"
        >
          <Button
            variant="outline"
            size="sm"
            :loading="receiptsLoading"
            @click="loadReceipts"
          >
            {{ $t('common.refresh') }}
          </Button>
        </SettingsRow>
        <InlineLoadingRow
          v-if="receiptsLoading"
          surface="card-row"
        >
          {{ $t('common.loading') }}
        </InlineLoadingRow>
        <SettingsRow
          v-else-if="receiptError"
          :label="receiptError"
        />
        <SettingsRow
          v-else-if="!receipts.length"
          :label="$t('push.noDeliveries')"
        />
        <template v-else>
          <SettingsRow
            v-for="receipt in receipts"
            :key="receipt.id"
            :label="$t('push.deliveryStatus.' + receipt.status)"
            :description="receipt.error_code ? $t('push.channelError') : receipt.updated_at ? new Date(receipt.updated_at).toLocaleString() : ''"
          />
        </template>
        <SettingsRow
          :label="$t('push.delete')"
          :description="$t('push.deleteHint')"
          stack="sm"
        >
          <ConfirmPopover
            :message="$t('push.deleteConfirm')"
            :cancel-text="$t('common.cancel')"
            :confirm-text="$t('common.delete')"
            variant="destructive"
            @confirm="act('delete')"
          >
            <template #trigger>
              <Button
                variant="destructive"
                size="sm"
                :disabled="busy"
              >
                {{ $t('common.delete') }}
              </Button>
            </template>
          </ConfirmPopover>
        </SettingsRow>
      </DialogBody>
    </DialogPanel>
  </Dialog>
</template>
