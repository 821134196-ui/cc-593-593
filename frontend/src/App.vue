<template>
  <template v-if="isLogin">
    <router-view />
  </template>
  <template v-else>
    <header class="topbar">
      <div class="brand">酒店遗留物保管与认领系统</div>
      <nav>
        <template v-if="user">
          <router-link v-if="user.role === 'housekeeping'" to="/register">物品登记</router-link>
          <router-link to="/items">{{ user.role === 'housekeeping' ? '台账浏览' : '搜索认领' }}</router-link>
        </template>
      </nav>
      <div class="user" v-if="user">
        <b>{{ user.full_name }}</b>
        （{{ roleLabel }}）
        <button class="ghost small" style="margin-left:12px" @click="logout">退出</button>
      </div>
    </header>
    <main class="container">
      <router-view />
    </main>
  </template>
</template>

<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getUser, clearSession } from './lib/api'

const route = useRoute()
const router = useRouter()
const user = computed(() => getUser())
const isLogin = computed(() => route.path === '/login')
const roleLabel = computed(() => user.value?.role === 'housekeeping' ? '保洁' : '前台')

function logout() {
  clearSession()
  router.push('/login')
}
</script>
