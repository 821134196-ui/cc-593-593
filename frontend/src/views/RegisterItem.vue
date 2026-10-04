<template>
  <div>
    <div class="panel">
      <h2>登记客房遗留物</h2>
      <div v-if="msg" class="msg ok">{{ msg }}</div>
      <div v-if="error" class="msg error">{{ error }}</div>
      <form @submit.prevent="submit">
        <div class="row">
          <label class="field">
            <span>房号 *</span>
            <input v-model="form.room_number" placeholder="如 1206" required />
          </label>
          <label class="field">
            <span>发现时间</span>
            <input v-model="form.found_at" type="datetime-local" />
          </label>
        </div>
        <div class="row">
          <label class="field">
            <span>物品分类 *</span>
            <select v-model="form.category">
              <option value="electronics">电子产品</option>
              <option value="clothing">衣物</option>
              <option value="jewelry">首饰/贵重物品</option>
              <option value="id">证件</option>
              <option value="cash">现金</option>
              <option value="other">其他</option>
            </select>
          </label>
          <label class="field">
            <span>封袋号（封签号）*</span>
            <input v-model="form.bag_seal_number" placeholder="如 SEAL-20261004-001" required />
          </label>
        </div>
        <label class="field">
          <span>完整描述（内部台账，可含证件号/金额）</span>
          <textarea v-model="form.description" placeholder="详细特征；证件类填证件号码，现金类填金额" required></textarea>
        </label>
        <label class="field" v-if="sensitive">
          <span>对外必要信息（普通搜索只显示这一条）</span>
          <textarea v-model="form.public_summary" :placeholder="defaultSummary"></textarea>
          <div class="hint">证件与现金属于敏感物品，搜索列表不会展示完整描述/号码/金额。</div>
        </label>
        <div class="row">
          <label class="field">
            <span>保管位置 *</span>
            <input v-model="form.storage_location" placeholder="如 一楼失物保管柜 A1" required />
          </label>
          <label class="field">
            <span>保管天数</span>
            <input v-model.number="form.retention_days" type="number" min="1" placeholder="默认 90 天" />
          </label>
        </div>
        <label class="field">
          <span>物品照片（本地保存）</span>
          <input type="file" accept="image/*" @change="onPhoto" />
        </label>
        <button :disabled="loading">{{ loading ? '提交中…' : '登记入库' }}</button>
      </form>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref, computed } from 'vue'
import { api } from '../lib/api'

const form = reactive({
  room_number: '',
  found_at: '',
  category: 'electronics',
  description: '',
  public_summary: '',
  bag_seal_number: '',
  storage_location: '',
  retention_days: 90
})
const photo = ref(null)
const error = ref('')
const msg = ref('')
const loading = ref(false)

const sensitive = computed(() => form.category === 'id' || form.category === 'cash')
const defaultSummary = computed(() =>
  form.category === 'id' ? '证件类物品（详情已隐藏，请到前台核实）' : '现金类物品（金额已隐藏，请到前台核实）')

function onPhoto(e) {
  photo.value = e.target.files[0] || null
}

async function submit() {
  error.value = ''
  msg.value = ''
  loading.value = true
  try {
    const fd = new FormData()
    Object.entries(form).forEach(([k, v]) => fd.append(k, v ?? ''))
    if (!form.public_summary && sensitive.value) fd.set('public_summary', defaultSummary.value)
    if (photo.value) fd.append('photo', photo.value)
    const res = await api.postForm('/items', fd)
    msg.value = `登记成功：封袋号 ${res.item.bag_seal_number}，已写入交接台账。`
    form.room_number = form.description = form.public_summary = form.bag_seal_number = form.storage_location = ''
    photo.value = null
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>
