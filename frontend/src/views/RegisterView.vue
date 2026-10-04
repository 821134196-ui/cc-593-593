<template>
  <div class="card" style="max-width: 640px; margin: 0 auto">
    <h2>遗留物品登记（保洁）</h2>
    <div v-if="error" class="error-msg">{{ error }}</div>
    <div v-if="done" class="ok-msg">
      登记成功，封袋号 {{ done.bag_no }}。<a href="javascript:;" @click="reset">继续登记下一件</a>
    </div>

    <form v-if="!done" @submit.prevent="submit">
      <div class="form-row">
        <label>封袋号 *（唯一，封口标签上的编号）</label>
        <input v-model="form.bag_no" required placeholder="如 BAG-20261004-001" />
      </div>
      <div class="form-row">
        <label>房号 *</label>
        <input v-model="form.room_no" required placeholder="如 808" />
      </div>
      <div class="form-row">
        <label>发现时间 *</label>
        <input v-model="form.found_at" type="datetime-local" required />
      </div>
      <div class="form-row">
        <label>类别 *</label>
        <select v-model="form.category">
          <option v-for="(label, key) in CATEGORY_LABELS" :key="key" :value="key">{{ label }}</option>
        </select>
        <p v-if="form.category === 'document' || form.category === 'cash'" style="font-size: 12px; color: #c0392b; margin: 6px 0 0">
          敏感物品：普通搜索中只显示必要信息，完整详情需前台核验身份后查看。
        </p>
      </div>
      <div class="form-row">
        <label>物品描述</label>
        <textarea v-model="form.description" rows="3" style="width: 100%" placeholder="外观、品牌、内含物等"></textarea>
      </div>
      <div class="form-row">
        <label>保管位置</label>
        <input v-model="form.storage_loc" placeholder="默认：前台保管柜" />
      </div>
      <div class="form-row">
        <label>照片（可多选，jpg/png/webp）</label>
        <input type="file" multiple accept="image/*" @change="onFiles" />
      </div>
      <button type="submit" :disabled="submitting">{{ submitting ? '提交中…' : '登记入库' }}</button>
    </form>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { api, CATEGORY_LABELS } from '../api'

const empty = { bag_no: '', room_no: '', found_at: '', category: 'general', description: '', storage_loc: '' }
const form = reactive({ ...empty })
const files = ref([])
const error = ref('')
const done = ref(null)
const submitting = ref(false)

function onFiles(e) {
  files.value = [...e.target.files]
}

async function submit() {
  error.value = ''
  submitting.value = true
  try {
    const fd = new FormData()
    fd.append('bag_no', form.bag_no)
    fd.append('room_no', form.room_no)
    fd.append('category', form.category)
    fd.append('description', form.description)
    fd.append('storage_loc', form.storage_loc)
    fd.append('found_at', new Date(form.found_at).toISOString())
    for (const f of files.value) fd.append('photos', f)
    done.value = await api.createItem(fd)
  } catch (e) {
    error.value = e.message
  } finally {
    submitting.value = false
  }
}

function reset() {
  Object.assign(form, empty)
  files.value = []
  done.value = null
}
</script>
