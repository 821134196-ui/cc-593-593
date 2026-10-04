<template>
  <div class="login-box">
    <div class="panel">
      <h2>登录</h2>
      <div v-if="error" class="msg error">{{ error }}</div>
      <form @submit.prevent="submit">
        <label class="field">
          <span>用户名</span>
          <input v-model="username" placeholder="clean1 / desk1" required />
        </label>
        <label class="field">
          <span>密码</span>
          <input v-model="password" type="password" required />
        </label>
        <button style="width:100%" :disabled="loading">{{ loading ? '登录中…' : '登 录' }}</button>
      </form>
      <div class="hint">
        演示账号：<br />
        保洁 clean1 / clean2，密码 clean123<br />
        前台 desk1 / desk2，密码 desk123
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, setSession } from '../lib/api'

const router = useRouter()
const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

async function submit() {
  error.value = ''
  loading.value = true
  try {
    const res = await api.post('/auth/login', { username: username.value, password: password.value })
    setSession(res.token, res.user)
    router.push('/items')
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}
</script>
