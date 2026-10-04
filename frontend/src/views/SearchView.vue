<template>
  <div>
    <div class="card">
      <h2>按描述搜索遗留物品</h2>
      <div style="display: flex; gap: 10px; flex-wrap: wrap">
        <input v-model="q" placeholder="住客提供的物品描述，如：黑色钱包" style="flex: 2; min-width: 220px" @keyup.enter="search" />
        <input v-model="roomNo" placeholder="房号（可选）" style="flex: 1; min-width: 120px" @keyup.enter="search" />
        <select v-model="status" style="flex: 1; min-width: 130px">
          <option value="">全部状态</option>
          <option value="stored">在库待认领</option>
          <option value="hold">暂停交付</option>
          <option value="delivered">已交付</option>
          <option value="shipped">已寄出</option>
        </select>
        <button @click="search">搜索</button>
      </div>
      <p style="font-size: 12px; color: #8b949e; margin-bottom: 0">
        提示：证件、现金类物品仅显示必要信息，完整详情需前台核验身份后在详情页查看。
      </p>
    </div>

    <div v-if="error" class="error-msg">{{ error }}</div>

    <div class="card">
      <table v-if="items.length">
        <thead>
          <tr>
            <th>封袋号</th><th>房号</th><th>发现时间</th><th>类别</th><th>描述</th><th>保管位置</th><th>状态</th><th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="it in items" :key="it.id">
            <td>{{ it.bag_no }}</td>
            <td>{{ it.room_no }}</td>
            <td>{{ fmtTime(it.found_at) }}</td>
            <td>
              {{ CATEGORY_LABELS[it.category] }}
              <span v-if="it.sensitive" class="badge sensitive">敏感</span>
            </td>
            <td style="max-width: 220px">{{ it.description }}</td>
            <td>{{ it.storage_loc }}</td>
            <td><span class="badge" :class="it.status">{{ STATUS_LABELS[it.status] }}</span></td>
            <td><button class="ghost" @click="$router.push(`/items/${it.id}`)">详情</button></td>
          </tr>
        </tbody>
      </table>
      <p v-else style="color: #8b949e">暂无匹配的物品记录</p>
    </div>
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import { api, CATEGORY_LABELS, STATUS_LABELS } from '../api'

const q = ref('')
const roomNo = ref('')
const status = ref('')
const items = ref([])
const error = ref('')

function fmtTime(t) {
  return t ? new Date(t).toLocaleString('zh-CN', { hour12: false }) : ''
}

async function search() {
  error.value = ''
  try {
    items.value = await api.searchItems({ q: q.value, room_no: roomNo.value, status: status.value })
  } catch (e) {
    error.value = e.message
  }
}

onMounted(search)
</script>
